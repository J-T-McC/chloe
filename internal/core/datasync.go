package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
}

type syncTable struct {
	res   DataSyncResult
	ref   model.ObjectRef // target
	types []string        // per column, for literals
	// identity "always" columns need OVERRIDING SYSTEM VALUE (PostgreSQL)
	overriding bool
	fks        []string // referenced tables (for ordering)
	recs       []DataSyncRecord
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
	ds := &dataSync{req: req, src: src, dst: dst}
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
	sref := model.ObjectRef{Database: r.Source.Database, Schema: r.Source.Schema, Name: mt.Source, Kind: model.KindTable}
	st.ref = model.ObjectRef{Database: r.Target.Database, Schema: r.Target.Schema, Name: mt.Target, Kind: model.KindTable}
	sd, err := ds.src.LoadDesign(ctx, sref)
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
	nk := len(key)

	sel := func(d driver.Dialect, ref model.ObjectRef) string {
		q := make([]string, len(cols))
		for i, c := range cols {
			q[i] = d.QuoteIdent(c)
		}
		s := "SELECT " + strings.Join(q, ", ") + " FROM " + d.Qualified(ref)
		if strings.TrimSpace(mt.Condition) != "" {
			s += " WHERE " + mt.Condition
		}
		return s
	}
	keyOf := func(row []*string) string {
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

	// The target's rows by key.
	tss, err := ds.dst.NewSession(ctx, r.Target.Database, r.Target.Schema)
	if err != nil {
		return err
	}
	defer tss.Close()
	trows, err := tss.Query(ctx, sel(ds.dst.Dialect(), st.ref))
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}
	target := map[string][]*string{}
	var order []string
	for {
		row := make([]*string, len(cols))
		if !trows.Next(row) {
			break
		}
		k := keyOf(row)
		target[k] = row
		order = append(order, k)
	}
	err = trows.Err()
	trows.Close()
	if err != nil {
		return fmt.Errorf("target: %w", err)
	}

	sss, err := ds.src.NewSession(ctx, r.Source.Database, r.Source.Schema)
	if err != nil {
		return err
	}
	defer sss.Close()
	srows, err := sss.Query(ctx, sel(ds.src.Dialect(), sref))
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	defer srows.Close()
	st.types = make([]string, len(cols))
	for i, c := range srows.Columns() {
		if i < len(st.types) {
			st.types[i] = c.Type
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		row := make([]*string, len(cols))
		if !srows.Next(row) {
			break
		}
		k := keyOf(row)
		cur, ok := target[k]
		if !ok {
			st.recs = append(st.recs, DataSyncRecord{Op: "insert", Source: row})
			st.res.Insert++
			continue
		}
		delete(target, k)
		var changed []int
		for i := nk; i < len(cols); i++ {
			if !sameCell(row[i], cur[i]) {
				changed = append(changed, i)
			}
		}
		if len(changed) == 0 {
			st.res.Same++
			continue
		}
		st.recs = append(st.recs, DataSyncRecord{Op: "update", Source: row, Target: cur, Changed: changed})
		st.res.Update++
	}
	if err := srows.Err(); err != nil {
		return fmt.Errorf("source: %w", err)
	}
	for _, k := range order {
		if row, ok := target[k]; ok {
			st.recs = append(st.recs, DataSyncRecord{Op: "delete", Target: row})
			st.res.Delete++
		}
	}
	return nil
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
	Total   int              `json:"total"`
}

// DataSyncRecords pages one table's differing records; op filters ("" = all differences).
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

// DataSyncScript builds the deployment script: deletes (referencing tables first),
// updates, then inserts (referenced tables first).
func (m *Manager) DataSyncScript(id string, sel []DataSyncSelection) ([]string, error) {
	ds, err := m.dataSync(id)
	if err != nil {
		return nil, err
	}
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
	d := ds.dst.Dialect()
	var out []string
	for i := len(tables) - 1; i >= 0; i-- {
		if t := tables[i]; want[t.res.Source].Delete {
			out = append(out, t.deletes(d)...)
		}
	}
	for _, t := range tables {
		if want[t.res.Source].Update {
			out = append(out, t.updates(d)...)
		}
	}
	for _, t := range tables {
		if want[t.res.Source].Insert {
			out = append(out, t.inserts(d)...)
		}
	}
	return out, nil
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

func (t *syncTable) inserts(d driver.Dialect) []string {
	cols := make([]string, len(t.res.Columns))
	for i, c := range t.res.Columns {
		cols[i] = d.QuoteIdent(c)
	}
	head := "INSERT INTO " + d.Qualified(t.ref) + " (" + strings.Join(cols, ", ") + ") "
	if t.overriding {
		head += "OVERRIDING SYSTEM VALUE "
	}
	head += "VALUES "
	var out, vals []string
	flush := func() {
		if len(vals) > 0 {
			out = append(out, head+strings.Join(vals, ", "))
			vals = vals[:0]
		}
	}
	for _, r := range t.recs {
		if r.Op != "insert" {
			continue
		}
		cells := make([]string, len(r.Source))
		for i, v := range r.Source {
			cells[i] = t.lit(v, i, d)
		}
		vals = append(vals, "("+strings.Join(cells, ", ")+")")
		if len(vals) == syncBatch {
			flush()
		}
	}
	flush()
	return out
}

func (t *syncTable) updates(d driver.Dialect) []string {
	var out []string
	for _, r := range t.recs {
		if r.Op != "update" {
			continue
		}
		sets := make([]string, len(r.Changed))
		for j, i := range r.Changed {
			sets[j] = d.QuoteIdent(t.res.Columns[i]) + " = " + t.lit(r.Source[i], i, d)
		}
		out = append(out, "UPDATE "+d.Qualified(t.ref)+" SET "+strings.Join(sets, ", ")+" WHERE "+t.where(r.Source, d))
	}
	return out
}

func (t *syncTable) deletes(d driver.Dialect) []string {
	var out, conds []string
	single := len(t.res.Key) == 1
	flush := func() {
		if len(conds) == 0 {
			return
		}
		if single {
			out = append(out, "DELETE FROM "+d.Qualified(t.ref)+" WHERE "+d.QuoteIdent(t.res.Key[0])+" IN ("+strings.Join(conds, ", ")+")")
		} else {
			out = append(out, "DELETE FROM "+d.Qualified(t.ref)+" WHERE ("+strings.Join(conds, ") OR (")+")")
		}
		conds = conds[:0]
	}
	for _, r := range t.recs {
		if r.Op != "delete" {
			continue
		}
		if single && r.Target[0] != nil {
			conds = append(conds, t.lit(r.Target[0], 0, d))
		} else if single {
			out = append(out, "DELETE FROM "+d.Qualified(t.ref)+" WHERE "+t.where(r.Target, d))
			continue
		} else {
			conds = append(conds, t.where(r.Target, d))
		}
		if len(conds) == syncBatch {
			flush()
		}
	}
	flush()
	return out
}

// DataSyncDeployRequest deploys chosen actions of a comparison.
type DataSyncDeployRequest struct {
	ID              string              `json:"id"`
	Tables          []DataSyncSelection `json:"tables"`
	ContinueOnError bool                `json:"continueOnError"`
	UseTransaction  bool                `json:"useTransaction"`
}

// DeployDataSync runs the script as a job, in one transaction when asked (rolled
// back on the first error unless continuing).
func (m *Manager) DeployDataSync(req DataSyncDeployRequest) (Job, error) {
	ds, err := m.dataSync(req.ID)
	if err != nil {
		return Job{}, err
	}
	stmts, err := m.DataSyncScript(req.ID, req.Tables)
	if err != nil {
		return Job{}, err
	}
	if len(stmts) == 0 {
		return Job{}, errors.New("nothing to deploy")
	}
	target := ds.req.Target
	d := ds.dst.Dialect()
	return m.startJob("datasync", "Data Synchronization", "statements", func(ctx context.Context, p *progress) (string, error) {
		s, err := ds.dst.NewSession(ctx, target.Database, target.Schema)
		if err != nil {
			return "", err
		}
		defer s.Close()
		p.set(0, int64(len(stmts)))
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
		var rows int64
		ok, failed := 0, 0
		for i, st := range stmts {
			if ctx.Err() != nil {
				if tx {
					_, _ = s.Exec(context.Background(), "ROLLBACK")
				}
				return "", ctx.Err()
			}
			n, err := s.Exec(ctx, st)
			if err != nil {
				failed++
				p.error("[DSYNC] %d> %s: %v", i+1, strings.SplitN(st, " VALUES ", 2)[0], err)
				if !req.ContinueOnError {
					if tx {
						_, _ = s.Exec(context.Background(), "ROLLBACK")
						p.logf("[DSYNC] Rolled back")
					}
					return "", fmt.Errorf("statement %d failed: %w", i+1, err)
				}
				continue
			}
			ok++
			rows += n
			p.setDone(int64(i + 1))
		}
		if tx {
			if _, err := s.Exec(ctx, "COMMIT"); err != nil {
				return "", err
			}
		}
		msg := fmt.Sprintf("Finished deployment (Total: %d, Success: %d, Fail: %d), %d row(s) changed", len(stmts), ok, failed, rows)
		p.logf("[DSYNC] %s", msg)
		m.DiscardDataSync(req.ID) // the target has changed: compare again for fresh results
		return msg, nil
	}), nil
}
