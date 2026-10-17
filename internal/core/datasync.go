package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/transfer"
)

// Data Synchronization compares the rows of tables in two databases of the same
// engine by primary key and makes the target's rows match the source's.

// DataSyncTable maps a source table to a target table, with an optional WHERE
// condition applied to both sides.
type DataSyncTable struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Condition string `json:"condition,omitempty"`
	// Key and Message are filled in by DataSyncTables.
	Key     []string `json:"key,omitempty"`
	Message string   `json:"message,omitempty"`
}

type DataSyncRequest struct {
	Source Endpoint        `json:"source"`
	Target Endpoint        `json:"target"`
	Tables []DataSyncTable `json:"tables"`
}

// DataSyncTables lists the source tables with their default mapping: the target
// table of the same name and the primary key.
func (m *Manager) DataSyncTables(ctx context.Context, source, target Endpoint) ([]DataSyncTable, error) {
	src, dst, err := m.syncConns(source, target)
	if err != nil {
		return nil, err
	}
	srcObjs, err := src.ListObjects(ctx, source.Database, source.Schema, model.KindTable)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	dstObjs, err := dst.ListObjects(ctx, target.Database, target.Schema, model.KindTable)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	have := map[string]string{}
	for _, o := range dstObjs {
		have[strings.ToLower(o.Name)] = o.Name
	}
	out := make([]DataSyncTable, 0, len(srcObjs))
	for _, o := range srcObjs {
		t := DataSyncTable{Source: o.Name, Target: have[strings.ToLower(o.Name)]}
		d, err := src.TableDetail(ctx, model.ObjectRef{Database: source.Database, Schema: source.Schema, Name: o.Name, Kind: model.KindTable})
		if err == nil {
			for _, c := range d.Columns {
				if c.PrimaryKey {
					t.Key = append(t.Key, c.Name)
				}
			}
		}
		switch {
		case t.Target == "":
			t.Message = "Target table does not exist"
		case len(t.Key) == 0:
			t.Message = "No primary key to compare by"
		}
		out = append(out, t)
	}
	return out, nil
}

func (m *Manager) syncConns(source, target Endpoint) (driver.Conn, driver.Conn, error) {
	src, err := m.conn(source.ConnID)
	if err != nil {
		return nil, nil, fmt.Errorf("source: %w", err)
	}
	dst, err := m.conn(target.ConnID)
	if err != nil {
		return nil, nil, fmt.Errorf("target: %w", err)
	}
	if src.Dialect().Name != dst.Dialect().Name {
		return nil, nil, fmt.Errorf("data synchronization needs two databases of the same engine (%s and %s); use Data Transfer between engines", src.Dialect().Name, dst.Dialect().Name)
	}
	return src, dst, nil
}

// DataSyncResult is one table's comparison.
type DataSyncResult struct {
	Source  string   `json:"source"`
	Target  string   `json:"target"`
	Key     []string `json:"key"`
	Columns []string `json:"columns"`
	Insert  int      `json:"insert"`
	Update  int      `json:"update"`
	Delete  int      `json:"delete"`
	Same    int      `json:"same"`
	Message string   `json:"message,omitempty"`
}

type DataSyncComparison struct {
	ID     string           `json:"id"`
	Tables []DataSyncResult `json:"tables"`
}

// DataSyncRecord is a differing row: the source and/or target values, and the
// columns that differ (for updates).
type DataSyncRecord struct {
	Op      string    `json:"op"` // insert, update, delete
	Source  []*string `json:"source,omitempty"`
	Target  []*string `json:"target,omitempty"`
	Changed []int     `json:"changed,omitempty"`
}

type dataSync struct {
	req    DataSyncRequest
	src    driver.Conn
	dst    driver.Conn
	tables []*syncTable
	// While comparing, every table is read on these two sessions rather than
	// logging in twice per table.
	ss, ts    driver.Session
	comparing bool
}

// syncKeep is how many records of each kind a table's comparison keeps for
// viewing and deploying. Past it only the count grows, and deploying compares
// the table again to stream the rest.
var syncKeep = 10000

var syncOps = [3]string{"insert", "update", "delete"}

