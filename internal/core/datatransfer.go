package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

// Data Transfer copies tables (structure and rows) and views from one database or
// schema to another, on the same server or another one, of the same engine or not.

// Endpoint is one side of a transfer.
type Endpoint struct {
	ConnID   string `json:"connId"`
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
}

// TransferOptions follow Navicat's Data Transfer options.
type TransferOptions struct {
	// Table options
	CreateTables      bool `json:"createTables"`
	IncludeIndexes    bool `json:"includeIndexes"`
	IncludeForeignKey bool `json:"includeForeignKeys"`
	IncludeChecks     bool `json:"includeChecks"`
	IncludeUniques    bool `json:"includeUniques"`
	IncludeAutoInc    bool `json:"includeAutoIncrement"`
	IncludeTriggers   bool `json:"includeTriggers"`
	IncludeCharset    bool `json:"includeCharset"`
	IncludeEngine     bool `json:"includeEngine"`
	IncludeOther      bool `json:"includeOtherOptions"`
	// "", "lower" or "upper"
	ConvertNames string `json:"convertNames,omitempty"`

	// Record options
	CreateRecords   bool `json:"createRecords"`
	EmptyTarget     bool `json:"emptyTarget"`
	UseTransaction  bool `json:"useTransaction"`
	ExtendedInserts bool `json:"extendedInserts"`
	// "", "ignore" or "replace"
	InsertMode string `json:"insertMode,omitempty"`

	// Other options
	ContinueOnError bool `json:"continueOnError"`
	DropTarget      bool `json:"dropTarget"`
	DropCascade     bool `json:"dropCascade"`
	CreateTargetDB  bool `json:"createTargetDatabase"`
}

// DefaultTransferOptions are Navicat's defaults.
func DefaultTransferOptions() TransferOptions {
	return TransferOptions{CreateTables: true, IncludeIndexes: true, IncludeForeignKey: true, IncludeChecks: true, IncludeUniques: true,
		IncludeAutoInc: true, IncludeTriggers: true, IncludeCharset: true, IncludeEngine: true,
		CreateRecords: true, UseTransaction: true, ExtendedInserts: true, DropTarget: true}
}

// DataTransferRequest says what to copy. With AllTables/AllViews set, the lists
// are ignored and every object present when the transfer runs is copied.
type DataTransferRequest struct {
	Source    Endpoint        `json:"source"`
	Target    Endpoint        `json:"target"`
	AllTables bool            `json:"allTables"`
	Tables    []string        `json:"tables"`
	AllViews  bool            `json:"allViews"`
	Views     []string        `json:"views"`
	Options   TransferOptions `json:"options"`
}

// DataTransfer starts a transfer job.
func (m *Manager) DataTransfer(req DataTransferRequest) (Job, error) {
	src, err := m.conn(req.Source.ConnID)
	if err != nil {
		return Job{}, fmt.Errorf("source: %w", err)
	}
	dst, err := m.conn(req.Target.ConnID)
	if err != nil {
		return Job{}, fmt.Errorf("target: %w", err)
	}
	if req.Source == req.Target {
		return Job{}, errors.New("the source and the target are the same")
	}
	if dst.Dialect().HasSchemas && req.Target.Schema == "" {
		req.Target.Schema = dst.Dialect().DefaultSchema
	}
	if src.Dialect().HasSchemas && req.Source.Schema == "" {
		req.Source.Schema = src.Dialect().DefaultSchema
	}
	t := &transferRun{m: m, req: req, o: req.Options, src: src, dst: dst, from: src.Dialect(), to: dst.Dialect()}
	title := fmt.Sprintf("Data Transfer %s → %s", endpointName(m, req.Source), endpointName(m, req.Target))
	return m.startJob("datatransfer", title, "rows", t.run), nil
}

func endpointName(m *Manager, e Endpoint) string {
	name := e.ConnID
	if p, err := m.Store.Get(e.ConnID); err == nil {
		name = p.Name
	}
	parts := []string{name}
	if e.Database != "" {
		parts = append(parts, e.Database)
	}
	if e.Schema != "" {
		parts = append(parts, e.Schema)
	}
	return strings.Join(parts, ".")
}

