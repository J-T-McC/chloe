package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"chloe/internal/driver"
	"chloe/internal/model"
)

// PasteTarget is the database or schema a paste creates its copies in.
type PasteTarget struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
}

// PasteResult is one copied table's outcome.
type PasteResult struct {
	Source  model.ObjectRef `json:"source"`
	Name    string          `json:"name"` // the copy's name, also set when it failed
	Error   string          `json:"error,omitempty"`
	Created bool            `json:"created"` // the copy exists; with Error: created without its rows
}

// PasteTables copies each table into target as <table>_copy_<date>_<time>, named
// for at. A table that fails doesn't stop the rest: its result carries the error.
func (m *Manager) PasteTables(ctx context.Context, connID string, tables []model.ObjectRef, target PasteTarget, at time.Time) ([]PasteResult, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, errors.New("nothing to paste")
	}
	for _, t := range tables {
		if t.Kind != model.KindTable {
			return nil, errors.New("only tables can be pasted")
		}
	}
	d := c.Dialect()
	switch d.Name {
	case "mysql", "sqlite":
	case "postgres":
		if target.Schema == "" {
			return nil, errors.New("choose a schema to paste into")
		}
		return nil, errors.New("PostgreSQL paste isn't built yet")
	default:
		return nil, errors.New("paste works on MySQL, PostgreSQL and SQLite tables")
	}
	taken, err := pasteNames(ctx, c, target)
	if err != nil {
		return nil, fmt.Errorf("reading the names in %s: %w", target.Database, err)
	}
	limit := identLimits[d.Name]
	out := make([]PasteResult, 0, len(tables))
	for _, src := range tables {
		name := copyName(src.Name, at, limit, func(n string) bool { return taken[strings.ToLower(n)] })
		taken[strings.ToLower(name)] = true
		r := PasteResult{Source: src, Name: name}
		if err := m.pasteTable(ctx, connID, c, src, target, name); err != nil {
			r.Error = err.Error()
			if now, err := pasteNames(ctx, c, target); err == nil {
				r.Created = now[strings.ToLower(name)]
			}
		} else {
			r.Created = true
		}
		out = append(out, r)
	}
	return out, nil
}

// pasteTable creates one copy, with its rows.
func (m *Manager) pasteTable(ctx context.Context, connID string, c driver.Conn, src model.ObjectRef, target PasteTarget, name string) error {
	d := c.Dialect()
	var stmts []string
	switch d.Name {
	case "mysql":
		t, err := c.LoadDesign(ctx, src)
		if err != nil {
			return err
		}
		var cols []string
		for _, col := range t.Columns {
			if col.GeneratedExpr == "" {
				cols = append(cols, d.QuoteIdent(col.Name))
			}
		}
		list := strings.Join(cols, ", ")
		from, to := d.Qualified(src), d.Qualified(model.ObjectRef{Database: target.Database, Name: name})
		stmts = []string{
			fmt.Sprintf("CREATE TABLE %s LIKE %s", to, from),
			fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", to, list, list, from),
		}
	case "sqlite":
		var err error
		if stmts, err = sqliteCopySQL(c, src, target.Database, name, true); err != nil {
			return err
		}
	}
	if res := m.ExecuteDDL(ctx, connID, target.Database, target.Schema, stmts, d.TransactionalDDL()); res.Error != "" {
		return errors.New(res.Error)
	}
	return nil
}

// pasteNames is every name in the target's namespace, lower-cased: MySQL on
// macOS and SQLite fold case, so a name differing only in case counts as taken.
func pasteNames(ctx context.Context, c driver.Conn, target PasteTarget) (map[string]bool, error) {
	ds, err := c.NewSession(ctx, target.Database, target.Schema)
	if err != nil {
		return nil, err
	}
	defer ds.Close()
	var rows driver.Rows
	if c.Dialect().Name == "sqlite" {
		rows, err = ds.Query(ctx, "SELECT name FROM "+sqliteIdent(target.Database)+".sqlite_schema")
	} else {
		rows, err = ds.Query(ctx, "SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = ?", target.Database)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	cell := make([]*string, 1)
	for rows.Next(cell) {
		out[strings.ToLower(str(cell[0]))] = true
	}
	return out, rows.Err()
}

// identLimit is the longest identifier an engine keeps, counted in bytes or in
// characters. The zero value means no limit.
type identLimit struct {
	max   int
	bytes bool
}

// identLimits is keyed on Dialect.Name; MariaDB uses the mysql dialect. Postgres
// silently truncates past 63 bytes, so a longer name must never reach it.
var identLimits = map[string]identLimit{
	"mysql":    {max: 64},
	"postgres": {max: 63, bytes: true},
}

// copyName is the name a pasted table gets: <table>_copy_YYYYMMDD_HHMMSS in
// local time, then _2, _3… until taken reports it free. Only the <table> part
// is cut to fit the limit, so every candidate keeps its whole suffix.
func copyName(table string, at time.Time, limit identLimit, taken func(string) bool) string {
	suffix := "_copy_" + at.Local().Format("20060102_150405")
	for n := 1; ; n++ {
		s := suffix
		if n > 1 {
			s += "_" + strconv.Itoa(n)
		}
		if name := limit.fit(table, s); !taken(name) {
			return name
		}
	}
}

// fit returns base+suffix, with base cut on a rune boundary when the whole
// would pass the limit.
func (l identLimit) fit(base, suffix string) string {
	if l.max == 0 {
		return base + suffix
	}
	room := l.max - l.size(suffix)
	used := 0
	for i := 0; i < len(base); {
		_, w := utf8.DecodeRuneInString(base[i:])
		unit := 1
		if l.bytes {
			unit = w
		}
		if used+unit > room {
			return base[:i] + suffix
		}
		used += unit
		i += w
	}
	return base + suffix
}

func (l identLimit) size(s string) int {
	if l.bytes {
		return len(s)
	}
	return utf8.RuneCountInString(s)
}