func opIndex(op string) int {
	switch op {
	case "insert":
		return 0
	case "update":
		return 1
	}
	return 2
}

type syncTable struct {
	res   DataSyncResult
	sref  model.ObjectRef // source
	ref   model.ObjectRef // target
	types []string        // per column, for literals
	// identity "always" columns need OVERRIDING SYSTEM VALUE (PostgreSQL)
	overriding bool
	fks        []string // referenced tables (for ordering)
	recs       []DataSyncRecord
	kept       [3]int // records kept per op (insert, update, delete)

	srcSQL, dstSQL string // the compared columns, ordered by key when ordered
	// ordered compares as a merge join of both sides read in key order; false
	// when the database's key order can't be matched here, and the target's
	// rows are then held by key instead.
	ordered bool
	numeric []bool // per key column: compare as numbers
}

func (t *syncTable) count(op int) int {
	return [3]int{t.res.Insert, t.res.Update, t.res.Delete}[op]
}

// CompareData compares the mapped tables. The result stays on the server until
// DiscardDataSync, for paging records and deploying.
func (m *Manager) CompareData(ctx context.Context, req DataSyncRequest) (DataSyncComparison, error) {
	src, dst, err := m.syncConns(req.Source, req.Target)
	if err != nil {
		return DataSyncComparison{}, err
	}
	if req.Source == req.Target {
		return DataSyncComparison{}, errors.New("the source and the target are the same")
	}
	ds := &dataSync{req: req, src: src, dst: dst, comparing: true}
	defer ds.closeSessions()
	for _, mt := range req.Tables {
		st := &syncTable{res: DataSyncResult{Source: mt.Source, Target: mt.Target}}
		if err := m.compareTable(ctx, ds, mt, st); err != nil {
			if ctx.Err() != nil {
				return DataSyncComparison{}, ctx.Err()
			}
			st.res.Message = err.Error()
		}
		ds.tables = append(ds.tables, st)
	}
	id := newID()
	m.syncMu.Lock()
	if m.syncs == nil {
		m.syncs = map[string]*dataSync{}
	}
	m.syncs[id] = ds
	m.syncMu.Unlock()
	out := DataSyncComparison{ID: id}
	for _, t := range ds.tables {
		out.Tables = append(out.Tables, t.res)
	}
	return out, nil
}