type transferRun struct {
	m        *Manager
	req      DataTransferRequest
	o        TransferOptions
	src, dst driver.Conn
	from, to driver.Dialect
	p        *progress
	ds       driver.Session // target
	ss       driver.Session // source
	seq      int            // running object number for the log
	rows     int64
	failed   int
	// index names taken in the target schema (PostgreSQL names them per schema)
	indexNames map[string]bool
}

// log writes a Navicat-style line: "[DTF] 3> orders: Create table".
func (t *transferRun) log(obj, format string, args ...any) {
	if obj == "" {
		t.p.logf("[DTF] "+format, args...)
		return
	}
	t.p.logf("[DTF] %d> %s: %s", t.seq, obj, fmt.Sprintf(format, args...))
}

// fail records an error. It returns the error when the transfer must stop.
func (t *transferRun) fail(obj string, err error) error {
	t.failed++
	t.p.error("[DTF] %d> %s: %v", t.seq, obj, err)
	if t.o.ContinueOnError {
		return nil
	}
	return fmt.Errorf("%s: %w", obj, err)
}

func (t *transferRun) run(ctx context.Context, p *progress) (string, error) {
	t.p = p
	start := time.Now()
	t.log("", "Data Transfer start: %s → %s", endpointName(t.m, t.req.Source), endpointName(t.m, t.req.Target))
	if t.o.CreateTargetDB {
		if err := t.createTargetDatabase(ctx); err != nil {
			return "", err
		}
	}
	var err error
	if t.ss, err = t.src.NewSession(ctx, t.req.Source.Database, t.req.Source.Schema); err != nil {
		return "", fmt.Errorf("source: %w", err)
	}
	defer t.ss.Close()
	if t.ds, err = t.dst.NewSession(ctx, t.req.Target.Database, t.req.Target.Schema); err != nil {
		return "", fmt.Errorf("target: %w", err)
	}
	defer t.ds.Close()

	tables, views, err := t.objects(ctx)
	if err != nil {
		return "", err
	}
	p.set(0, 0)
	t.log("", "%d table(s), %d view(s)", len(tables), len(views))
	t.foreignKeysOff(ctx)
	defer t.foreignKeysOn(context.Background())

	// Read every table's design first: foreign keys decide the order.
	designs := make([]design.Table, 0, len(tables))
	for _, name := range tables {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		t.seq++
		t.log(name, "Getting table structure")
		td, err := t.src.LoadDesign(ctx, model.ObjectRef{Database: t.req.Source.Database, Schema: t.req.Source.Schema, Name: name, Kind: model.KindTable})
		if err != nil {
			if err := t.fail(name, err); err != nil {
				return "", err
			}
			continue
		}
		designs = append(designs, td)
	}
	designs = dependencyOrder(designs)
	if err := t.prepareIndexNames(ctx); err != nil {
		return "", err
	}

	type created struct {
		src, dst design.Table // dst without foreign keys and triggers yet
		full     design.Table
	}
	var done []created
	if t.o.CreateTables && t.o.DropTarget {
		// Children before parents, so foreign keys between them don't block the drops.
		for i := len(designs) - 1; i >= 0; i-- {
			name := t.targetName(designs[i].Name)
			if _, err := t.exec(ctx, t.dropTable(name)); err != nil {
				if err := t.fail(name, fmt.Errorf("drop table: %w", err)); err != nil {
					return "", err
				}
			}
		}
	}
	t.seq = 0
	for _, td := range designs {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		t.seq++
		full := t.targetDesign(td)
		dst := full
		if t.o.CreateTables {
			var ok bool
			if dst, ok, err = t.createTable(ctx, full); err != nil {
				return "", err
			} else if !ok {
				continue
			}
		}
		if t.o.CreateRecords {
			if err := t.copyRows(ctx, td, full); err != nil {
				if errors.Is(err, context.Canceled) {
					return "", err
				}
				if err := t.fail(full.Name, err); err != nil {
					return "", err
				}
			}
		}
		done = append(done, created{src: td, dst: dst, full: full})
	}

	// Foreign keys and triggers once every table has its rows.
	if t.o.CreateTables {
		for i, c := range done {
			t.seq = i + 1
			if err := t.finishTable(ctx, c.dst, c.full); err != nil {
				return "", err
			}
		}
	}
	if err := t.copyViews(ctx, views); err != nil {
		return "", err
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	summary := fmt.Sprintf("%d table(s), %d view(s), %d record(s) in %s", len(done), len(views), t.rows, elapsed)
	if t.failed > 0 {
		t.log("", "Finished with error: %d error(s)", t.failed)
		return fmt.Sprintf("%s, %d error(s)", summary, t.failed), nil
	}
	t.log("", "Finished successfully")
	return summary, nil
}

// ---- objects ---------------------------------------------------------------------------------

func (t *transferRun) objects(ctx context.Context) (tables, views []string, err error) {
	list := func(kind model.ObjectKind) ([]string, error) {
		objs, err := t.src.ListObjects(ctx, t.req.Source.Database, t.req.Source.Schema, kind)
		if err != nil {
			return nil, err
		}
		out := make([]string, len(objs))
		for i, o := range objs {
			out[i] = o.Name
		}
		return out, nil
	}
	tables = t.req.Tables
	if t.req.AllTables {
		if tables, err = list(model.KindTable); err != nil {
			return nil, nil, err
		}
	}
	views = t.req.Views
	if t.req.AllViews {
		if views, err = list(model.KindView); err != nil {
			return nil, nil, err
		}
	}
	return tables, views, nil
}

// dependencyOrder puts referenced tables before the tables that reference them.
func dependencyOrder(ts []design.Table) []design.Table {
	byName := map[string]design.Table{}
	for _, t := range ts {
		byName[t.Name] = t
	}
	var out []design.Table
	state := map[string]int{} // 1 visiting, 2 done
	var visit func(t design.Table)
	visit = func(t design.Table) {
		if state[t.Name] != 0 {
			return // done, or a cycle: foreign keys come last anyway
		}
		state[t.Name] = 1
		for _, f := range t.ForeignKeys {
			if p, ok := byName[f.RefTable]; ok && p.Name != t.Name {
				visit(p)
			}
		}
		state[t.Name] = 2
		out = append(out, t)
	}
	for _, t := range ts {
		visit(t)
	}
	return out
}

// ---- names and placement ---------------------------------------------------------------------

func (t *transferRun) convertName(s string) string {
	switch t.o.ConvertNames {
	case "lower":
		return strings.ToLower(s)
	case "upper":
		return strings.ToUpper(s)
	}
	return s
}

func (t *transferRun) targetName(s string) string { return t.convertName(s) }

func (t *transferRun) targetRef(name string, kind model.ObjectKind) model.ObjectRef {
	return model.ObjectRef{Database: t.req.Target.Database, Schema: t.req.Target.Schema, Name: name, Kind: kind}
}

// targetDesign converts a source table for the target and applies the options.
func (t *transferRun) targetDesign(src design.Table) design.Table {
	td, notes := design.Convert(src, t.from.Name, t.to.Name)
	for _, n := range notes {
		t.log(src.Name, "%s", strings.TrimPrefix(n, src.Name+": "))
	}
	td.Database, td.Schema = "", ""
	if t.to.Name == "mysql" {
		td.Database = t.req.Target.Database
	}
	if t.to.HasSchemas {
		td.Schema = t.req.Target.Schema
	}
	if t.to.Name == "sqlite" && t.req.Target.Database != "main" {
		td.Database = t.req.Target.Database
	}
	cn := t.convertName
	td.Name = cn(td.Name)
	for i := range td.Columns {
		td.Columns[i].Name = cn(td.Columns[i].Name)
	}
	if td.PrimaryKey != nil {
		pk := *td.PrimaryKey
		pk.Columns = mapStrings(pk.Columns, cn)
		td.PrimaryKey = &pk
	}
	for i := range td.Indexes {
		ix := &td.Indexes[i]
		ix.Name = cn(ix.Name)
		cols := make([]design.IndexColumn, len(ix.Columns))
		for j, c := range ix.Columns {
			c.Name = cn(c.Name)
			cols[j] = c
		}
		ix.Columns = cols
	}
	for i := range td.Uniques {
		td.Uniques[i].Name = cn(td.Uniques[i].Name)
		td.Uniques[i].Columns = mapStrings(td.Uniques[i].Columns, cn)
	}
	for i := range td.ForeignKeys {
		f := &td.ForeignKeys[i]
		f.Name = cn(f.Name)
		f.Columns = mapStrings(f.Columns, cn)
		f.RefColumns = mapStrings(f.RefColumns, cn)
		f.RefTable = cn(f.RefTable)
		// A reference into the source database/schema points at the target's.
		if f.RefDatabase == t.req.Source.Database || t.to.Name != "mysql" {
			f.RefDatabase = ""
		}
		if f.RefSchema == t.req.Source.Schema || !t.to.HasSchemas {
			f.RefSchema = ""
		}
		if t.to.HasSchemas && f.RefSchema == "" {
			f.RefSchema = t.req.Target.Schema
		}
	}

	o := t.o
	if !o.IncludeIndexes {
		td.Indexes = nil
	}
	if !o.IncludeUniques {
		td.Uniques = nil
		td.Indexes = slices.DeleteFunc(td.Indexes, func(ix design.Index) bool { return ix.Kind == "unique" })
	}
	if !o.IncludeChecks {
		td.Checks = nil
	}
	if !o.IncludeForeignKey {
		td.ForeignKeys = nil
	}
	if !o.IncludeTriggers {
		td.Triggers = nil
	}
	if !o.IncludeAutoInc {
		for i := range td.Columns {
			td.Columns[i].AutoIncrement, td.Columns[i].Identity, td.Columns[i].IdentityOpts = false, "", nil
		}
		td.Options.AutoIncrement = ""
	}
	if !o.IncludeCharset {
		for i := range td.Columns {
			td.Columns[i].Charset, td.Columns[i].Collation = "", ""
		}
		td.Options.Charset, td.Options.Collation = "", ""
	}
	if !o.IncludeEngine {
		td.Options.Engine = ""
	}
	if !o.IncludeOther {
		eng, cs, coll, ai := td.Options.Engine, td.Options.Charset, td.Options.Collation, td.Options.AutoIncrement
		td.Options = design.Options{Engine: eng, Charset: cs, Collation: coll, AutoIncrement: ai}
	}
	// Index names are per schema in PostgreSQL; two source tables may share one.
	if t.to.Name == "postgres" {
		for i := range td.Indexes {
			name := td.Indexes[i].Name
			for n := 2; t.indexNames[name]; n++ {
				name = fmt.Sprintf("%s_%s", td.Name, td.Indexes[i].Name)
				if n > 2 {
					name = fmt.Sprintf("%s_%s_%d", td.Name, td.Indexes[i].Name, n)
				}
			}
			t.indexNames[name] = true
			td.Indexes[i].Name = name
		}
	}
	return td
}

func mapStrings(xs []string, f func(string) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = f(x)
	}
	return out
}

