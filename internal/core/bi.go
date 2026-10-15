package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"chloe/internal/bi"
	"chloe/internal/driver"
	// Archives are SQLite; the driver registers chloe_tz for data source time zones.
	_ "chloe/internal/driver/sqlite"
	"chloe/internal/model"
)

// BI data sources read in read-only sessions, with a time limit per statement.
const (
	BITimeout = 30 * time.Second
	// MaxArchiveRows bounds an archive snapshot.
	MaxArchiveRows = 1_000_000
	probeRows      = 200
)

// BIPreview is a data source's rows as its preview grid shows them.
type BIPreview struct {
	Columns []model.ResultColumn `json:"columns"`
	Rows    [][]*string          `json:"rows"`
	SQL     string               `json:"sql"`
}

var biEngines = []model.DBType{model.MySQL, model.MariaDB, model.PostgreSQL, model.SQLite}

// biLive runs fn in a read-only session on the data source's database.
func (m *Manager) biLive(ctx context.Context, src bi.DataSource, fn func(context.Context, driver.Session, driver.Dialect) error) error {
	t, err := m.ConnType(src.ConnID)
	if err != nil {
		return fmt.Errorf("the data source's connection is gone: %w", err)
	}
	if !slices.Contains(biEngines, t) {
		return fmt.Errorf("BI data sources read MySQL, MariaDB, PostgreSQL and SQLite, not %s", t)
	}
	ctx, cancel := context.WithTimeout(ctx, BITimeout)
	defer cancel()
	return m.WithSession(ctx, src.ConnID, src.Database, src.Schema, func(ds driver.Session, d driver.Dialect) error {
		return driver.ReadOnly(ctx, ds, d, func() error {
			if err := checkZone(ctx, ds, d, src.Timezone); err != nil {
				return err
			}
			return fn(ctx, ds, d)
		})
	})
}

// checkZone makes sure the server knows a data source's time zone: MySQL's
// CONVERT_TZ quietly returns NULL when its time zone tables aren't loaded.
func checkZone(ctx context.Context, ds driver.Session, d driver.Dialect, tz string) error {
	if tz == "" || d.Name != "mysql" {
		return nil
	}
	_, rows, _, err := readStmt(ctx, ds, bi.Stmt{SQL: "SELECT CONVERT_TZ('2000-01-01 00:00:00', '+00:00', ?)", Args: []any{tz}}, 1)
	if err != nil {
		return err
	}
	if len(rows) == 0 || rows[0][0] == nil {
		return fmt.Errorf("MySQL doesn't know the time zone %q: load its time zone tables (mysql_tzinfo_to_sql /usr/share/zoneinfo | mysql -u root mysql), or clear the data source's time zone", tz)
	}
	return nil
}