func (m *Manager) compareTable(ctx context.Context, ds *dataSync, mt DataSyncTable, st *syncTable) error {
	if mt.Target == "" {
		return errors.New("Target table does not exist")
	}
	r := ds.req
	st.sref = model.ObjectRef{Database: r.Source.Database, Schema: r.Source.Schema, Name: mt.Source, Kind: model.KindTable}
	st.ref = model.ObjectRef{Database: r.Target.Database, Schema: r.Target.Schema, Name: mt.Target, Kind: model.KindTable}
	sd, err := ds.src.LoadDesign(ctx, st.sref)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	td, err := ds.dst.LoadDesign(ctx, st.ref)
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	for _, f := range sd.ForeignKeys {
		st.fks = append(st.fks, strings.ToLower(f.RefTable))
	}
	key := mt.Key
	if len(key) == 0 && sd.PrimaryKey != nil {
		key = sd.PrimaryKey.Columns
	}
	if len(key) == 0 {
		return errors.New("No primary key to compare by")
	}
	scols := map[string]design.Column{}
	for _, c := range sd.Columns {
		scols[strings.ToLower(c.Name)] = c
	}
	tcols := map[string]design.Column{}
	for _, c := range td.Columns {
		tcols[strings.ToLower(c.Name)] = c
	}
	for _, k := range key {
		if _, ok := tcols[strings.ToLower(k)]; !ok {
			return fmt.Errorf("Key column %s is missing in the target", k)
		}
	}
	// Columns in both tables, keys first; generated columns are computed by the target.
	var cols []string
	isKey := map[string]bool{}
	for _, k := range key {
		isKey[strings.ToLower(k)] = true
		cols = append(cols, tcols[strings.ToLower(k)].Name)
	}
	for _, c := range sd.Columns {
		tc, ok := tcols[strings.ToLower(c.Name)]
		if !ok || isKey[strings.ToLower(c.Name)] || c.GeneratedExpr != "" || tc.GeneratedExpr != "" {
			continue
		}
		cols = append(cols, tc.Name)
		if tc.Identity == "always" {
			st.overriding = true
		}
	}
	for _, k := range key {
		if tcols[strings.ToLower(k)].Identity == "always" {
			st.overriding = true
		}
	}
	st.res.Key = cols[:len(key)]
	st.res.Columns = cols

	// Both sides are read in key order, compared the way the ORDER BY sorts:
	// numbers by value, everything else by bytes (text under a binary collation).
	st.ordered = true
	st.numeric = make([]bool, len(key))
	sorder := make([]string, len(key))
	torder := make([]string, len(key))
	for i, k := range key {
		sc, ok := scols[strings.ToLower(k)]
		tc := tcols[strings.ToLower(k)]
		skind, tkind := keyKindOf(ds.src.Dialect().Name, sc.Type), keyKindOf(ds.dst.Dialect().Name, tc.Type)
		if !ok || skind != tkind {
			st.ordered = false
		}
		st.numeric[i] = skind == keyNumber
		sorder[i] = keyOrder(ds.src.Dialect(), sc.Name, skind)
		torder[i] = keyOrder(ds.dst.Dialect(), tc.Name, tkind)
	}
	sel := func(d driver.Dialect, ref model.ObjectRef, order []string) string {
		q := make([]string, len(cols))
		for i, c := range cols {
			q[i] = d.QuoteIdent(c)
		}
		s := "SELECT " + strings.Join(q, ", ") + " FROM " + d.Qualified(ref)
		if strings.TrimSpace(mt.Condition) != "" {
			s += " WHERE (" + mt.Condition + "\n)"
		}
		return s + " ORDER BY " + strings.Join(order, ", ")
	}
	st.srcSQL = sel(ds.src.Dialect(), st.sref, sorder)
	st.dstSQL = sel(ds.dst.Dialect(), st.ref, torder)

	keep := func(rec DataSyncRecord) error {
		i := opIndex(rec.Op)
		switch i {
		case 0:
			st.res.Insert++
		case 1:
			st.res.Update++
		default:
			st.res.Delete++
		}
		if st.kept[i] < syncKeep {
			st.recs = append(st.recs, rec)
			st.kept[i]++
		}
		return nil
	}
	same, err := ds.diff(ctx, st, keep)
	if errors.Is(err, errSyncOrder) {
		// The database sorts the key differently from the comparison here (a
		// collation or type it doesn't share): hold the target by key instead.
		st.recs, st.kept, st.ordered = nil, [3]int{}, false
		st.res.Insert, st.res.Update, st.res.Delete = 0, 0, 0
		same, err = ds.diff(ctx, st, keep)
	}
	st.res.Same = same
	return err
}

type keyKind uint8

const (
	keyOther  keyKind = iota // sorted natively, compared as bytes (dates, uuids, binary)
	keyText                  // sorted under a binary collation, compared as bytes
	keyNumber                // sorted and compared by value
)

// keyKindOf classifies a key column's type for ordering.
func keyKindOf(dialect, typ string) keyKind {
	t := strings.ToLower(strings.TrimSpace(typ))
	if dialect == "sqlite" {
		// Type affinity rules.
		switch {
		case strings.Contains(t, "int"):
			return keyNumber
		case strings.Contains(t, "char"), strings.Contains(t, "clob"), strings.Contains(t, "text"):
			return keyText
		case t == "", strings.Contains(t, "blob"):
			return keyOther
		}
		return keyNumber
	}
	if i := strings.IndexAny(t, "( "); i > 0 && !strings.HasPrefix(t, "double precision") && !strings.HasPrefix(t, "character varying") {
		t = t[:i]
	}
	switch t {
	case "tinyint", "smallint", "mediumint", "int", "integer", "bigint", "int2", "int4", "int8",
		"serial", "smallserial", "bigserial", "serial2", "serial4", "serial8",
		"decimal", "numeric", "float", "float4", "float8", "double", "double precision", "real":
		return keyNumber
	case "char", "varchar", "character", "character varying", "bpchar", "text", "tinytext", "mediumtext", "longtext",
		"nchar", "nvarchar", "citext", "name", "enum", "set":
		return keyText
	}
	return keyOther
}