// prepareIndexNames records the index names already in a PostgreSQL target schema
// (of tables that aren't about to be dropped).
func (t *transferRun) prepareIndexNames(ctx context.Context) error {
	t.indexNames = map[string]bool{}
	if t.to.Name != "postgres" {
		return nil
	}
	rows, err := t.ds.Query(ctx, "SELECT indexname, tablename FROM pg_indexes WHERE schemaname = "+pgLiteral(t.req.Target.Schema))
	if err != nil {
		return err
	}
	defer rows.Close()
	cell := make([]*string, 2)
	for rows.Next(cell) {
		t.indexNames[*cell[0]] = true
	}
	return rows.Err()
}

func pgLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// ---- target DDL ------------------------------------------------------------------------------

func (t *transferRun) exec(ctx context.Context, sql string) (int64, error) {
	return t.ds.Exec(ctx, sql)
}

func (t *transferRun) dropTable(name string) string {
	q := "DROP TABLE IF EXISTS " + t.to.Qualified(t.targetRef(name, model.KindTable))
	if t.to.Name == "postgres" && t.o.DropCascade {
		q += " CASCADE"
	}
	return q
}

// foreignKeysOff lets rows load in any order and tables drop while referenced.
func (t *transferRun) foreignKeysOff(ctx context.Context) {
	switch t.to.Name {
	case "mysql":
		_, _ = t.ds.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 0")
	case "sqlite":
		_, _ = t.ds.Exec(ctx, "PRAGMA foreign_keys = OFF")
	}
}

