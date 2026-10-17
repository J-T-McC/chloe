package core

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"

	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/safefile"
	"chloe/internal/sqlq"
	"chloe/internal/sqlsplit"
	"chloe/internal/transfer"
)

// DumpRequest describes a "Dump SQL File" of a database (MySQL) or schema (PostgreSQL).
type DumpRequest struct {
	Database string   `json:"database"`
	Schema   string   `json:"schema,omitempty"`
	Tables   []string `json:"tables,omitempty"` // empty = every table, plus views and routines
	Data     bool     `json:"data"`             // false = structure only
	Path     string   `json:"path"`
}

// Dump writes a SQL script that recreates the objects (and optionally data).
func (m *Manager) Dump(connID string, req DumpRequest) (Job, error) {
	c, err := m.conn(connID)
	if err != nil {
		return Job{}, err
	}
	what := req.Database
	if req.Schema != "" {
		what += "." + req.Schema
	}
	return m.startJob("dump", "Dump "+what+" → "+baseName(req.Path), "rows", func(ctx context.Context, p *progress) (string, error) {
		p.setPath(req.Path)
		f, err := safefile.Create(req.Path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		w := bufio.NewWriterSize(f, 1<<16)
		d := &dumper{m: m, c: c, d: c.Dialect(), connID: connID, req: req, w: w, p: p}
		if err := d.run(ctx); err != nil {
			return "", err
		}
		if err := w.Flush(); err != nil {
			return "", err
		}
		return fmt.Sprintf("Dumped %d tables (%d rows) to %s", d.tables, d.rows, req.Path), f.Commit()
	}), nil
}

type dumper struct {
	m      *Manager
	c      driver.Conn
	d      driver.Dialect
	connID string
	req    DumpRequest
	w      *bufio.Writer
	p      *progress
	tables int
	rows   int64
}

func (d *dumper) pg() bool { return d.d.Name == "postgres" }

var definerRe = regexp.MustCompile("DEFINER=`[^`]*`@`[^`]*` ")

// portable makes MySQL view/routine DDL restorable into another database or server:
// SHOW CREATE qualifies names with the source database and records the definer.
func (d *dumper) portable(ddl string) string {
	ddl = definerRe.ReplaceAllString(ddl, "")
	return strings.ReplaceAll(ddl, d.d.QuoteIdent(d.req.Database)+".", "")
}

func (d *dumper) ref(name string, kind model.ObjectKind) model.ObjectRef {
	return model.ObjectRef{Database: d.req.Database, Schema: d.req.Schema, Name: name, Kind: kind}
}

func (d *dumper) section(title string) {
	fmt.Fprintf(d.w, "\n-- ----------------------------\n-- %s\n-- ----------------------------\n", title)
}

func (d *dumper) list(ctx context.Context, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	objs, err := d.c.ListObjects(ctx, d.req.Database, d.req.Schema, kind)
	if err != nil {
		return nil, err
	}
	if kind == model.KindTable && len(d.req.Tables) > 0 {
		keep := map[string]bool{}
		for _, t := range d.req.Tables {
			keep[t] = true
		}
		var out []model.ObjectInfo
		for _, o := range objs {
			if keep[o.Name] {
				out = append(out, o)
			}
		}
		return out, nil
	}
	return objs, nil
}

func (d *dumper) run(ctx context.Context) error {
	tables, err := d.list(ctx, model.KindTable)
	if err != nil {
		return err
	}
	all := len(d.req.Tables) == 0
	fmt.Fprintf(d.w, "-- Chloe SQL dump\n-- Server version: %s\n-- Database: %s\n", d.c.ServerVersion(), d.req.Database)
	if d.req.Schema != "" {
		fmt.Fprintf(d.w, "-- Schema: %s\n", d.req.Schema)
	}
	fmt.Fprintf(d.w, "-- Date: %s\n", time.Now().Format(time.RFC3339))
	switch d.d.Name {
	case "postgres":
		return d.runPG(ctx, tables, all)
	case "sqlite":
		return d.runSQLite(ctx, tables, all)
	}
	return d.runMySQL(ctx, tables, all)
}

func (d *dumper) runMySQL(ctx context.Context, tables []model.ObjectInfo, all bool) error {
	d.w.WriteString("\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS = 0;\n")
	var designs []design.Table
	for _, t := range tables {
		ref := d.ref(t.Name, model.KindTable)
		ddl, err := d.c.DDL(ctx, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		d.section("Table structure for " + t.Name)
		fmt.Fprintf(d.w, "DROP TABLE IF EXISTS %s;\n%s\n", d.d.QuoteIdent(t.Name), ddl)
		td, err := d.c.LoadDesign(ctx, ref)
		if err != nil {
			return err
		}
		designs = append(designs, td)
		if d.req.Data {
			if err := d.data(ctx, td); err != nil {
				return err
			}
		}
		d.tables++
	}
	if all {
		views, err := d.list(ctx, model.KindView)
		if err != nil {
			return err
		}
		for _, v := range views {
			ddl, err := d.c.DDL(ctx, d.ref(v.Name, model.KindView))
			if err != nil {
				return err
			}
			d.section("View structure for " + v.Name)
			fmt.Fprintf(d.w, "DROP VIEW IF EXISTS %s;\n%s\n", d.d.QuoteIdent(v.Name), d.portable(ddl))
		}
		for _, kind := range []model.ObjectKind{model.KindFunction, model.KindProcedure, model.KindEvent} {
			objs, err := d.list(ctx, kind)
			if err != nil {
				return err
			}
			for _, o := range objs {
				ddl, err := d.c.DDL(ctx, d.ref(o.Name, kind))
				if err != nil {
					return err
				}
				kw := strings.ToUpper(string(kind))
				d.section(strings.Title(string(kind)) + " structure for " + o.Name)
				fmt.Fprintf(d.w, "DROP %s IF EXISTS %s;\nDELIMITER ;;\n%s;\nDELIMITER ;\n", kw, d.d.QuoteIdent(o.Name), strings.TrimSuffix(d.portable(ddl), ";"))
			}
		}
	}
	// Triggers come after the data so restoring doesn't fire them.
	for _, td := range designs {
		for _, tr := range td.Triggers {
			d.section("Trigger " + tr.Name)
			fmt.Fprintf(d.w, "DROP TRIGGER IF EXISTS %s;\nDELIMITER ;;\nCREATE TRIGGER %s %s %s ON %s FOR EACH ROW %s;;\nDELIMITER ;\n",
				d.d.QuoteIdent(tr.Name), d.d.QuoteIdent(tr.Name), tr.Timing, tr.Events[0], d.d.QuoteIdent(td.Name), strings.TrimSpace(tr.Body))
		}
	}
	d.w.WriteString("\nSET FOREIGN_KEY_CHECKS = 1;\n")
	return nil
}

// runSQLite writes tables, then data, then indexes, views and triggers, in one
// transaction with foreign keys off, using the statements SQLite stored.
func (d *dumper) runSQLite(ctx context.Context, tables []model.ObjectInfo, all bool) error {
	d.w.WriteString("\nPRAGMA foreign_keys = OFF;\nBEGIN TRANSACTION;\n")
	var later []string
	for _, t := range tables {
		if t.Engine == "virtual" {
			continue
		}
		ref := d.ref(t.Name, model.KindTable)
		ddl, err := d.c.DDL(ctx, ref)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		// DDL returns the table followed by its indexes and triggers.
		stmts := sqlsplit.Split(ddl, sqlsplit.SQLite)
		if len(stmts) == 0 {
			continue
		}
		d.section("Table structure for " + t.Name)
		fmt.Fprintf(d.w, "DROP TABLE IF EXISTS %s;\n%s;\n", d.d.QuoteIdent(t.Name), stmts[0].SQL)
		for _, st := range stmts[1:] {
			later = append(later, st.SQL)
		}
		if d.req.Data {
			td, err := d.c.LoadDesign(ctx, ref)
			if err != nil {
				return err
			}
			if err := d.data(ctx, td); err != nil {
				return err
			}
		}
		d.tables++
	}
	if len(later) > 0 {
		d.section("Indexes and triggers")
		for _, st := range later {
			fmt.Fprintf(d.w, "%s;\n", st)
		}
	}
	if all {
		views, err := d.list(ctx, model.KindView)
		if err != nil {
			return err
		}
		for _, v := range views {
			ddl, err := d.c.DDL(ctx, d.ref(v.Name, model.KindView))
			if err != nil {
				return err
			}
			d.section("View structure for " + v.Name)
			fmt.Fprintf(d.w, "DROP VIEW IF EXISTS %s;\n%s\n", d.d.QuoteIdent(v.Name), ddl)
		}
	}
	d.w.WriteString("\nCOMMIT;\nPRAGMA foreign_keys = ON;\n")
	return nil
}

func (d *dumper) runPG(ctx context.Context, tables []model.ObjectInfo, all bool) error {
	schema := d.req.Schema
	if schema == "" {
		schema = "public"
		d.req.Schema = schema
	}
	d.w.WriteString("\nSET client_encoding = 'UTF8';\nSET standard_conforming_strings = on;\nSET check_function_bodies = false;\n")
	fmt.Fprintf(d.w, "CREATE SCHEMA IF NOT EXISTS %s;\n", d.d.QuoteIdent(schema))
	gen := design.Postgres{}

	if all {
		for _, kind := range []model.ObjectKind{model.KindType, model.KindFunction, model.KindProcedure} {
			objs, err := d.list(ctx, kind)
			if err != nil {
				return err
			}
			for _, o := range objs {
				ddl, err := d.c.DDL(ctx, model.ObjectRef{Database: d.req.Database, Schema: o.Schema, Name: o.Name, Kind: kind, Signature: o.Signature})
				if err != nil {
					return err
				}
				d.section(strings.Title(string(kind)) + " " + o.Name)
				d.w.WriteString(ddl + "\n")
			}
		}
		// Standalone and serial sequences (identity sequences come with their tables).
		seqs, err := d.standaloneSequences(ctx, schema)
		if err != nil {
			return err
		}
		for _, s := range seqs {
			ddl, err := d.c.DDL(ctx, d.ref(s, model.KindSequence))
			if err != nil {
				return err
			}
			d.section("Sequence " + s)
			d.w.WriteString(strings.Replace(ddl, "CREATE SEQUENCE", "CREATE SEQUENCE IF NOT EXISTS", 1) + "\n")
		}
	}

	var designs []design.Table
	for _, t := range tables {
		td, err := d.c.LoadDesign(ctx, d.ref(t.Name, model.KindTable))
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		designs = append(designs, td)
		bare := td
		bare.ForeignKeys, bare.Triggers = nil, nil
		stmts, err := gen.Create(bare)
		if err != nil {
			return fmt.Errorf("%s: %w", t.Name, err)
		}
		d.section("Table structure for " + t.Name)
		fmt.Fprintf(d.w, "DROP TABLE IF EXISTS %s CASCADE;\n%s;\n", d.d.Qualified(d.ref(t.Name, model.KindTable)), strings.Join(stmts, ";\n"))
		if d.req.Data {
			if err := d.data(ctx, td); err != nil {
				return err
			}
			d.resetSequences(td)
		}
		d.tables++
	}
	for _, td := range designs {
		if len(td.ForeignKeys) == 0 {
			continue
		}
		bare := td
		bare.ForeignKeys, bare.Triggers = nil, nil
		withFK := bare
		withFK.ForeignKeys = td.ForeignKeys
		stmts, err := gen.Alter(bare, withFK)
		if err != nil {
			return err
		}
		d.section("Foreign keys for " + td.Name)
		d.w.WriteString(strings.Join(stmts, ";\n") + ";\n")
	}
	for _, td := range designs {
		if len(td.Triggers) == 0 {
			continue
		}
		bare := td
		bare.Triggers = nil
		stmts, err := gen.Alter(bare, td)
		if err != nil {
			return err
		}
		d.section("Triggers for " + td.Name)
		d.w.WriteString(strings.Join(stmts, ";\n") + ";\n")
	}
	if all {
		for _, kind := range []model.ObjectKind{model.KindView, model.KindMatView} {
			objs, err := d.list(ctx, kind)
			if err != nil {
				return err
			}
			for _, v := range objs {
				ddl, err := d.c.DDL(ctx, d.ref(v.Name, kind))
				if err != nil {
					return err
				}
				d.section("View " + v.Name)
				d.w.WriteString(strings.TrimSuffix(strings.TrimSpace(ddl), ";") + ";\n")
			}
		}
	}
	return nil
}

func (d *dumper) standaloneSequences(ctx context.Context, schema string) ([]string, error) {
	sid, err := d.m.OpenSession(ctx, d.connID, d.req.Database, schema)
	if err != nil {
		return nil, err
	}
	defer d.m.CloseSession(sid)
	s, _ := d.m.session(sid)
	rows, err := s.ds.Query(ctx, fmt.Sprintf(`SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind = 'S' AND n.nspname = %s
		AND NOT EXISTS (SELECT 1 FROM pg_depend dp WHERE dp.objid = c.oid AND dp.deptype = 'i') ORDER BY 1`, transfer.Literal(&schema, "text", "postgres")))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	cell := make([]*string, 1)
	for rows.Next(cell) {
		out = append(out, *cell[0])
	}
	return out, rows.Err()
}

var nextvalRe = regexp.MustCompile(`^nextval\('((?:[^']|'')+)'(?:::regclass)?\)$`)

// resetSequences ties serial sequences back to their columns (OWNED BY) and moves
// every serial/identity sequence past the restored ids.
func (d *dumper) resetSequences(td design.Table) {
	tbl := d.d.Qualified(d.ref(td.Name, model.KindTable))
	lit := sqlq.PGString
	for _, c := range td.Columns {
		col := d.d.QuoteIdent(c.Name)
		var seq string
		switch {
		case c.Identity != "":
			seq = fmt.Sprintf("pg_get_serial_sequence(%s, %s)", lit(tbl), lit(c.Name))
		case c.Default != nil:
			m := nextvalRe.FindStringSubmatch(*c.Default)
			if m == nil {
				continue
			}
			name := strings.ReplaceAll(m[1], "''", "'")
			fmt.Fprintf(d.w, "ALTER SEQUENCE %s OWNED BY %s.%s;\n", name, tbl, col)
			seq = lit(name)
		default:
			continue
		}
		fmt.Fprintf(d.w, "SELECT setval(%s, COALESCE((SELECT max(%s) FROM %s), 1), (SELECT max(%s) FROM %s) IS NOT NULL);\n", seq, col, tbl, col, tbl)
	}
}

// data writes INSERT statements for a table, skipping generated columns.
func (d *dumper) data(ctx context.Context, td design.Table) error {
	var cols []string
	overriding := false
	for _, c := range td.Columns {
		if strings.TrimSpace(c.GeneratedExpr) != "" {
			continue
		}
		if c.Identity == "always" {
			overriding = true
		}
		cols = append(cols, d.d.QuoteIdent(c.Name))
	}
	ref := d.ref(td.Name, model.KindTable)
	q := "SELECT " + strings.Join(cols, ", ") + " FROM " + d.d.Qualified(ref)
	ds, err := d.c.NewSession(ctx, d.req.Database, d.req.Schema)
	if err != nil {
		return err
	}
	defer ds.Close()
	d.section("Records of " + td.Name)
	table := d.d.QuoteIdent(td.Name)
	if d.pg() {
		table = d.d.Qualified(ref)
	}
	n, err := streamExport(ctx, ds, q, d.w, transfer.ExportOptions{Format: transfer.SQL, TableName: table, RowsPerInsert: 100,
		Dialect: d.d.Name, OverridingSystemValue: overriding}, func(n int64) { d.p.setDone(d.rows + n) })
	d.rows += n
	return err
}

// ---- execute SQL file ---------------------------------------------------------------

type ExecuteFileRequest struct {
	Path            string `json:"path"`
	Database        string `json:"database"`
	Schema          string `json:"schema,omitempty"`
	Encoding        string `json:"encoding,omitempty"`
	ContinueOnError bool   `json:"continueOnError"`
}

// ExecuteFile runs every statement of a SQL script.
func (m *Manager) ExecuteFile(connID string, req ExecuteFileRequest) (Job, error) {
	c, err := m.conn(connID)
	if err != nil {
		return Job{}, err
	}
	return m.startJob("execute", "Execute "+baseName(req.Path), "statements", func(ctx context.Context, p *progress) (string, error) {
		script, err := readText(req.Path, req.Encoding)
		if err != nil {
			return "", err
		}
		stmts := sqlsplit.Split(script, c.Dialect().Split)
		p.set(0, int64(len(stmts)))
		ds, err := c.NewSession(ctx, req.Database, req.Schema)
		if err != nil {
			return "", err
		}
		defer ds.Close()
		failed := 0
		for i, st := range stmts {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			rows, err := ds.Query(ctx, st.SQL)
			if err == nil {
				// Drain result sets (SELECTs in scripts are ignored).
				cell := make([]*string, len(rows.Columns()))
				for rows.Next(cell) {
				}
				err = rows.Err()
				rows.Close()
			}
			if err != nil {
				failed++
				p.error("line %d: %v", st.StartLine, err)
				if !req.ContinueOnError {
					return "", fmt.Errorf("statement at line %d failed: %w", st.StartLine, err)
				}
			}
			p.setDone(int64(i + 1))
		}
		msg := fmt.Sprintf("Executed %d statements", len(stmts))
		if failed > 0 {
			msg += fmt.Sprintf(", %d failed", failed)
		}
		return msg, nil
	}), nil
}

func readText(path, encoding string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	var r io.Reader = bufio.NewReader(f)
	if encoding != "" && !strings.EqualFold(encoding, "utf-8") {
		enc, err := htmlindex.Get(encoding)
		if err != nil {
			return "", fmt.Errorf("unknown encoding %q", encoding)
		}
		r = transform.NewReader(r, enc.NewDecoder())
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(string(b), "\ufeff"), nil
}