// keyOrder is a key column's ORDER BY term, sorting text by bytes and NULLs first.
func keyOrder(d driver.Dialect, col string, kind keyKind) string {
	q := d.QuoteIdent(col)
	switch d.Name {
	case "postgres":
		if kind == keyText {
			q += ` COLLATE "C"`
		}
		return q + " NULLS FIRST"
	case "mysql":
		if kind == keyText {
			return "CAST(" + q + " AS BINARY)"
		}
	case "sqlite":
		if kind == keyText {
			return q + " COLLATE BINARY"
		}
	}
	return q
}

// compareKeys orders two rows by their first len(numeric) cells the way
// keyOrder sorts them. Rows with byte-equal keys compare equal, and only those.
func compareKeys(a, b []*string, numeric []bool) int {
	for i, num := range numeric {
		x, y := a[i], b[i]
		switch {
		case x == nil && y == nil:
			continue
		case x == nil:
			return -1
		case y == nil:
			return 1
		}
		if num {
			if c := compareNumbers(*x, *y); c != 0 {
				return c
			}
		}
		if c := strings.Compare(*x, *y); c != 0 {
			return c
		}
	}
	return 0
}

// compareNumbers compares two decimal strings by value; 0 when either isn't one.
func compareNumbers(x, y string) int {
	if a, err := strconv.ParseInt(x, 10, 64); err == nil {
		if b, err := strconv.ParseInt(y, 10, 64); err == nil {
			return cmp.Compare(a, b)
		}
	}
	a, ok := new(big.Rat).SetString(x)
	if !ok {
		return 0
	}
	b, ok := new(big.Rat).SetString(y)
	if !ok {
		return 0
	}
	return a.Cmp(b)
}

// errSyncOrder: a side's rows didn't arrive in the order compareKeys expects.
var errSyncOrder = errors.New("rows are not in key order")

// diff streams the table's differences to emit and returns the number of
// identical rows. Ordered, it holds one row per side; otherwise it holds the
// target's rows by key.
func (ds *dataSync) diff(ctx context.Context, st *syncTable, emit func(DataSyncRecord) error) (int, error) {
	sss, tss, done, err := ds.sessions(ctx)
	if err != nil {
		return 0, err
	}
	defer done()
	trows, err := tss.Query(ctx, st.dstSQL)
	if err != nil {
		return 0, fmt.Errorf("target: %w", err)
	}
	defer trows.Close()
	srows, err := sss.Query(ctx, st.srcSQL)
	if err != nil {
		return 0, fmt.Errorf("source: %w", err)
	}
	defer srows.Close()
	if st.types == nil {
		st.types = make([]string, len(st.res.Columns))
		for i, c := range srows.Columns() {
			if i < len(st.types) {
				st.types[i] = c.Type
			}
		}
	}
	src := &syncCursor{rows: srows, side: "source", n: len(st.res.Columns), numeric: st.numeric, ordered: st.ordered}
	dst := &syncCursor{rows: trows, side: "target", n: len(st.res.Columns), numeric: st.numeric, ordered: st.ordered}
	nk := len(st.numeric)
	same := 0
	// match emits an update when the rows differ outside the key.
	match := func(s, t []*string) error {
		var changed []int
		for i := nk; i < len(s); i++ {
			if !sameCell(s[i], t[i]) {
				changed = append(changed, i)
			}
		}
		if len(changed) == 0 {
			same++
			return nil
		}
		return emit(DataSyncRecord{Op: "update", Source: s, Target: t, Changed: changed})
	}

	if !st.ordered {
		target := map[string][]*string{}
		var order []string
		for dst.next() {
			k := keyString(dst.row, nk)
			target[k] = dst.row
			order = append(order, k)
		}
		if dst.err != nil {
			return 0, dst.err
		}
		for src.next() {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}
			k := keyString(src.row, nk)
			cur, ok := target[k]
			if !ok {
				err = emit(DataSyncRecord{Op: "insert", Source: src.row})
			} else {
				delete(target, k)
				err = match(src.row, cur)
			}
			if err != nil {
				return 0, err
			}
		}
		if src.err != nil {
			return 0, src.err
		}
		for _, k := range order {
			if row, ok := target[k]; ok {
				if err := emit(DataSyncRecord{Op: "delete", Target: row}); err != nil {
					return 0, err
				}
			}
		}
		return same, nil
	}

	// A merge join: the smaller key is missing from the other side.
	src.next()
	dst.next()
	for {
		// Check before acting: a side that failed (or broke key order) looks finished.
		if src.err != nil {
			return 0, src.err
		}
		if dst.err != nil {
			return 0, dst.err
		}
		if src.row == nil && dst.row == nil {
			return same, nil
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		c := 0
		switch {
		case dst.row == nil:
			c = -1
		case src.row == nil:
			c = 1
		default:
			c = compareKeys(src.row, dst.row, st.numeric)
		}
		switch {
		case c < 0:
			err = emit(DataSyncRecord{Op: "insert", Source: src.row})
			src.next()
		case c > 0:
			err = emit(DataSyncRecord{Op: "delete", Target: dst.row})
			dst.next()
		default:
			err = match(src.row, dst.row)
			src.next()
			dst.next()
		}
		if err != nil {
			return 0, err
		}
	}
}

