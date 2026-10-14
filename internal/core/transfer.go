package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/transfer"
)

// ExportSource is either a table (with optional filter) or a SQL query.
type ExportSource struct {
	Ref     *model.ObjectRef `json:"ref,omitempty"`
	Where   string           `json:"where,omitempty"`
	Columns []string         `json:"columns,omitempty"` // subset of table columns; empty = all
	SQL     string           `json:"sql,omitempty"`
	// Database/Schema for SQL sources.
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
}

type ExportRequest struct {
	Source  ExportSource           `json:"source"`
	Options transfer.ExportOptions `json:"options"`
	Path    string                 `json:"path"`
}

// Export streams rows from the server into a file as a background job.
func (m *Manager) Export(connID string, req ExportRequest) (Job, error) {
	c, err := m.conn(connID)
	if err != nil {
		return Job{}, err
	}
	d := c.Dialect()
	src := req.Source
	query, title := src.SQL, "Export query"
	db, schema := src.Database, src.Schema
	if src.Ref != nil {
		db, schema = src.Ref.Database, src.Ref.Schema
		cols := "*"
		if len(src.Columns) > 0 {
			q := make([]string, len(src.Columns))
			for i, col := range src.Columns {
				q[i] = d.QuoteIdent(col)
			}
			cols = strings.Join(q, ", ")
		}
		query = "SELECT " + cols + " FROM " + d.Qualified(*src.Ref)
		if w := strings.TrimSpace(src.Where); w != "" {
			query += " WHERE " + w
		}
		title = "Export " + src.Ref.Name
		if req.Options.Format == transfer.SQL && req.Options.TableName == "" {
			req.Options.TableName = d.QuoteIdent(src.Ref.Name)
		}
	}
	if strings.TrimSpace(query) == "" {
		return Job{}, errors.New("nothing to export")
	}
	req.Options.Dialect = d.Name
	return m.startJob("export", title+" → "+baseName(req.Path), "rows", func(ctx context.Context, p *progress) (string, error) {
		if src.Ref != nil {
			if n, err := m.CountRows(ctx, connID, *src.Ref, src.Where); err == nil {
				p.set(0, n)
			}
		}
		ds, err := c.NewSession(ctx, db, schema)
		if err != nil {
			return "", err
		}
		defer ds.Close()
		p.setPath(req.Path)
		f, err := os.Create(req.Path)
		if err != nil {
			return "", err
		}
		defer f.Close()
		n, err := streamExport(ctx, ds, query, f, req.Options, p)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Exported %d rows to %s", n, req.Path), f.Sync()
	}), nil
}