// readStmt reads at most limit rows of a statement; truncated reports more.
func readStmt(ctx context.Context, ds driver.Session, q bi.Stmt, limit int) ([]model.ResultColumn, [][]*string, bool, error) {
	rows, err := ds.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	cols := rows.Columns()
	out := [][]*string{}
	truncated := false
	for {
		cells := make([]*string, len(cols))
		if !rows.Next(cells) {
			break
		}
		if len(out) >= limit {
			truncated = true
			_ = ds.Cancel(context.Background())
			break
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil && !truncated {
		return nil, nil, false, err
	}
	return cols, out, truncated, nil
}

// BIFields reads a data source's columns and their types. Columns the database
// doesn't type are typed from sample values.
func (m *Manager) BIFields(ctx context.Context, w bi.Workspace, src bi.DataSource) ([]bi.Field, error) {
	var fields []bi.Field
	previous := src.Fields
	src.Joins = slices.Clone(src.Joins)
	if err := m.biJoinColumns(ctx, &src); err != nil {
		return nil, err
	}
	src, err := w.ResolveSource(src, nil)
	if err != nil {
		return nil, err
	}
	err = m.biLive(ctx, src, func(ctx context.Context, ds driver.Session, d driver.Dialect) error {
		from, err := bi.From(d, src)
		if err != nil {
			return err
		}
		cols, rows, _, err := readStmt(ctx, ds, bi.ProbeQuery(from, probeRows), probeRows)
		if err != nil {
			return err
		}
		var unsampled []int
		fields, unsampled = probeFields(cols, rows)
		// Columns with no values to look at (a parameter or join that matches
		// nothing right now) keep the type they had, or for a table source are
		// typed from their table.
		for _, i := range unsampled {
			f := &fields[i]
			if old, ok := fieldNamed(previous, f.Name); ok {
				f.Type = old.Type
				continue
			}
			if src.Table == "" {
				continue
			}
			table, col := src.Table, f.Name
			if t, c, ok := strings.Cut(f.Name, "."); ok && slices.ContainsFunc(src.Joins, func(j bi.Join) bool { return j.Table == t }) {
				table, col = t, c
			}
			ref := model.ObjectRef{Database: src.Database, Schema: src.Schema, Name: table}
			if d.HasSchemas && ref.Schema == "" {
				ref.Schema = d.DefaultSchema
			}
			q := bi.Stmt{SQL: fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT 50", d.QuoteIdent(col), d.Qualified(ref), d.QuoteIdent(col))}
			if _, rows, _, err := readStmt(ctx, ds, q, 50); err == nil {
				var samples []string
				for _, r := range rows {
					if r[0] != nil && *r[0] != "" {
						samples = append(samples, *r[0])
					}
				}
				if g := bi.GuessType(samples); len(samples) > 0 && (f.Type == bi.TypeString || f.DBType == "") && g != bi.TypeString {
					f.Type = g
				}
			}
		}
		return nil
	})
	return fields, err
}

func fieldNamed(fs []bi.Field, name string) (bi.Field, bool) {
	for _, f := range fs {
		if f.Name == name {
			return f, true
		}
	}
	return bi.Field{}, false
}

// probeFields types columns, and lists those it had no sample values for whose
// type was only guessed.
func probeFields(cols []model.ResultColumn, rows [][]*string) ([]bi.Field, []int) {
	fields := make([]bi.Field, len(cols))
	var unsampled []int
	for i, c := range cols {
		f := bi.Field{Name: c.Name, DBType: c.Type, Type: bi.FieldType(c.Type)}
		var samples []string
		for _, r := range rows {
			if r[i] != nil && *r[i] != "" {
				samples = append(samples, *r[i])
			}
		}
		if len(samples) == 0 && (f.Type == "" || f.Type == bi.TypeString) {
			unsampled = append(unsampled, i)
		}
		switch guess := bi.GuessType(samples); {
		case f.Type == "":
			f.Type = guess
		case f.Type == bi.TypeString && len(samples) > 0 && guess == bi.TypeDateTime:
			// Dates kept as text (common in SQLite) still group by month, year…
			f.Type = bi.TypeDateTime
		}
		fields[i] = f
	}
	return fields, unsampled
}

// BIPreview reads the first rows of a data source, projected, filtered and sorted.
func (m *Manager) BIPreview(ctx context.Context, w bi.Workspace, src bi.DataSource, limit int) (BIPreview, error) {
	var out BIPreview
	src.Joins = slices.Clone(src.Joins)
	if src.Mode != bi.ModeArchive {
		if err := m.biJoinColumns(ctx, &src); err != nil {
			return out, err
		}
	}
	src, err := w.ResolveSource(src, nil)
	if err != nil {
		return out, err
	}
	run := func(ctx context.Context, q bi.Stmt, read func(bi.Stmt) ([]model.ResultColumn, [][]*string, error)) error {
		out.SQL = q.SQL
		cols, rows, err := read(q)
		out.Columns, out.Rows = cols, rows
		return err
	}
	if src.Mode == bi.ModeArchive {
		q, err := bi.PreviewQuery(bi.SQLite, bi.Table(bi.SQLite.QuoteIdent(bi.ArchiveTable)), src, limit)
		if err != nil {
			return out, err
		}
		return out, run(ctx, q, func(q bi.Stmt) ([]model.ResultColumn, [][]*string, error) {
			cols, rows, _, err := m.biArchiveQuery(ctx, w, src, q, limit)
			return cols, rows, err
		})
	}
	err = m.biLive(ctx, src, func(ctx context.Context, ds driver.Session, d driver.Dialect) error {
		from, err := bi.From(d, src)
		if err != nil {
			return err
		}
		q, err := bi.PreviewQuery(d, from, src, limit)
		if err != nil {
			return err
		}
		return run(ctx, q, func(q bi.Stmt) ([]model.ResultColumn, [][]*string, error) {
			cols, rows, _, err := readStmt(ctx, ds, q, limit)
			return cols, rows, err
		})
	})
	return out, err
}

// BIChartData reads what a chart draws. A chart whose shelves aren't filled yet
// comes back with Complete false and no rows.
//
// params override parameter values for this read (by name); the values used come
// back in Data.Params for titles and text.
func (m *Manager) BIChartData(ctx context.Context, w bi.Workspace, c bi.Chart, params map[string]string) (bi.Data, error) {
	out := bi.Data{Rows: [][]any{}, Columns: []bi.Column{}}
	stored := w.Source(c.DataSource)
	if stored == nil {
		return out, errors.New("the chart's data source is no longer in the workspace")
	}
	resolved, c, err := w.Resolve(*stored, c, params)
	if err != nil {
		return out, err
	}
	src := &resolved
	out.Mode = src.Mode
	out.Params, _ = w.Values(params)
	read := func(d driver.Dialect, from bi.Source, query func(bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error)) error {
		complete, err := c.Check(*src, d.Name)
		if err != nil || !complete {
			return err
		}
		q, cols, err := bi.ChartQuery(d, from, *src, c, bi.MaxChartRows)
		if err != nil {
			return err
		}
		out.SQL = q.SQL
		_, raw, truncated, err := query(q)
		if err != nil {
			return err
		}
		out.Complete, out.Columns, out.Truncated = true, cols, truncated
		out.Rows = bi.ShapeRange(c, cols, raw, bi.FilterWindow(*src, c))
		out.RanAt = time.Now().UTC().Format(time.RFC3339)
		if pc, span, ok := bi.SameDays(*src, c, cols, out.Rows); ok {
			pq, pcols, err := bi.ChartQuery(d, from, *src, pc, 1)
			if err != nil {
				return err
			}
			_, praw, _, err := query(pq)
			if err != nil {
				return err
			}
			if rows := bi.Shape(pc, pcols, praw); len(rows) > 0 && len(rows[0]) > 0 {
				span.Value = rows[0][0]
			}
			out.Previous = span
		}
		return nil
	}
	if src.Mode == bi.ModeArchive {
		err := read(bi.SQLite, bi.Table(bi.SQLite.QuoteIdent(bi.ArchiveTable)), func(q bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error) {
			return m.biArchiveQuery(ctx, w, *src, q, bi.MaxChartRows)
		})
		if out.Complete {
			out.RanAt = src.RefreshedAt
		}
		return out, err
	}
	if len(src.Fields) == 0 {
		fields, err := m.BIFields(ctx, w, *src)
		if err != nil {
			return out, err
		}
		src.Fields = fields
	}
	err = m.biLive(ctx, *src, func(ctx context.Context, ds driver.Session, d driver.Dialect) error {
		from, err := bi.From(d, *src)
		if err != nil {
			return err
		}
		return read(d, from, func(q bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error) {
			return readStmt(ctx, ds, q, bi.MaxChartRows)
		})
	})
	return out, err
}

// --- archive snapshots ---------------------------------------------------------

func archivePurpose(sourceID string) string { return "chloe-bi-archive/" + sourceID }

// biJoinColumns reads the columns of a table source's joined tables.
func (m *Manager) biJoinColumns(ctx context.Context, src *bi.DataSource) error {
	if len(src.Joins) == 0 {
		return nil
	}
	if err := m.Ensure(ctx, src.ConnID); err != nil {
		return err
	}
	columns := func(table string) ([]string, error) {
		t, err := m.TableDetail(ctx, src.ConnID, model.ObjectRef{Database: src.Database, Schema: src.Schema, Name: table, Kind: model.KindTable})
		if err != nil {
			return nil, fmt.Errorf("join %s: %w", table, err)
		}
		if len(t.Columns) == 0 {
			return nil, fmt.Errorf("no table or view %q to join", table)
		}
		out := make([]string, len(t.Columns))
		for k, c := range t.Columns {
			out[k] = c.Name
		}
		return out, nil
	}
	base, err := columns(src.Table)
	if err != nil {
		return err
	}
	// What the left side of a match may name: the first table's columns, then
	// table.column of each earlier join.
	known := slices.Clone(base)
	for i, j := range src.Joins {
		all, err := columns(j.Table)
		if err != nil {
			return err
		}
		// Columns given pick which of the joined table's columns come through.
		picked := all
		if len(j.Columns) > 0 {
			for _, c := range j.Columns {
				if !slices.Contains(all, c) {
					return fmt.Errorf("%s has no column %q (columns: %s)", j.Table, c, strings.Join(all, ", "))
				}
			}
			picked = j.Columns
		}
		for _, on := range j.On {
			if !slices.Contains(known, on.Left) {
				return fmt.Errorf("the join on %s matches %q, which isn't a column of %s or table.column of an earlier join", j.Table, on.Left, src.Table)
			}
			if !slices.Contains(all, on.Right) {
				return fmt.Errorf("%s has no column %q (columns: %s)", j.Table, on.Right, strings.Join(all, ", "))
			}
		}
		src.Joins[i].Columns = picked
		for _, c := range picked {
			known = append(known, j.Table+"."+c)
		}
	}
	return nil
}

// BIArchive reads a data source's rows into its archive snapshot and returns the
// source with its fields, row count and snapshot time updated.
func (m *Manager) BIArchive(ctx context.Context, w bi.Workspace, src bi.DataSource) (bi.DataSource, error) {
	fields, err := m.BIFields(ctx, w, src)
	if err != nil {
		return src, err
	}
	stored := src
	src, err = w.ResolveSource(src, nil)
	if err != nil {
		return src, err
	}
	db, err := newMemDB()
	if err != nil {
		return src, err
	}
	defer db.Close()
	cols := make([]string, len(fields))
	for i, f := range fields {
		affinity := "TEXT"
		if f.Type == bi.TypeNumber {
			affinity = "NUMERIC"
		}
		cols[i] = bi.SQLite.QuoteIdent(f.Name) + " " + affinity
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+bi.SQLite.QuoteIdent(bi.ArchiveTable)+" ("+strings.Join(cols, ", ")+")"); err != nil {
		return src, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return src, err
	}
	defer tx.Rollback()
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(fields)), ", ")
	ins, err := tx.PrepareContext(ctx, "INSERT INTO "+bi.SQLite.QuoteIdent(bi.ArchiveTable)+" VALUES ("+ph+")")
	if err != nil {
		return src, err
	}
	var n int64
	err = m.biLive(ctx, src, func(ctx context.Context, ds driver.Session, d driver.Dialect) error {
		from, err := bi.From(d, src)
		if err != nil {
			return err
		}
		rows, err := ds.Query(ctx, "SELECT * FROM "+from.SQL+" AS s", from.Args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		if len(rows.Columns()) != len(fields) {
			return errors.New("the data source's columns changed while reading it; try again")
		}
		cells := make([]*string, len(fields))
		args := make([]any, len(fields))
		for rows.Next(cells) {
			if n >= MaxArchiveRows {
				_ = ds.Cancel(context.Background())
				return fmt.Errorf("the data source has more than %d rows; filter it in its query, or use live mode", MaxArchiveRows)
			}
			for i, c := range cells {
				switch {
				case c == nil:
					args[i] = nil
				case fields[i].Type == bi.TypeDateTime:
					args[i] = bi.NormalizeDate(*c)
				default:
					args[i] = *c
				}
			}
			if _, err := ins.ExecContext(ctx, args...); err != nil {
				return err
			}
			n++
		}
		return rows.Err()
	})
	if err != nil {
		return src, err
	}
	if err := tx.Commit(); err != nil {
		return src, err
	}
	image, err := serialize(ctx, db)
	if err != nil {
		return src, err
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(image); err != nil {
		return src, err
	}
	if err := zw.Close(); err != nil {
		return src, err
	}
	sealed, err := m.Store.SealData(gz.Bytes(), archivePurpose(src.ID))
	if err != nil {
		return src, err
	}
	p := m.Store.BI().ArchivePath(w.ID, src.ID)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return src, err
	}
	if err := writeFileAtomic(p, sealed); err != nil {
		return src, err
	}
	archives.drop(p)
	// The stored definition comes back, not the copy with parameter values filled in.
	stored.Fields, stored.ArchiveRows, stored.RefreshedAt = fields, n, time.Now().UTC().Format(time.RFC3339)
	return stored, nil
}

// biArchiveQuery runs a query on a data source's archive snapshot.
func (m *Manager) biArchiveQuery(ctx context.Context, w bi.Workspace, src bi.DataSource, q bi.Stmt, limit int) ([]model.ResultColumn, [][]*string, bool, error) {
	db, err := m.archiveDB(w, src)
	if err != nil {
		return nil, nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, BITimeout)
	defer cancel()
	rows, err := db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}
	types, _ := rows.ColumnTypes()
	cols := make([]model.ResultColumn, len(names))
	for i, n := range names {
		cols[i] = model.ResultColumn{Name: n, Nullable: true}
		if i < len(types) {
			cols[i].Type = types[i].DatabaseTypeName()
		}
	}
	out := [][]*string{}
	vals := make([]any, len(names))
	ptrs := make([]any, len(names))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if len(out) >= limit {
			return cols, out, true, nil
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		row := make([]*string, len(vals))
		for i, v := range vals {
			row[i] = archiveCell(v)
		}
		out = append(out, row)
	}
	return cols, out, false, rows.Err()
}

func archiveCell(v any) *string {
	var s string
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		s = x
	case []byte:
		s = string(x)
	case int64:
		s = strconv.FormatInt(x, 10)
	case float64:
		s = strconv.FormatFloat(x, 'f', -1, 64)
	case time.Time:
		s = x.Format("2006-01-02 15:04:05")
	default:
		s = fmt.Sprint(x)
	}
	return &s
}