// sessions returns the comparison's sessions, opening them on first use; done
// closes them unless they're kept for the comparison's next table.
func (ds *dataSync) sessions(ctx context.Context) (src, dst driver.Session, done func(), err error) {
	r := ds.req
	open := func() (driver.Session, driver.Session, error) {
		t, err := ds.dst.NewSession(ctx, r.Target.Database, r.Target.Schema)
		if err != nil {
			return nil, nil, err
		}
		s, err := ds.src.NewSession(ctx, r.Source.Database, r.Source.Schema)
		if err != nil {
			t.Close()
			return nil, nil, err
		}
		return s, t, nil
	}
	if ds.comparing {
		if ds.ss == nil {
			if ds.ss, ds.ts, err = open(); err != nil {
				return nil, nil, nil, err
			}
		}
		return ds.ss, ds.ts, func() {}, nil
	}
	if src, dst, err = open(); err != nil {
		return nil, nil, nil, err
	}
	return src, dst, func() { src.Close(); dst.Close() }, nil
}

func (ds *dataSync) closeSessions() {
	ds.comparing = false
	if ds.ss != nil {
		ds.ss.Close()
		ds.ts.Close()
		ds.ss, ds.ts = nil, nil
	}
}

// syncCursor reads one side's rows; row is nil once they run out or fail.
type syncCursor struct {
	rows    driver.Rows
	side    string
	n       int
	numeric []bool
	ordered bool
	row     []*string
	err     error
}

func (c *syncCursor) next() bool {
	prev := c.row
	c.row = nil
	if c.err != nil {
		return false
	}
	row := make([]*string, c.n)
	if !c.rows.Next(row) {
		if err := c.rows.Err(); err != nil {
			c.err = fmt.Errorf("%s: %w", c.side, err)
		}
		return false
	}
	if c.ordered && prev != nil && compareKeys(prev, row, c.numeric) >= 0 {
		c.err = errSyncOrder
		return false
	}
	c.row = row
	return true
}

func keyString(row []*string, nk int) string {
	var b strings.Builder
	for _, v := range row[:nk] {
		if v == nil {
			b.WriteString("\x00N")
		} else {
			b.WriteString(*v)
		}
		b.WriteString("\x00|")
	}
	return b.String()
}

