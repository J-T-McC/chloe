package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	"chloe/internal/datamodel"
	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
)

// LoadDesign reads a table definition for the designer.
func (m *Manager) LoadDesign(ctx context.Context, connID string, ref model.ObjectRef) (design.Table, error) {
	c, err := m.conn(connID)
	if err != nil {
		return design.Table{}, err
	}
	return c.LoadDesign(ctx, ref)
}

func (m *Manager) DesignMeta(ctx context.Context, connID, database string) (design.Meta, error) {
	c, err := m.conn(connID)
	if err != nil {
		return design.Meta{}, err
	}
	return c.DesignMeta(ctx, database)
}

// DesignPreview returns the statements that turn old into new (old nil = create).
type DesignPreview struct {
	Statements []string `json:"statements"`
	// Warnings the user should see before saving (e.g. unsupported column reordering).
	Warnings []string `json:"warnings,omitempty"`
	// Transactional reports whether the statements run atomically.
	Transactional bool `json:"transactional"`
}

func (m *Manager) PreviewDesign(connID string, old *design.Table, nw design.Table) (DesignPreview, error) {
	c, err := m.conn(connID)
	if err != nil {
		return DesignPreview{}, err
	}
	gen := c.DesignGenerator()
	p := DesignPreview{Transactional: c.Dialect().TransactionalDDL()}
	if old == nil {
		p.Statements, err = gen.Create(nw)
		return p, err
	}
	p.Statements, err = gen.Alter(*old, nw)
	if c.Dialect().Name == "postgres" && design.ColumnOrderChanged(*old, nw) {
		p.Warnings = append(p.Warnings, "PostgreSQL can't reorder existing columns; the new order is ignored.")
	}
	if !p.Transactional && len(p.Statements) > 1 {
		p.Warnings = append(p.Warnings, "MySQL DDL isn't transactional: if a statement fails, the ones before it stay applied.")
	}
	return p, err
}

// ApplyResult reports how far a design save got.
type ApplyResult struct {
	Executed int    `json:"executed"`
	Error    string `json:"error,omitempty"`
	// FailedStatement is the statement that failed, for showing next to the error.
	FailedStatement string `json:"failedStatement,omitempty"`
	// Table is the definition re-read from the server after a successful save.
	Table *design.Table `json:"table,omitempty"`
	// View is the re-read view after a view designer save.
	View *design.View `json:"view,omitempty"`
}

// ApplyDesign generates and runs the statements for a design change, then reloads
// the table so the designer continues from the server's actual state.
func (m *Manager) ApplyDesign(ctx context.Context, connID string, old *design.Table, nw design.Table) (ApplyResult, error) {
	p, err := m.PreviewDesign(connID, old, nw)
	if err != nil {
		return ApplyResult{}, err
	}
	res := m.ExecuteDDL(ctx, connID, nw.Database, nw.Schema, p.Statements, p.Transactional)
	if res.Error != "" {
		return res, nil
	}
	c, _ := m.conn(connID)
	t, err := c.LoadDesign(ctx, model.ObjectRef{Database: nw.Database, Schema: nw.Schema, Name: nw.Name, Kind: model.KindTable})
	if err == nil {
		res.Table = &t
	}
	return res, nil
}