func (t *transferRun) foreignKeysOn(ctx context.Context) {
	switch t.to.Name {
	case "mysql":
		_, _ = t.ds.Exec(ctx, "SET FOREIGN_KEY_CHECKS = 1")
	case "sqlite":
		_, _ = t.ds.Exec(ctx, "PRAGMA foreign_keys = ON")
	}
}

// createTable creates the table without its foreign keys and triggers (SQLite
// keeps its foreign keys: it can't add them later). It returns what was created and
// whether it was; the error is set when the transfer must stop.
func (t *transferRun) createTable(ctx context.Context, full design.Table) (design.Table, bool, error) {
	base := full
	base.Triggers = nil
	if t.to.Name != "sqlite" {
		base.ForeignKeys = nil
	}
	t.log(full.Name, "Create table")
	stmts, err := t.dst.DesignGenerator().Create(base)
	if err == nil {
		for _, s := range stmts {
			if _, err = t.exec(ctx, s); err != nil {
				err = fmt.Errorf("%w\n%s", err, s)
				break
			}
		}
	}
	if err != nil {
		return design.Table{}, false, t.fail(full.Name, fmt.Errorf("create table: %w", err))
	}
	return base, true, nil
}

// finishTable adds foreign keys and triggers, and moves sequences past the copied ids.
func (t *transferRun) finishTable(ctx context.Context, created, full design.Table) error {
	if t.o.CreateRecords {
		t.resetSequences(ctx, full)
	}
	if t.to.Name == "sqlite" {
		created.ForeignKeys = full.ForeignKeys
	}
	if len(full.ForeignKeys) == len(created.ForeignKeys) && len(full.Triggers) == 0 {
		return nil
	}
	old := created
	old.OrigName = created.Name
	for i := range old.Columns {
		old.Columns[i].OrigName = old.Columns[i].Name
	}
	want := full
	want.OrigName = created.Name
	want.Columns = old.Columns
	for i := range want.Indexes {
		want.Indexes[i].OrigName = want.Indexes[i].Name
	}
	old.Indexes = want.Indexes
	for i := range want.Uniques {
		want.Uniques[i].OrigName = want.Uniques[i].Name
	}
	old.Uniques = want.Uniques
	for i := range want.Checks {
		want.Checks[i].OrigName = want.Checks[i].Name
	}
	old.Checks = want.Checks
	if len(full.ForeignKeys) > 0 && t.to.Name != "sqlite" {
		t.log(full.Name, "Create foreign keys")
	}
	if len(full.Triggers) > 0 {
		t.log(full.Name, "Create triggers")
	}
	stmts, err := t.dst.DesignGenerator().Alter(old, want)
	if err == nil {
		for _, s := range stmts {
			if _, err = t.exec(ctx, s); err != nil {
				err = fmt.Errorf("%w\n%s", err, s)
				break
			}
		}
	}
	if err != nil {
		return t.fail(full.Name, err)
	}
	return nil
}