func sameCell(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (m *Manager) dataSync(id string) (*dataSync, error) {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	ds, ok := m.syncs[id]
	if !ok {
		return nil, errors.New("the comparison is no longer available; compare again")
	}
	return ds, nil
}

// DiscardDataSync frees a comparison.
func (m *Manager) DiscardDataSync(id string) {
	m.syncMu.Lock()
	delete(m.syncs, id)
	m.syncMu.Unlock()
}

type DataSyncPage struct {
	Columns []string         `json:"columns"`
	Key     int              `json:"key"` // the first Key columns are the key
	Records []DataSyncRecord `json:"records"`
	Total   int              `json:"total"` // records kept for listing
	More    int              `json:"more,omitempty"`
}

// DataSyncRecords pages one table's differing records; op filters ("" = all differences).
// Past syncKeep records of a kind, More counts the ones not listed.
func (m *Manager) DataSyncRecords(id, table, op string, offset, limit int) (DataSyncPage, error) {
	ds, err := m.dataSync(id)
	if err != nil {
		return DataSyncPage{}, err
	}
	for _, t := range ds.tables {
		if t.res.Source != table {
			continue
		}
		page := DataSyncPage{Columns: t.res.Columns, Key: len(t.res.Key), Records: []DataSyncRecord{}}
		n := 0
		for _, r := range t.recs {
			if op != "" && r.Op != op {
				continue
			}
			if n >= offset && len(page.Records) < limit {
				page.Records = append(page.Records, r)
			}
			n++
		}
		page.Total = n
		for i, o := range syncOps {
			if op == "" || op == o {
				page.More += t.count(i) - t.kept[i]
			}
		}
		return page, nil
	}
	return DataSyncPage{}, fmt.Errorf("table %s isn't in the comparison", table)
}

// DataSyncSelection says which actions to deploy for a table.
type DataSyncSelection struct {
	Table  string `json:"table"`
	Insert bool   `json:"insert"`
	Update bool   `json:"update"`
	Delete bool   `json:"delete"`
}

func (s DataSyncSelection) has(op int) bool {
	return [3]bool{s.Insert, s.Update, s.Delete}[op]
}

// syncStep is one table's action in deployment order.
type syncStep struct {
	t  *syncTable
	op int
}

// plan orders the chosen actions: deletes (referencing tables first), updates,
// then inserts (referenced tables first).
func (ds *dataSync) plan(sel []DataSyncSelection) []syncStep {
	want := map[string]DataSyncSelection{}
	for _, s := range sel {
		want[s.Table] = s
	}
	var tables []*syncTable
	for _, t := range ds.tables {
		if _, ok := want[t.res.Source]; ok && t.res.Message == "" {
			tables = append(tables, t)
		}
	}
	tables = syncOrder(tables)
	var out []syncStep
	add := func(t *syncTable, op int) {
		if want[t.res.Source].has(op) && t.count(op) > 0 {
			out = append(out, syncStep{t, op})
		}
	}
	for i := len(tables) - 1; i >= 0; i-- {
		add(tables[i], 2)
	}
	for _, t := range tables {
		add(t, 1)
	}
	for _, t := range tables {
		add(t, 0)
	}
	return out
}

// DataSyncScript builds the deployment script for preview. Records past those
// kept are compared again while deploying, and a comment stands in for them.
func (m *Manager) DataSyncScript(id string, sel []DataSyncSelection) ([]string, error) {
	ds, err := m.dataSync(id)
	if err != nil {
		return nil, err
	}
	d := ds.dst.Dialect()
	var out []string
	for _, s := range ds.plan(sel) {
		emit := func(q string) error { out = append(out, q); return nil }
		w := s.t.writer(s.op, d, emit)
		for _, r := range s.t.recs {
			if opIndex(r.Op) == s.op {
				_ = w.add(r)
			}
		}
		_ = w.flush()
		if more := s.t.count(s.op) - s.t.kept[s.op]; more > 0 {
			out = append(out, fmt.Sprintf("-- %d more %s(s) on %s are compared again and applied when deploying", more, syncOps[s.op], s.t.res.Target))
		}
	}
	return out, nil
}

// run feeds a step's statements to emit: from the kept records, or from a
// fresh comparison when there were more than were kept.
func (ds *dataSync) run(ctx context.Context, s syncStep, emit func(string) error) error {
	w := s.t.writer(s.op, ds.dst.Dialect(), emit)
	if s.t.kept[s.op] == s.t.count(s.op) {
		for _, r := range s.t.recs {
			if opIndex(r.Op) == s.op {
				if err := w.add(r); err != nil {
					return err
				}
			}
		}
		return w.flush()
	}
	_, err := ds.diff(ctx, s.t, func(r DataSyncRecord) error {
		if opIndex(r.Op) != s.op {
			return nil
		}
		return w.add(r)
	})
	if errors.Is(err, errSyncOrder) {
		return fmt.Errorf("%s changed since the comparison; compare again", s.t.res.Target)
	}
	if err != nil {
		return err
	}
	return w.flush()
}

// statements estimates how many statements a step makes, for progress.
func (s syncStep) statements() int {
	n := s.t.count(s.op)
	if s.op == 1 {
		return n
	}
	return (n + syncBatch - 1) / syncBatch
}

// syncOrder puts referenced tables before the tables referencing them.
func syncOrder(ts []*syncTable) []*syncTable {
	by := map[string]*syncTable{}
	for _, t := range ts {
		by[strings.ToLower(t.res.Source)] = t
	}
	var out []*syncTable
	state := map[*syncTable]int{}
	var visit func(t *syncTable)
	visit = func(t *syncTable) {
		if state[t] != 0 {
			return
		}
		state[t] = 1
		for _, r := range t.fks {
			if p, ok := by[r]; ok && p != t {
				visit(p)
			}
		}
		state[t] = 2
		out = append(out, t)
	}
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].res.Source < ts[j].res.Source })
	for _, t := range ts {
		visit(t)
	}
	return out
}