// archives caches open snapshots: decrypting and loading one is the slow part.
var archives = &archiveCache{dbs: map[string]*cachedArchive{}}

type archiveCache struct {
	mu  sync.Mutex
	dbs map[string]*cachedArchive
}

type cachedArchive struct {
	db   *sql.DB
	mod  time.Time
	used time.Time
}

const maxCachedArchives = 8

func (c *archiveCache) drop(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a := c.dbs[p]; a != nil {
		a.db.Close()
		delete(c.dbs, p)
	}
}

func (m *Manager) archiveDB(w bi.Workspace, src bi.DataSource) (*sql.DB, error) {
	p := m.Store.BI().ArchivePath(w.ID, src.ID)
	info, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("data source %q has no archive yet; refresh its data", src.Name)
	}
	if err != nil {
		return nil, err
	}
	archives.mu.Lock()
	defer archives.mu.Unlock()
	if a := archives.dbs[p]; a != nil && a.mod.Equal(info.ModTime()) {
		a.used = time.Now()
		return a.db, nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	plain, err := m.Store.OpenData(data, archivePurpose(src.ID))
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return nil, fmt.Errorf("the archive of %q is damaged: %w", src.Name, err)
	}
	image, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("the archive of %q is damaged: %w", src.Name, err)
	}
	db, err := newMemDB()
	if err != nil {
		return nil, err
	}
	if err := deserialize(db, image); err != nil {
		db.Close()
		return nil, err
	}
	if old := archives.dbs[p]; old != nil {
		old.db.Close()
	}
	archives.dbs[p] = &cachedArchive{db: db, mod: info.ModTime(), used: time.Now()}
	for len(archives.dbs) > maxCachedArchives {
		var oldest string
		for k, a := range archives.dbs {
			if oldest == "" || a.used.Before(archives.dbs[oldest].used) {
				oldest = k
			}
		}
		archives.dbs[oldest].db.Close()
		delete(archives.dbs, oldest)
	}
	return db, nil
}

