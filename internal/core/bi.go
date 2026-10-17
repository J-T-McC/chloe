package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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
	return m.withBISession(ctx, src.ConnID, src.Database, src.Schema, func(ds driver.Session, d driver.Dialect) error {
		if err := m.checkZone(ctx, ds, d, src.ConnID, src.Timezone); err != nil {
			return err
		}
		return fn(ctx, ds, d)
	})
}

// zoneCheckTTL is how long a MySQL server is taken to still know a time zone it knew.
const zoneCheckTTL = 10 * time.Minute

// checkZone makes sure the server knows a data source's time zone: MySQL's
// CONVERT_TZ quietly returns NULL when its time zone tables aren't loaded. A
// zone that passed is not asked about again for zoneCheckTTL.
func (m *Manager) checkZone(ctx context.Context, ds driver.Session, d driver.Dialect, connID, tz string) error {
	if tz == "" || d.Name != "mysql" {
		return nil
	}
	key := connID + "\x00" + tz
	if at, ok := m.biZones.Load(key); ok && time.Since(at.(time.Time)) < zoneCheckTTL {
		return nil
	}
	_, rows, _, err := readStmt(ctx, ds, bi.Stmt{SQL: "SELECT CONVERT_TZ('2000-01-01 00:00:00', '+00:00', ?)", Args: []any{tz}}, 1)
	if err != nil {
		return err
	}
	if len(rows) == 0 || rows[0][0] == nil {
		m.biZones.Delete(key)
		return fmt.Errorf("MySQL doesn't know the time zone %q: load its time zone tables (mysql_tzinfo_to_sql /usr/share/zoneinfo | mysql -u root mysql), or clear the data source's time zone", tz)
	}
	m.biZones.Store(key, time.Now())
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

// BIRead says how fresh a chart's live data must be.
type BIRead struct {
	// Since takes a cached result only if its query ran at or after this time;
	// the zero time takes any result younger than BICacheTTL.
	Since time.Time
	// Uncached runs the chart's queries whatever the cache holds.
	Uncached bool
}

// BIChartData reads what a chart draws. A chart whose shelves aren't filled yet
// comes back with Complete false and no rows.
//
// params override parameter values for this read (by name); the values used come
// back in Data.Params for titles and text. A live chart's result may be one read
// recently (see cachedResult), as opt allows.
func (m *Manager) BIChartData(ctx context.Context, w bi.Workspace, c bi.Chart, params map[string]string, opt BIRead) (bi.Data, error) {
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
	read := func(d driver.Dialect, from bi.Source, query func(bi.Chart, int, bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error)) error {
		complete, err := c.Check(*src, d.Name)
		if err != nil || !complete {
			return err
		}
		// One row past the limit tells the reader the chart was cut off.
		q, cols, err := bi.ChartQuery(d, from, *src, c, bi.MaxChartRows+1)
		if err != nil {
			return err
		}
		out.SQL = q.SQL
		_, raw, truncated, err := query(c, bi.MaxChartRows, q)
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
			_, praw, _, err := query(pc, 1, pq)
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
		err := read(bi.SQLite, bi.Table(bi.SQLite.QuoteIdent(bi.ArchiveTable)), func(_ bi.Chart, _ int, q bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error) {
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
	if opt.Uncached {
		err = m.biLive(ctx, *src, func(ctx context.Context, ds driver.Session, d driver.Dialect) error {
			from, err := bi.From(d, *src)
			if err != nil {
				return err
			}
			return read(d, from, func(_ bi.Chart, _ int, q bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error) {
				return readStmt(ctx, ds, q, bi.MaxChartRows)
			})
		})
		return out, err
	}
	// Each of the chart's statements comes from the result cache, or runs on one
	// of the connection's BI sessions.
	if err := m.Ensure(ctx, src.ConnID); err != nil {
		return out, err
	}
	conn, err := m.conn(src.ConnID)
	if err != nil {
		return out, err
	}
	d := conn.Dialect()
	from, err := bi.From(d, *src)
	if err != nil {
		return out, err
	}
	var first *resultEntry
	var hit bool
	err = read(d, from, func(qc bi.Chart, limit int, q bi.Stmt) ([]model.ResultColumn, [][]*string, bool, error) {
		e, cached, err := m.cachedResult(ctx, *src, q, bi.MaxChartRows, opt.Since, func(ctx context.Context) ([]model.ResultColumn, [][]*string, bool, error) {
			// Charts drawn together on this source may share one read of it.
			return m.biBatched(ctx, d, *src, from, qc, q, limit)
		})
		if err != nil {
			return nil, nil, false, err
		}
		if first == nil {
			first, hit = e, cached
		}
		return e.cols, e.rows, e.truncated, nil
	})
	if err == nil && out.Complete && first != nil {
		out.RanAt = first.started.UTC().Format(time.RFC3339)
		out.Cached = hit
	}
	return out, err
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