func (t *syncTable) lit(v *string, i int, d driver.Dialect) string {
	return transfer.Literal(v, t.types[i], d.Name)
}

func (t *syncTable) where(row []*string, d driver.Dialect) string {
	parts := make([]string, len(t.res.Key))
	for i, k := range t.res.Key {
		if row[i] == nil {
			parts[i] = d.QuoteIdent(k) + " IS NULL"
		} else {
			parts[i] = d.QuoteIdent(k) + " = " + t.lit(row[i], i, d)
		}
	}
	return strings.Join(parts, " AND ")
}

const syncBatch = 100

// syncWriter turns one kind of record into statements: inserts and deletes in
// batches of syncBatch rows, an update per row.
type syncWriter struct {
	add   func(DataSyncRecord) error
	flush func() error
}

func (t *syncTable) writer(op int, d driver.Dialect, emit func(string) error) syncWriter {
	switch op {
	case 0:
		return t.inserts(d, emit)
	case 1:
		return t.updates(d, emit)
	}
	return t.deletes(d, emit)
}

func (t *syncTable) inserts(d driver.Dialect, emit func(string) error) syncWriter {
	cols := make([]string, len(t.res.Columns))
	for i, c := range t.res.Columns {
		cols[i] = d.QuoteIdent(c)
	}
	head := "INSERT INTO " + d.Qualified(t.ref) + " (" + strings.Join(cols, ", ") + ") "
	if t.overriding {
		head += "OVERRIDING SYSTEM VALUE "
	}
	head += "VALUES "
	var vals []string
	flush := func() error {
		if len(vals) == 0 {
			return nil
		}
		q := head + strings.Join(vals, ", ")
		vals = vals[:0]
		return emit(q)
	}
	return syncWriter{
		add: func(r DataSyncRecord) error {
			cells := make([]string, len(r.Source))
			for i, v := range r.Source {
				cells[i] = t.lit(v, i, d)
			}
			vals = append(vals, "("+strings.Join(cells, ", ")+")")
			if len(vals) == syncBatch {
				return flush()
			}
			return nil
		},
		flush: flush,
	}
}

func (t *syncTable) updates(d driver.Dialect, emit func(string) error) syncWriter {
	return syncWriter{
		add: func(r DataSyncRecord) error {
			sets := make([]string, len(r.Changed))
			for j, i := range r.Changed {
				sets[j] = d.QuoteIdent(t.res.Columns[i]) + " = " + t.lit(r.Source[i], i, d)
			}
			return emit("UPDATE " + d.Qualified(t.ref) + " SET " + strings.Join(sets, ", ") + " WHERE " + t.where(r.Source, d))
		},
		flush: func() error { return nil },
	}
}

