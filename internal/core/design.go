package core

import (
	"context"
	"fmt"
	"strings"

	"chloe/internal/datamodel"
	"chloe/internal/design"
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
	p := DesignPreview{Transactional: c.Dialect().Name == "postgres"}
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
	if transactional {
		if _, err := ds.Exec(ctx, "COMMIT"); err != nil {
			res.Error = err.Error()
			res.Executed = 0
		}
	}
	return res
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

// RunTableAction executes a table operation.
func (m *Manager) RunTableAction(ctx context.Context, connID string, ref model.ObjectRef, action TableAction, newName string) ApplyResult {
	stmts, err := m.TableActionSQL(connID, ref, action, newName)
	if err != nil {
		return ApplyResult{Error: err.Error()}
	}
	c, _ := m.conn(connID)
	return m.ExecuteDDL(ctx, connID, ref.Database, ref.Schema, stmts, c.Dialect().Name == "postgres")
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
	for _, n := range names {
		t, err := c.LoadDesign(ctx, tableRef(database, schema, n))
		if err != nil {
			return mdl, fmt.Errorf("%s: %w", n, err)
		}
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
	var existing []design.Table
	for _, o := range objs {
		t, err := c.LoadDesign(ctx, tableRef(to.Database, schema, o.Name))
		if err != nil {
			return datamodel.SyncPlan{}, err
		}
		existing = append(existing, t)
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
	return m.ExecuteDDL(ctx, connID, to.Database, to.Schema, plan.Statements(), c.Dialect().Name == "postgres")
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
	c.CloseDatabase(name)
	return m.ExecuteDDL(ctx, connID, "", "", []string{"DROP DATABASE " + c.Dialect().QuoteIdent(name)}, false)
}