// resetSequences moves PostgreSQL identity sequences past the copied ids.
func (t *transferRun) resetSequences(ctx context.Context, td design.Table) {
	if t.to.Name != "postgres" {
		return
	}
	tbl := t.to.Qualified(t.targetRef(td.Name, model.KindTable))
	for _, c := range td.Columns {
		if c.Identity == "" {
			continue
		}
		col := t.to.QuoteIdent(c.Name)
		q := fmt.Sprintf("SELECT setval(pg_get_serial_sequence(%s, %s), COALESCE((SELECT max(%s) FROM %s), 1), (SELECT max(%s) FROM %s) IS NOT NULL)",
			pgLiteral(tbl), pgLiteral(c.Name), col, tbl, col, tbl)
		if rows, err := t.ds.Query(ctx, q); err == nil {
			rows.Close()
		}
	}
}

func (t *transferRun) createTargetDatabase(ctx context.Context) error {
	e := t.req.Target
	switch t.to.Name {
	case "mysql":
		s, err := t.dst.NewSession(ctx, "", "")
		if err != nil {
			return err
		}
		defer s.Close()
		_, err = s.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+t.to.QuoteIdent(e.Database))
		return err
	case "postgres":
		dbs, err := t.dst.ListDatabases(ctx)
		if err != nil {
			return err
		}
		if !slices.Contains(dbs, e.Database) {
			s, err := t.dst.NewSession(ctx, "", "")
			if err != nil {
				return err
			}
			_, err = s.Exec(ctx, "CREATE DATABASE "+t.to.QuoteIdent(e.Database))
			s.Close()
			if err != nil {
				return err
			}
			t.log("", "Create database %s", e.Database)
		}
		s, err := t.dst.NewSession(ctx, e.Database, "")
		if err != nil {
			return err
		}
		defer s.Close()
		_, err = s.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+t.to.QuoteIdent(e.Schema))
		return err
	}
	return nil
}