func streamExport(ctx context.Context, ds driver.Session, query string, out io.Writer, o transfer.ExportOptions, p *progress) (int64, error) {
	rows, err := ds.Query(ctx, query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.HasRows() {
		return 0, errors.New("the statement returned no result set")
	}
	w, err := transfer.NewWriter(out, o)
	if err != nil {
		return 0, err
	}
	cols := rows.Columns()
	if err := w.Begin(cols); err != nil {
		return 0, err
	}
	var n int64
	row := make([]*string, len(cols))
	for rows.Next(row) {
		if err := w.Write(row); err != nil {
			return n, err
		}
		n++
		if n%1000 == 0 {
			if ctx.Err() != nil {
				_ = ds.Cancel(context.Background())
				return n, ctx.Err()
			}
			p.setDone(n)
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	p.setDone(n)
	return n, w.Close()
}

// ---- import ----------------------------------------------------------------------

// ImportMode follows Navicat's import modes.
type ImportMode string

const (
	ImportAppend       ImportMode = "append" // insert every row
	ImportUpsert       ImportMode = "upsert" // insert, or update rows whose key exists
	ImportSkipExisting ImportMode = "skip"   // insert, skipping rows whose key exists
	ImportCopy         ImportMode = "copy"   // delete all rows first, then insert
	ImportUpdate       ImportMode = "update" // update matching rows only
	ImportDelete       ImportMode = "delete" // delete matching rows
)

type FieldMap struct {
	Source string `json:"source"` // file column
	Target string `json:"target"` // table column
}

type ImportRequest struct {
	Path    string                 `json:"path"`
	Options transfer.ImportOptions `json:"options"`
	Target  model.ObjectRef        `json:"target"`
	// CreateTable creates Target first with these columns (from the mapping UI).
	CreateTable *design.Table `json:"createTable,omitempty"`
	Mapping     []FieldMap    `json:"mapping"`
	Mode        ImportMode    `json:"mode"`
	// Key columns for upsert/skip/update/delete; defaults to the primary key.
	KeyColumns      []string `json:"keyColumns,omitempty"`
	BatchSize       int      `json:"batchSize,omitempty"` // rows per INSERT; default 500
	ContinueOnError bool     `json:"continueOnError"`
	// Transaction wraps the whole import so a failure leaves the table untouched.
	Transaction     bool `json:"transaction"`
	DisableFKChecks bool `json:"disableFKChecks"`
}

// ImportPreview is shown in the mapping step.
type ImportPreview struct {
	Columns []string        `json:"columns"`
	Rows    [][]*string     `json:"rows"`
	Types   []string        `json:"types"` // inferred, for creating a new table
	Sheets  []string        `json:"sheets,omitempty"`
	Format  transfer.Format `json:"format"`
}

func (m *Manager) PreviewImport(connID, path string, o transfer.ImportOptions) (ImportPreview, error) {
	c, err := m.conn(connID)
	if err != nil {
		return ImportPreview{}, err
	}
	if o.Format == "" {
		o.Format = transfer.DetectFormat(path)
	}
	p := ImportPreview{Format: o.Format}
	if o.Format == transfer.XLSX {
		p.Sheets, _ = transfer.Sheets(path)
	}
	cols, rows, err := transfer.Preview(path, o, 200)
	if err != nil {
		return p, err
	}
	p.Columns, p.Rows = cols, rows
	if len(p.Rows) > 100 {
		p.Rows = p.Rows[:100]
	}
	p.Types = transfer.InferTypes(rows, len(cols), c.Dialect().Name)
	return p, nil
}

// Import loads a file into a table as a background job.
func (m *Manager) Import(ctx context.Context, connID string, req ImportRequest) (Job, error) {
	c, err := m.conn(connID)
	if err != nil {
		return Job{}, err
	}
	if len(req.Mapping) == 0 {
		return Job{}, errors.New("map at least one field")
	}
	d := c.Dialect()
	keys := req.KeyColumns
	if req.CreateTable == nil && req.Mode != ImportAppend && req.Mode != ImportCopy && len(keys) == 0 {
		td, err := c.TableDetail(ctx, req.Target)
		if err != nil {
			return Job{}, err
		}
		for _, col := range td.Columns {
			if col.PrimaryKey {
				keys = append(keys, col.Name)
			}
		}
		if len(keys) == 0 {
			return Job{}, fmt.Errorf("%s mode needs a primary key (or chosen key columns) on %s", req.Mode, req.Target.Name)
		}
	}
	return m.startJob("import", "Import "+baseName(req.Path)+" → "+req.Target.Name, "rows", func(ctx context.Context, p *progress) (string, error) {
		if req.CreateTable != nil {
			stmts, err := c.DesignGenerator().Create(*req.CreateTable)
			if err != nil {
				return "", err
			}
			if r := m.ExecuteDDL(ctx, connID, req.Target.Database, req.Target.Schema, stmts, d.Name == "postgres"); r.Error != "" {
				return "", fmt.Errorf("create table: %s", r.Error)
			}
			p.logf("Created table %s", req.Target.Name)
		}
		src, err := transfer.Open(req.Path, req.Options)
		if err != nil {
			return "", err
		}
		defer src.Close()
		ds, err := c.NewSession(ctx, req.Target.Database, req.Target.Schema)
		if err != nil {
			return "", err
		}
		defer ds.Close()
		imp := &importer{d: d, ds: ds, req: req, keys: keys, p: p}
		return imp.run(ctx, src)
	}), nil
}

type importer struct {
	d    driver.Dialect
	ds   driver.Session
	req  ImportRequest
	keys []string
	p    *progress

	srcIdx []int    // index into the source row for each mapped column
	cols   []string // target columns
}

func (im *importer) run(ctx context.Context, src transfer.Source) (string, error) {
	req := im.req
	exec := func(q string, args ...any) error {
		_, err := im.ds.Exec(ctx, q, args...)
		return err
	}
	// Resolve the mapping against the file's columns. JSON/Excel columns are known
	// up front; delimited files without a header learn them from the first row.
	var pending []*string
	srcCols := src.Columns()
	if len(srcCols) == 0 {
		r, err := src.Next()
		if errors.Is(err, io.EOF) {
			return "File is empty", nil
		}
		if err != nil {
			return "", err
		}
		pending, srcCols = r, src.Columns()
	}
	pos := map[string]int{}
	for i, c := range srcCols {
		pos[c] = i
	}
	for _, f := range req.Mapping {
		i, ok := pos[f.Source]
		if !ok {
			return "", fmt.Errorf("source field %q not found in file", f.Source)
		}
		im.srcIdx = append(im.srcIdx, i)
		im.cols = append(im.cols, f.Target)
	}
	if im.req.Mode == ImportUpdate || im.req.Mode == ImportDelete || im.req.Mode == ImportUpsert || im.req.Mode == ImportSkipExisting {
		for _, k := range im.keys {
			if !containsStr(im.cols, k) {
				return "", fmt.Errorf("key column %q must be mapped for %s mode", k, req.Mode)
			}
		}
	}

	if req.DisableFKChecks && im.d.Name != "postgres" {
		if err := exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
			return "", err
		}
		defer exec("SET FOREIGN_KEY_CHECKS = 1")
	}
	if req.Transaction {
		if err := exec("BEGIN"); err != nil {
			return "", err
		}
	}
	committed := false
	defer func() {
		if req.Transaction && !committed {
			_, _ = im.ds.Exec(context.Background(), "ROLLBACK")
		}
	}()
	if req.Mode == ImportCopy {
		if err := exec("DELETE FROM " + im.d.Qualified(req.Target)); err != nil {
			return "", err
		}
		im.p.logf("Deleted existing rows")
	}

	batch := req.BatchSize
	if batch <= 0 {
		batch = 500
	}
	// Bind-parameter limits (65535 for both servers) cap the batch.
	if max := 60000 / max(1, len(im.cols)); batch > max {
		batch = max
	}
	if req.Mode == ImportUpdate || req.Mode == ImportDelete {
		batch = 1
	}
	var buf [][]*string
	var done, affected int64
	flush := func() error {
		if len(buf) == 0 {
			return nil
		}
		n, err := im.writeBatch(ctx, buf)
		if err != nil {
			if !req.ContinueOnError || req.Transaction {
				return fmt.Errorf("row %d: %w", done+1, err)
			}
			// Retry row by row to isolate the bad rows.
			for i, r := range buf {
				if n1, err1 := im.writeBatch(ctx, [][]*string{r}); err1 != nil {
					im.p.error("row %d: %v", done+int64(i)+1, err1)
				} else {
					affected += n1
				}
			}
		} else {
			affected += n
		}
		done += int64(len(buf))
		im.p.setDone(done)
		buf = buf[:0]
		return nil
	}
	next := func() ([]*string, error) {
		if pending != nil {
			r := pending
			pending = nil
			return r, nil
		}
		return src.Next()
	}
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		r, err := next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("reading row %d: %w", done+int64(len(buf))+1, err)
		}
		out := make([]*string, len(im.srcIdx))
		for i, si := range im.srcIdx {
			if si < len(r) {
				out[i] = r[si]
			}
		}
		buf = append(buf, out)
		if len(buf) >= batch {
			if err := flush(); err != nil {
				return "", err
			}
		}
	}
	if err := flush(); err != nil {
		return "", err
	}
	if req.Transaction {
		if err := exec("COMMIT"); err != nil {
			return "", err
		}
		committed = true
	}
	msg := fmt.Sprintf("Processed %d rows, %d affected", done, affected)
	if n := im.p.errorCount(); n > 0 {
		msg += fmt.Sprintf(", %d failed", n)
	}
	return msg, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// writeBatch writes rows according to the import mode and returns rows affected.
func (im *importer) writeBatch(ctx context.Context, rows [][]*string) (int64, error) {
	d := im.d
	table := d.Qualified(im.req.Target)
	qcols := make([]string, len(im.cols))
	for i, c := range im.cols {
		qcols[i] = d.QuoteIdent(c)
	}
	var args []any
	ph := func(v *string) string {
		if v == nil {
			args = append(args, nil)
		} else {
			args = append(args, *v)
		}
		return d.Placeholder(len(args))
	}
	switch im.req.Mode {
	case ImportUpdate, ImportDelete:
		r := rows[0]
		var sets, where []string
		if im.req.Mode == ImportUpdate {
			for i, c := range im.cols {
				if !containsStr(im.keys, c) {
					sets = append(sets, qcols[i]+" = "+ph(r[i]))
				}
			}
			if len(sets) == 0 {
				return 0, nil
			}
		}
		for i, c := range im.cols {
			if containsStr(im.keys, c) {
				where = append(where, qcols[i]+" = "+ph(r[i]))
			}
		}
		if im.req.Mode == ImportDelete {
			return im.ds.Exec(ctx, "DELETE FROM "+table+" WHERE "+strings.Join(where, " AND "), args...)
		}
		return im.ds.Exec(ctx, "UPDATE "+table+" SET "+strings.Join(sets, ", ")+" WHERE "+strings.Join(where, " AND "), args...)
	}

	values := make([]string, len(rows))
	for i, r := range rows {
		cells := make([]string, len(r))
		for j, v := range r {
			cells[j] = ph(v)
		}
		values[i] = "(" + strings.Join(cells, ", ") + ")"
	}
	q := "INSERT INTO " + table + " (" + strings.Join(qcols, ", ") + ") VALUES " + strings.Join(values, ", ")
	var nonKey []string
	for i, c := range im.cols {
		if !containsStr(im.keys, c) {
			nonKey = append(nonKey, qcols[i])
		}
	}
	qkeys := make([]string, len(im.keys))
	for i, k := range im.keys {
		qkeys[i] = d.QuoteIdent(k)
	}
	switch im.req.Mode {
	case ImportUpsert:
		if d.Name == "postgres" {
			sets := make([]string, len(nonKey))
			for i, c := range nonKey {
				sets[i] = c + " = EXCLUDED." + c
			}
			if len(sets) == 0 {
				q += " ON CONFLICT (" + strings.Join(qkeys, ", ") + ") DO NOTHING"
			} else {
				q += " ON CONFLICT (" + strings.Join(qkeys, ", ") + ") DO UPDATE SET " + strings.Join(sets, ", ")
			}
		} else {
			sets := make([]string, 0, len(nonKey))
			for _, c := range nonKey {
				sets = append(sets, c+" = VALUES("+c+")")
			}
			if len(sets) == 0 {
				sets = append(sets, qkeys[0]+" = "+qkeys[0])
			}
			q += " ON DUPLICATE KEY UPDATE " + strings.Join(sets, ", ")
		}
	case ImportSkipExisting:
		if d.Name == "postgres" {
			q += " ON CONFLICT (" + strings.Join(qkeys, ", ") + ") DO NOTHING"
		} else {
			// A no-op update skips duplicates without INSERT IGNORE's habit of
			// swallowing unrelated errors.
			q += " ON DUPLICATE KEY UPDATE " + qkeys[0] + " = " + qkeys[0]
		}
	}
	return im.ds.Exec(ctx, q, args...)
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}