func (t *syncTable) deletes(d driver.Dialect, emit func(string) error) syncWriter {
	var conds []string
	single := len(t.res.Key) == 1
	flush := func() error {
		if len(conds) == 0 {
			return nil
		}
		var q string
		if single {
			q = "DELETE FROM " + d.Qualified(t.ref) + " WHERE " + d.QuoteIdent(t.res.Key[0]) + " IN (" + strings.Join(conds, ", ") + ")"
		} else {
			q = "DELETE FROM " + d.Qualified(t.ref) + " WHERE (" + strings.Join(conds, ") OR (") + ")"
		}
		conds = conds[:0]
		return emit(q)
	}
	return syncWriter{
		add: func(r DataSyncRecord) error {
			switch {
			case single && r.Target[0] != nil:
				conds = append(conds, t.lit(r.Target[0], 0, d))
			case single:
				return emit("DELETE FROM " + d.Qualified(t.ref) + " WHERE " + t.where(r.Target, d))
			default:
				conds = append(conds, t.where(r.Target, d))
			}
			if len(conds) == syncBatch {
				return flush()
			}
			return nil
		},
		flush: flush,
	}
}

// DataSyncDeployRequest deploys chosen actions of a comparison.
type DataSyncDeployRequest struct {
	ID              string              `json:"id"`
	Tables          []DataSyncSelection `json:"tables"`
	ContinueOnError bool                `json:"continueOnError"`
	UseTransaction  bool                `json:"useTransaction"`
}

// errSyncStop ends a deployment after a failed statement.
var errSyncStop = errors.New("stop")

// DeployDataSync runs the chosen actions as a job, in one transaction when asked
// (rolled back on the first error unless continuing).
func (m *Manager) DeployDataSync(req DataSyncDeployRequest) (Job, error) {
	ds, err := m.dataSync(req.ID)
	if err != nil {
		return Job{}, err
	}
	steps := ds.plan(req.Tables)
	if len(steps) == 0 {
		return Job{}, errors.New("nothing to deploy")
	}
	total := 0
	for _, s := range steps {
		total += s.statements()
	}
	target := ds.req.Target
	d := ds.dst.Dialect()
	return m.startJob("datasync", "Data Synchronization", "statements", func(ctx context.Context, p *progress) (string, error) {
		s, err := ds.dst.NewSession(ctx, target.Database, target.Schema)
		if err != nil {
			return "", err
		}
		defer s.Close()
		p.set(0, int64(total))
		p.logf("[DSYNC] Data Synchronization start: %s → %s", endpointName(m, ds.req.Source), endpointName(m, target))
		tx := req.UseTransaction
		if tx {
			begin := "BEGIN"
			if d.Name == "mysql" {
				begin = "START TRANSACTION"
			}
			if _, err := s.Exec(ctx, begin); err != nil {
				return "", err
			}
		}
		rollback := func() {
			if tx {
				_, _ = s.Exec(context.Background(), "ROLLBACK")
			}
		}
		var rows int64
		n, ok, failed := 0, 0, 0
		var stmtErr error
		exec := func(st string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			n++
			c, err := s.Exec(ctx, st)
			if err != nil {
				failed++
				p.error("[DSYNC] %d> %s: %v", n, strings.SplitN(st, " VALUES ", 2)[0], err)
				if !req.ContinueOnError {
					stmtErr = fmt.Errorf("statement %d failed: %w", n, err)
					return errSyncStop
				}
				return nil
			}
			ok++
			rows += c
			p.setDone(int64(min(n, total)))
			return nil
		}
		for _, st := range steps {
			if err := ds.run(ctx, st, exec); err != nil {
				rollback()
				switch {
				case errors.Is(err, errSyncStop):
					if tx {
						p.logf("[DSYNC] Rolled back")
					}
					return "", stmtErr
				case ctx.Err() != nil:
					return "", ctx.Err()
				}
				return "", err
			}
		}
		if tx {
			if _, err := s.Exec(ctx, "COMMIT"); err != nil {
				return "", err
			}
		}
		msg := fmt.Sprintf("Finished deployment (Total: %d, Success: %d, Fail: %d), %d row(s) changed", n, ok, failed, rows)
		p.logf("[DSYNC] %s", msg)
		m.DiscardDataSync(req.ID) // the target has changed: compare again for fresh results
		return msg, nil
	}), nil
}