// ---- rows ------------------------------------------------------------------------------------

// maxParams keeps a multi-row INSERT under every engine's placeholder limit.
const maxParams = 30000

func (t *transferRun) copyRows(ctx context.Context, src, dst design.Table) error {
	var srcCols, dstCols []string
	var dstDesign []design.Column
	for i, c := range src.Columns {
		if strings.TrimSpace(c.GeneratedExpr) != "" && t.from.Name == t.to.Name {
			continue // the target computes it
		}
		srcCols = append(srcCols, t.from.QuoteIdent(c.Name))
		dstCols = append(dstCols, t.to.QuoteIdent(dst.Columns[i].Name))
		dstDesign = append(dstDesign, dst.Columns[i])
	}
	if len(srcCols) == 0 {
		return nil
	}
	table := t.to.Qualified(t.targetRef(dst.Name, model.KindTable))
	if t.o.EmptyTarget && !(t.o.CreateTables && t.o.DropTarget) {
		t.log(dst.Name, "Empty table")
		if _, err := t.exec(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("empty table: %w", err)
		}
	}
	srcRef := model.ObjectRef{Database: t.req.Source.Database, Schema: t.req.Source.Schema, Name: src.Name, Kind: model.KindTable}
	rows, err := t.ss.Query(ctx, "SELECT "+strings.Join(srcCols, ", ")+" FROM "+t.from.Qualified(srcRef))
	if err != nil {
		return fmt.Errorf("read rows: %w", err)
	}
	defer rows.Close()
	srcTypes := make([]string, len(srcCols))
	for i, c := range rows.Columns() {
		if i < len(srcTypes) {
			srcTypes[i] = c.Type
		}
	}

	t.log(dst.Name, "Transfer records")
	if t.o.UseTransaction {
		if _, err := t.exec(ctx, t.beginSQL()); err != nil {
			return err
		}
	}
	commit := func(ok bool) error {
		if !t.o.UseTransaction {
			return nil
		}
		if ok {
			_, err := t.exec(ctx, "COMMIT")
			return err
		}
		_, _ = t.exec(context.Background(), "ROLLBACK")
		return nil
	}

	perStmt := 1
	if t.o.ExtendedInserts {
		perStmt = min(500, max(1, maxParams/len(dstCols)))
	}
	overriding := t.to.Name == "postgres" && slices.ContainsFunc(dstDesign, func(c design.Column) bool { return c.Identity == "always" })
	head := t.insertHead(table, dstCols, overriding)
	batch := make([][]any, 0, perStmt)
	var copied int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		q, args := t.insertSQL(head, dstCols, batch)
		if _, err := t.ds.Exec(ctx, q, args...); err != nil {
			return err
		}
		copied += int64(len(batch))
		t.rows += int64(len(batch))
		t.p.add(int64(len(batch)))
		batch = batch[:0]
		return nil
	}
	cell := make([]*string, len(srcCols))
	for rows.Next(cell) {
		if ctx.Err() != nil {
			_ = commit(false)
			return ctx.Err()
		}
		vals := make([]any, len(cell))
		for i, v := range cell {
			vals[i] = convertValue(v, srcTypes[i], dstDesign[i], t.to.Name)
		}
		batch = append(batch, vals)
		if len(batch) == perStmt {
			if err := flush(); err != nil {
				_ = commit(false)
				return fmt.Errorf("insert: %w", err)
			}
		}
	}
	if err := rows.Err(); err != nil {
		_ = commit(false)
		return fmt.Errorf("read rows: %w", err)
	}
	if err := flush(); err != nil {
		_ = commit(false)
		return fmt.Errorf("insert: %w", err)
	}
	if err := commit(true); err != nil {
		return err
	}
	t.log(dst.Name, "%d record(s) transferred", copied)
	return nil
}