// newMemDB opens an in-memory SQLite database on a single connection that is
// never recycled (another connection would see a different, empty database).
func newMemDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	db.SetConnMaxIdleTime(0)
	return db, nil
}

func serialize(ctx context.Context, db *sql.DB) ([]byte, error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var image []byte
	err = conn.Raw(func(dc any) error {
		s, ok := dc.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("the SQLite driver can't serialize databases")
		}
		image, err = s.Serialize()
		return err
	})
	return image, err
}

func deserialize(db *sql.DB, image []byte) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		d, ok := dc.(interface{ Deserialize([]byte) error })
		if !ok {
			return errors.New("the SQLite driver can't deserialize databases")
		}
		return d.Deserialize(image)
	})
}

func writeFileAtomic(p string, data []byte) error {
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// --- saving ------------------------------------------------------------------

// BISaveSource stores a data source in a library workspace (new when it has no
// ID) after reading its fields. An archive source with no snapshot yet, or whose
// table or query changed, is read into a new snapshot.
func (m *Manager) BISaveSource(ctx context.Context, path string, src bi.DataSource) (bi.DataSource, error) {
	lib := m.Store.BI()
	if !lib.Contains(path) {
		return src, errors.New("not a workspace in Chloe's library")
	}
	w, err := bi.Read(path)
	if err != nil {
		return src, err
	}
	if src.Mode == "" {
		src.Mode = bi.ModeLive
	}
	src.Name = strings.TrimSpace(src.Name)
	if err := src.Validate(); err != nil {
		return src, err
	}
	if err := m.biJoinColumns(ctx, &src); err != nil {
		return src, err
	}
	if src.Fields, err = m.BIFields(ctx, w, src); err != nil {
		return src, err
	}
	// Custom fields are typed against the fields just read.
	if err := src.Validate(); err != nil {
		return src, err
	}
	if err := w.CheckRefs(src); err != nil {
		return src, err
	}
	src.TypeCustom()
	if src.Timezone, err = canonicalZone(src.Timezone); err != nil {
		return src, err
	}
	put := func() error {
		_, err := lib.Update(path, func(w *bi.Workspace) error {
			saved, err := w.PutSource(src)
			src = saved
			return err
		})
		return err
	}
	old := w.Source(src.ID)
	if src.Mode == bi.ModeArchive {
		if old != nil && old.RefreshedAt != "" && sameDefinition(*old, src) {
			src.RefreshedAt, src.ArchiveRows = old.RefreshedAt, old.ArchiveRows
		} else {
			// The snapshot is filed under the source's ID, so a new source is stored first.
			if src.ID == "" {
				if err := put(); err != nil {
					return src, err
				}
			}
			if src, err = m.BIArchive(ctx, w, src); err != nil {
				return src, err
			}
		}
	}
	return src, put()
}

func sameDefinition(a, b bi.DataSource) bool {
	return a.ConnID == b.ConnID && a.Database == b.Database && a.Schema == b.Schema && a.Table == b.Table && a.SQL == b.SQL
}

// BIRefreshSource reads an archive data source into a new snapshot.
func (m *Manager) BIRefreshSource(ctx context.Context, path, id string) (bi.DataSource, error) {
	lib := m.Store.BI()
	if !lib.Contains(path) {
		return bi.DataSource{}, errors.New("not a workspace in Chloe's library")
	}
	w, err := bi.Read(path)
	if err != nil {
		return bi.DataSource{}, err
	}
	src := w.Source(id)
	if src == nil {
		return bi.DataSource{}, errors.New("no such data source")
	}
	if src.Mode != bi.ModeArchive {
		return *src, nil
	}
	fresh, err := m.BIArchive(ctx, w, *src)
	if err != nil {
		return *src, err
	}
	_, err = lib.Update(path, func(w *bi.Workspace) error {
		cur := w.Source(id)
		if cur == nil {
			return errors.New("the data source was deleted while it was read")
		}
		cur.Fields, cur.ArchiveRows, cur.RefreshedAt = fresh.Fields, fresh.ArchiveRows, fresh.RefreshedAt
		fresh = *cur
		return nil
	})
	return fresh, err
}

// canonicalZone trims a time zone name and checks it.
func canonicalZone(tz string) (string, error) {
	tz = strings.TrimSpace(tz)
	return tz, bi.CheckTimezone(tz)
}