// ExecuteDDL runs statements on a fresh session, inside a transaction when asked.
func (m *Manager) ExecuteDDL(ctx context.Context, connID, database, schema string, stmts []string, transactional bool) ApplyResult {
	var res ApplyResult
	c, err := m.conn(connID)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	ds, err := c.NewSession(ctx, database, schema)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer ds.Close()
	// A SQLite table rebuild drops the old table: foreign keys must not cascade
	// or block it, and views naming the table must survive the rename. The
	// session is discarded afterwards, so the pragmas don't leak.
	rebuild := c.Dialect().Name == "sqlite" && slices.ContainsFunc(stmts, func(s string) bool { return strings.Contains(s, design.RebuildPrefix) })
	var fkBefore map[string]int
	if rebuild {
		for _, p := range []string{"PRAGMA foreign_keys = OFF", "PRAGMA legacy_alter_table = ON"} {
			if _, err := ds.Exec(ctx, p); err != nil {
				res.Error = err.Error()
				return res
			}
		}
		if fkBefore, err = fkViolations(ctx, ds, database); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	if transactional {
		if _, err := ds.Exec(ctx, "BEGIN"); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	for _, st := range stmts {
		if _, err := ds.Exec(ctx, st); err != nil {
			res.Error, res.FailedStatement = err.Error(), st
			if transactional {
				_, _ = ds.Exec(ctx, "ROLLBACK")
				res.Executed = 0
			}
			return res
		}
		res.Executed++
	}
	if rebuild {
		after, err := fkViolations(ctx, ds, database)
		if err == nil {
			for table, n := range after {
				if n > fkBefore[table] {
					err = fmt.Errorf("the change breaks foreign keys: %d row(s) of %s no longer match their parent", n-fkBefore[table], table)
					break
				}
			}
		}
		if err != nil {
			_, _ = ds.Exec(ctx, "ROLLBACK")
			res.Error, res.Executed = err.Error(), 0
			return res
		}
	}
	if transactional {
		if _, err := ds.Exec(ctx, "COMMIT"); err != nil {
			res.Error = err.Error()
			res.Executed = 0
		}
	}
	return res
}

// fkViolations counts rows breaking foreign keys, per child table (SQLite).
func fkViolations(ctx context.Context, ds driver.Session, database string) (map[string]int, error) {
	rows, err := ds.Query(ctx, "PRAGMA "+sqliteIdent(database)+".foreign_key_check")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	cell := make([]*string, len(rows.Columns()))
	for rows.Next(cell) {
		out[str(cell[0])]++
	}
	return out, rows.Err()
}

func sqliteIdent(s string) string {
	if s == "" {
		s = "main"
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// TableAction is a one-click table operation from the tree or object list.
type TableAction string

const (
	ActionDrop      TableAction = "drop"
	ActionTruncate  TableAction = "truncate"
	ActionEmpty     TableAction = "empty" // DELETE FROM: fires triggers, keeps auto-increment
	ActionRename    TableAction = "rename"
	ActionDuplicate TableAction = "duplicate" // structure only
	ActionCopy      TableAction = "copy"      // structure and data
)

// TableActionSQL builds the SQL for a table operation, so the UI can show it first.
func (m *Manager) TableActionSQL(connID string, ref model.ObjectRef, action TableAction, newName string) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	d := c.Dialect()
	pg := d.Name == "postgres"
	tbl := d.Qualified(ref)
	target := d.Qualified(model.ObjectRef{Database: ref.Database, Schema: ref.Schema, Name: newName})
	kind := "TABLE"
	if ref.Kind == model.KindView {
		kind = "VIEW"
	} else if ref.Kind == model.KindMatView {
		kind = "MATERIALIZED VIEW"
	}
	if (action == ActionRename || action == ActionDuplicate || action == ActionCopy) && strings.TrimSpace(newName) == "" {
		return nil, fmt.Errorf("a new name is required")
	}
	if d.Name == "sqlite" {
		return sqliteTableAction(c, ref, action, newName)
	}
	switch action {
	case ActionDrop:
		return []string{"DROP " + kind + " " + tbl}, nil
	case ActionTruncate:
		return []string{"TRUNCATE TABLE " + tbl}, nil
	case ActionEmpty:
		return []string{"DELETE FROM " + tbl}, nil
	case ActionRename:
		if pg {
			return []string{fmt.Sprintf("ALTER %s %s RENAME TO %s", kind, tbl, d.QuoteIdent(newName))}, nil
		}
		return []string{fmt.Sprintf("RENAME TABLE %s TO %s", tbl, target)}, nil
	case ActionDuplicate, ActionCopy:
		var out []string
		if pg {
			out = append(out, fmt.Sprintf("CREATE TABLE %s (LIKE %s INCLUDING ALL)", target, tbl))
		} else {
			out = append(out, fmt.Sprintf("CREATE TABLE %s LIKE %s", target, tbl))
		}
		if action == ActionCopy {
			out = append(out, fmt.Sprintf("INSERT INTO %s SELECT * FROM %s", target, tbl))
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown table action %q", action)
}

// sqliteTableAction: SQLite has no TRUNCATE or CREATE TABLE … LIKE.
func sqliteTableAction(c driver.Conn, ref model.ObjectRef, action TableAction, newName string) ([]string, error) {
	d := c.Dialect()
	tbl := d.Qualified(ref)
	switch action {
	case ActionDrop:
		kind := map[model.ObjectKind]string{model.KindView: "VIEW", model.KindIndex: "INDEX", model.KindTrigger: "TRIGGER"}[ref.Kind]
		if kind == "" {
			kind = "TABLE"
		}
		return []string{"DROP " + kind + " " + tbl}, nil
	case ActionTruncate, ActionEmpty:
		return []string{"DELETE FROM " + tbl}, nil
	case ActionRename:
		return []string{"ALTER TABLE " + tbl + " RENAME TO " + d.QuoteIdent(newName)}, nil
	case ActionDuplicate, ActionCopy:
		t, err := c.LoadDesign(context.Background(), ref)
		if err != nil {
			return nil, err
		}
		// Index and trigger names are unique per database, so the copy's get the new table name.
		rename := func(n string) string {
			if strings.Contains(n, ref.Name) {
				return strings.ReplaceAll(n, ref.Name, newName)
			}
			return newName + "_" + n
		}
		t.Name, t.OrigName = newName, ""
		for i := range t.Indexes {
			t.Indexes[i].Name = rename(t.Indexes[i].Name)
		}
		for i := range t.Triggers {
			t.Triggers[i].Name = rename(t.Triggers[i].Name)
		}
		out, err := c.DesignGenerator().Create(t)
		if err != nil {
			return nil, err
		}
		if action == ActionCopy {
			var cols []string
			for _, col := range t.Columns {
				if col.GeneratedExpr == "" {
					cols = append(cols, d.QuoteIdent(col.Name))
				}
			}
			list := strings.Join(cols, ", ")
			target := d.Qualified(model.ObjectRef{Database: ref.Database, Name: newName})
			// Rows go in before the triggers are created, so copying doesn't fire them.
			n := 1 + len(t.Indexes)
			out = slices.Insert(out, n, fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", target, list, list, tbl))
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown table action %q", action)
}

// RunTableAction executes a table operation.
func (m *Manager) RunTableAction(ctx context.Context, connID string, ref model.ObjectRef, action TableAction, newName string) ApplyResult {
	stmts, err := m.TableActionSQL(connID, ref, action, newName)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	c, _ := m.conn(connID)
	return m.ExecuteDDL(ctx, connID, ref.Database, ref.Schema, stmts, c.Dialect().TransactionalDDL())
}

// MaintainOp is a table maintenance operation (Navicat's Maintain menu).
type MaintainOp string

const (
	MaintainAnalyze      MaintainOp = "analyze"
	MaintainCheck        MaintainOp = "check"    // MySQL
	MaintainOptimize     MaintainOp = "optimize" // MySQL
	MaintainRepair       MaintainOp = "repair"   // MySQL
	MaintainVacuum       MaintainOp = "vacuum"   // PostgreSQL
	MaintainVacuumFull   MaintainOp = "vacuum_full"
	MaintainVacuumAnalyz MaintainOp = "vacuum_analyze"
	MaintainReindex      MaintainOp = "reindex" // PostgreSQL
)

// sqliteMaintain runs ANALYZE, REINDEX, VACUUM (whole database) or an integrity check.
func sqliteMaintain(ctx context.Context, ds driver.Session, refs []model.ObjectRef, op MaintainOp) ([]MaintainResult, error) {
	var out []MaintainResult
	for _, r := range refs {
		db, name := sqliteIdent(r.Database), sqliteIdent(r.Name)
		var stmt string
		switch op {
		case MaintainAnalyze:
			stmt = "ANALYZE " + db + "." + name
		case MaintainReindex:
			stmt = "REINDEX " + db + "." + name
		case MaintainCheck:
			stmt = "PRAGMA " + db + ".integrity_check(" + name + ")"
		case MaintainVacuum:
			stmt = "VACUUM " + db
		default:
			return nil, fmt.Errorf("%s isn't available for SQLite", op)
		}
		rows, err := ds.Query(ctx, stmt)
		if err != nil {
			out = append(out, MaintainResult{Table: r.Name, Status: "error", Message: err.Error()})
			continue
		}
		msg := "OK"
		cell := make([]*string, len(rows.Columns()))
		var lines []string
		for rows.Next(cell) {
			if len(cell) > 0 && str(cell[0]) != "ok" {
				lines = append(lines, str(cell[0]))
			}
		}
		rows.Close()
		status := "ok"
		if len(lines) > 0 {
			status, msg = "error", strings.Join(lines, "; ")
		}
		out = append(out, MaintainResult{Table: r.Name, Status: status, Message: msg})
		if op == MaintainVacuum {
			break // VACUUM covers the whole database
		}
	}
	return out, nil
}

// MaintainResult is one line of maintenance output.
type MaintainResult struct {
	Table   string `json:"table"`
	Status  string `json:"status"` // MySQL Msg_type, or "ok"/"error"
	Message string `json:"message"`
}

// Maintain runs a maintenance operation on tables and returns the server's report.
func (m *Manager) Maintain(ctx context.Context, connID string, refs []model.ObjectRef, op MaintainOp) ([]MaintainResult, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	d := c.Dialect()
	ds, err := c.NewSession(ctx, refs[0].Database, refs[0].Schema)
	if err != nil {
		return nil, err
	}
	defer ds.Close()
	var out []MaintainResult
	if d.Name == "sqlite" {
		return sqliteMaintain(ctx, ds, refs, op)
	}
	if d.Name != "postgres" {
		kw := map[MaintainOp]string{MaintainAnalyze: "ANALYZE", MaintainCheck: "CHECK", MaintainOptimize: "OPTIMIZE", MaintainRepair: "REPAIR"}[op]
		if kw == "" {
			return nil, fmt.Errorf("%s isn't available for MySQL", op)
		}
		names := make([]string, len(refs))
		for i, r := range refs {
			names[i] = d.Qualified(r)
		}
		rows, err := ds.Query(ctx, kw+" TABLE "+strings.Join(names, ", "))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cell := make([]*string, len(rows.Columns()))
		for rows.Next(cell) {
			// Table, Op, Msg_type, Msg_text
			out = append(out, MaintainResult{Table: str(cell[0]), Status: str(cell[2]), Message: str(cell[3])})
		}
		return out, rows.Err()
	}
	kw := map[MaintainOp]string{MaintainAnalyze: "ANALYZE", MaintainVacuum: "VACUUM", MaintainVacuumFull: "VACUUM FULL",
		MaintainVacuumAnalyz: "VACUUM ANALYZE", MaintainReindex: "REINDEX TABLE"}[op]
	if kw == "" {
		return nil, fmt.Errorf("%s isn't available for PostgreSQL", op)
	}
	for _, r := range refs {
		res := MaintainResult{Table: r.Name, Status: "ok", Message: "OK"}
		if _, err := ds.Exec(ctx, kw+" "+d.Qualified(r)); err != nil {
			res.Status, res.Message = "error", err.Error()
		}
		out = append(out, res)
	}
	return out, nil
}

func str(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ReverseModel builds a data model from tables in a database (all tables when names
// is empty).
func (m *Manager) ReverseModel(ctx context.Context, connID, database, schema string, names []string) (datamodel.Model, error) {
	c, err := m.conn(connID)
	if err != nil {
		return datamodel.Model{}, err
	}
	dialect := c.Dialect().Name
	mdl := datamodel.New(database, dialect)
	mdl.Link = &datamodel.Link{ConnID: connID, Database: database, Schema: schema}
	if schema != "" {
		mdl.Name = database + "." + schema
		mdl.Schema = schema
	}
	if len(names) == 0 {
		objs, err := c.ListObjects(ctx, database, schema, model.KindTable)
		if err != nil {
			return mdl, err
		}
		for _, o := range objs {
			names = append(names, o.Name)
		}
	}
	tables, err := loadDesigns(ctx, c, database, schema, names)
	if err != nil {
		return mdl, err
	}
	for _, t := range tables {
		// Models are database-independent; keep only the PostgreSQL schema.
		t.Database = ""
		mdl.Tables = append(mdl.Tables, t)
	}
	return mdl, nil
}

// PlanModelSync diffs a model against a database.
func (m *Manager) PlanModelSync(ctx context.Context, connID string, mdl datamodel.Model, to datamodel.Target, dropMissing bool) (datamodel.SyncPlan, error) {
	c, err := m.conn(connID)
	if err != nil {
		return datamodel.SyncPlan{}, err
	}
	if (mdl.Dialect == "postgres") != (c.Dialect().Name == "postgres") {
		return datamodel.SyncPlan{}, fmt.Errorf("a %s model can't be synchronized to a %s database", mdl.Dialect, c.Dialect().Name)
	}
	schema := to.Schema
	if mdl.Dialect == "postgres" && schema == "" {
		schema = mdl.Schema
	}
	objs, err := c.ListObjects(ctx, to.Database, schema, model.KindTable)
	if err != nil {
		return datamodel.SyncPlan{}, err
	}
	names := make([]string, len(objs))
	for i, o := range objs {
		names[i] = o.Name
	}
	existing, err := loadDesigns(ctx, c, to.Database, schema, names)
	if err != nil {
		return datamodel.SyncPlan{}, err
	}
	if mdl.Dialect == "postgres" {
		to.Schema = schema
	}
	return mdl.Plan(existing, to, dropMissing)
}

// ApplyModelSync runs a previously previewed plan.
func (m *Manager) ApplyModelSync(ctx context.Context, connID string, to datamodel.Target, plan datamodel.SyncPlan) ApplyResult {
	c, err := m.conn(connID)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	return m.ExecuteDDL(ctx, connID, to.Database, to.Schema, plan.Statements(), c.Dialect().TransactionalDDL())
}

// designLoads is how many tables loadDesigns reads at once: each takes about ten
// catalog queries, which is what makes a schema of hundreds of tables slow.
const designLoads = 4

// loadDesigns loads the designs of the named tables, a few at a time, in order.
func loadDesigns(ctx context.Context, c driver.Conn, database, schema string, names []string) ([]design.Table, error) {
	out := make([]design.Table, len(names))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(designLoads)
	for i, n := range names {
		g.Go(func() error {
			t, err := c.LoadDesign(gctx, tableRef(database, schema, n))
			if err != nil {
				return fmt.Errorf("%s: %w", n, err)
			}
			out[i] = t
			return nil
		})
	}
	return out, g.Wait()
}

func tableRef(database, schema, name string) model.ObjectRef {
	return model.ObjectRef{Database: database, Schema: schema, Name: name, Kind: model.KindTable}
}

// DatabaseMeta lists the choices for the New / Edit Database dialog.
func (m *Manager) DatabaseMeta(ctx context.Context, connID string) (design.DatabaseMeta, error) {
	c, err := m.conn(connID)
	if err != nil {
		return design.DatabaseMeta{}, err
	}
	return c.DatabaseMeta(ctx)
}

// LoadDatabase reads a database's properties for Edit Database.
func (m *Manager) LoadDatabase(ctx context.Context, connID, name string) (design.Database, error) {
	c, err := m.conn(connID)
	if err != nil {
		return design.Database{}, err
	}
	return c.LoadDatabase(ctx, name)
}

// DatabaseSQL returns the statements New Database (old nil) or Edit Database would run.
func (m *Manager) DatabaseSQL(connID string, old *design.Database, d design.Database) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return design.DatabaseSQL(c.Dialect().Name, old, d)
}

// ApplyDatabase creates or alters a database.
func (m *Manager) ApplyDatabase(ctx context.Context, connID string, old *design.Database, d design.Database) ApplyResult {
	stmts, err := m.DatabaseSQL(connID, old, d)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	c, _ := m.conn(connID)
	if a, ok := c.(driver.Attacher); ok {
		if err := a.Attach(ctx, d.Name, d.File, d.Create); err != nil {
			return ApplyResult{Error: err.Error()}
		}
		m.saveAttachments(connID, a)
		return ApplyResult{Executed: 1}
	}
	if old != nil && old.Name != d.Name {
		// PostgreSQL can't rename a database we hold connections to.
		c.CloseDatabase(old.Name)
	}
	return m.ExecuteDDL(ctx, connID, "", "", stmts, false)
}

// DropDatabase deletes a database, releasing Chloe's own connections to it first.
func (m *Manager) DropDatabase(ctx context.Context, connID, name string) ApplyResult {
	c, err := m.conn(connID)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	if a, ok := c.(driver.Attacher); ok {
		// SQLite: detach; the file stays on disk.
		if err := a.Detach(ctx, name); err != nil {
			return ApplyResult{Error: err.Error()}
		}
		m.saveAttachments(connID, a)
		return ApplyResult{Executed: 1}
	}
	c.CloseDatabase(name)
	return m.ExecuteDDL(ctx, connID, "", "", []string{"DROP DATABASE " + c.Dialect().QuoteIdent(name)}, false)
}

// saveAttachments records a SQLite connection's attached files in its profile,
// so they're attached again next time.
func (m *Manager) saveAttachments(connID string, a driver.Attacher) {
	p, err := m.Store.Get(connID)
	if err != nil {
		return
	}
	p.SQLite.Attached = a.Attached()
	_, _ = m.Store.Save(p)
	m.mu.Lock()
	if oc, ok := m.conns[connID]; ok {
		oc.profile.SQLite.Attached = p.SQLite.Attached
	}
	m.mu.Unlock()
}

// LoadView reads a view for the view designer.
func (m *Manager) LoadView(ctx context.Context, connID string, ref model.ObjectRef) (design.View, error) {
	c, err := m.conn(connID)
	if err != nil {
		return design.View{}, err
	}
	return c.LoadView(ctx, ref)
}

// PreviewView returns the SQL saving a view would run. old is nil for a new view.
func (m *Manager) PreviewView(connID string, old *design.View, v design.View) (DesignPreview, error) {
	c, err := m.conn(connID)
	if err != nil {
		return DesignPreview{}, err
	}
	stmts, warns, err := design.ViewSQL(c.Dialect().Name, old, v)
	return DesignPreview{Statements: stmts, Warnings: warns, Transactional: c.Dialect().TransactionalDDL()}, err
}

// ApplyView saves a view and returns it re-read from the server.
func (m *Manager) ApplyView(ctx context.Context, connID string, old *design.View, v design.View) (ApplyResult, error) {
	p, err := m.PreviewView(connID, old, v)
	if err != nil {
		return ApplyResult{}, err
	}
	res := m.ExecuteDDL(ctx, connID, v.Database, v.Schema, p.Statements, p.Transactional)
	if res.Error != "" {
		return res, nil
	}
	kind := model.KindView
	if v.Materialized {
		kind = model.KindMatView
	}
	if nv, err := m.LoadView(ctx, connID, model.ObjectRef{Database: v.Database, Schema: v.Schema, Name: v.Name, Kind: kind}); err == nil {
		res.View = &nv
	}
	return res, nil
}

// RefreshMaterializedView re-runs a materialized view's query.
func (m *Manager) RefreshMaterializedView(ctx context.Context, connID string, ref model.ObjectRef, concurrently bool) ApplyResult {
	c, err := m.conn(connID)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	st := "REFRESH MATERIALIZED VIEW "
	if concurrently {
		st += "CONCURRENTLY "
	}
	return m.ExecuteDDL(ctx, connID, ref.Database, ref.Schema, []string{st + c.Dialect().Qualified(ref)}, false)
}