func (t *transferRun) beginSQL() string {
	if t.to.Name == "mysql" {
		return "START TRANSACTION"
	}
	return "BEGIN"
}

func (t *transferRun) insertHead(table string, cols []string, overriding bool) string {
	verb := "INSERT INTO "
	switch {
	case t.o.InsertMode == "ignore" && t.to.Name == "mysql":
		verb = "INSERT IGNORE INTO "
	case t.o.InsertMode == "ignore" && t.to.Name == "sqlite":
		verb = "INSERT OR IGNORE INTO "
	case t.o.InsertMode == "replace" && t.to.Name == "mysql":
		verb = "REPLACE INTO "
	case t.o.InsertMode == "replace" && t.to.Name == "sqlite":
		verb = "INSERT OR REPLACE INTO "
	}
	head := verb + table + " (" + strings.Join(cols, ", ") + ") "
	if overriding {
		// GENERATED ALWAYS identity columns only take the copied ids this way.
		head += "OVERRIDING SYSTEM VALUE "
	}
	return head + "VALUES "
}

func (t *transferRun) insertSQL(head string, cols []string, batch [][]any) (string, []any) {
	var b strings.Builder
	b.WriteString(head)
	args := make([]any, 0, len(batch)*len(cols))
	for i, r := range batch {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('(')
		for j, v := range r {
			if j > 0 {
				b.WriteString(", ")
			}
			args = append(args, v)
			b.WriteString(t.to.Placeholder(len(args)))
		}
		b.WriteByte(')')
	}
	if t.o.InsertMode == "ignore" && t.to.Name == "postgres" {
		b.WriteString(" ON CONFLICT DO NOTHING")
	}
	return b.String(), args
}

var zeroDate = regexp.MustCompile(`^0000-00-00`)

// convertValue turns a display-formatted source cell into a parameter for the target column.
func convertValue(v *string, srcType string, dst design.Column, to string) any {
	if v == nil {
		return nil
	}
	s := *v
	typ := design.CanonType(dst.Type)
	binarySrc := sqlcommon.IsBinaryType(srcType) || srcType == "" || srcType == "BLOB"
	switch {
	case isBinaryColumn(typ):
		if binarySrc || strings.HasPrefix(s, `\x`) {
			if b, ok := decodeHex(s); ok {
				return b
			}
		}
		return []byte(s)
	case isBoolColumn(typ, dst, to):
		if b, ok := parseBool(s); ok {
			if to == "postgres" {
				return b
			}
			if b {
				return int64(1)
			}
			return int64(0)
		}
	case isIntColumn(typ) && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, `\x`)):
		// MySQL BIT(n) values arrive as hex.
		if b, ok := decodeHex(s); ok && len(b) <= 8 {
			var n uint64
			for _, x := range b {
				n = n<<8 | uint64(x)
			}
			return strconv.FormatUint(n, 10)
		}
	case typ == "date" || typ == "datetime" || typ == "timestamp" || typ == "timestamptz":
		if zeroDate.MatchString(s) {
			return nil // MySQL's zero date has no equivalent
		}
		if to == "mysql" && srcType == "TIMESTAMPTZ" {
			if tm, err := time.Parse("2006-01-02 15:04:05.999999-07", s); err == nil {
				return tm.UTC().Format("2006-01-02 15:04:05.999999")
			}
		}
	}
	return s
}

func decodeHex(s string) ([]byte, bool) {
	switch {
	case strings.HasPrefix(s, "0x"), strings.HasPrefix(s, `\x`):
		b, err := hex.DecodeString(s[2:])
		return b, err == nil
	}
	return nil, false
}

func parseBool(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "1", "t", "true", "y", "yes", "on", "0x01":
		return true, true
	case "0", "f", "false", "n", "no", "off", "0x00":
		return false, true
	}
	return false, false
}

func isBinaryColumn(t string) bool {
	switch t {
	case "bytea", "blob", "tinyblob", "mediumblob", "longblob", "binary", "varbinary":
		return true
	}
	return false
}

func isBoolColumn(t string, c design.Column, to string) bool {
	return t == "boolean" || (to == "mysql" && t == "tinyint" && c.Length == "1")
}

func isIntColumn(t string) bool {
	switch t {
	case "smallint", "integer", "bigint", "tinyint", "mediumint":
		return true
	}
	return false
}

// ---- views -----------------------------------------------------------------------------------

var viewDefinerRe = regexp.MustCompile(`(?i)\s+DEFINER\s*=\s*(` + "`[^`]*`" + `|'[^']*'|\S+)@(` + "`[^`]*`" + `|'[^']*'|\S+)`)

// copyViews recreates views from their DDL. Only views between the same engine
// can work; between engines they are skipped. Views that fail because another view
// isn't there yet are retried once the others exist.
func (t *transferRun) copyViews(ctx context.Context, views []string) error {
	if len(views) == 0 {
		return nil
	}
	if t.from.Name != t.to.Name {
		for _, v := range views {
			t.seq++
			t.log(v, "Skipped: views are only copied between databases of the same engine")
		}
		return nil
	}
	type pending struct {
		name, ddl string
		err       error
	}
	var todo []pending
	for _, v := range views {
		t.seq++
		ddl, err := t.src.DDL(ctx, model.ObjectRef{Database: t.req.Source.Database, Schema: t.req.Source.Schema, Name: v, Kind: model.KindView})
		if err != nil {
			if err := t.fail(v, err); err != nil {
				return err
			}
			continue
		}
		todo = append(todo, pending{name: v, ddl: t.retargetView(v, ddl)})
	}
	for len(todo) > 0 {
		var next []pending
		for _, v := range todo {
			name := t.targetName(v.name)
			if t.o.DropTarget {
				_, _ = t.exec(ctx, "DROP VIEW IF EXISTS "+t.to.Qualified(t.targetRef(name, model.KindView)))
			}
			t.log(name, "Create view")
			if _, err := t.exec(ctx, v.ddl); err != nil {
				v.err = err
				next = append(next, v)
			}
		}
		if len(next) == len(todo) {
			for _, v := range next {
				if err := t.fail(v.name, fmt.Errorf("create view: %w", v.err)); err != nil {
					return err
				}
			}
			break
		}
		todo = next
	}
	return nil
}

// retargetView rewrites a view's DDL to create it in the target database/schema.
func (t *transferRun) retargetView(name, ddl string) string {
	ddl = viewDefinerRe.ReplaceAllString(ddl, "")
	q := t.from.QuoteIdent
	src, dst := t.req.Source.Database, t.req.Target.Database
	if t.from.HasSchemas {
		src, dst = t.req.Source.Schema, t.req.Target.Schema
	}
	if src != "" && dst != "" && src != dst {
		ddl = strings.ReplaceAll(ddl, q(src)+".", t.to.QuoteIdent(dst)+".")
		if t.from.HasSchemas {
			ddl = regexp.MustCompile(`\b`+regexp.QuoteMeta(src)+`\.`).ReplaceAllString(ddl, t.to.QuoteIdent(dst)+".")
		}
	}
	if t.o.ConvertNames != "" {
		ddl = strings.Replace(ddl, q(name), t.to.QuoteIdent(t.targetName(name)), 1)
	}
	// A view created in another database (MySQL, SQLite) needs its name qualified.
	if t.to.Name != "postgres" && !strings.Contains(ddl, q(dst)+"."+q(t.targetName(name))) && dst != "" && dst != "main" {
		ddl = strings.Replace(ddl, q(t.targetName(name)), q(dst)+"."+q(t.targetName(name)), 1)
	}
	return ddl
}
