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
	"chloe/internal/safefile"

	// Archives are SQLite; the driver registers chloe_tz for data source time zones.
	_ "chloe/internal/driver/sqlite"
	"chloe/internal/model"
)

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
	n, err := m.biCopyRows(ctx, src, fields, db, MaxArchiveRows, fmt.Errorf("the data source has more than %d rows; filter it in its query, or use live mode", MaxArchiveRows))
	if err != nil {
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
	if err := safefile.Write(p, sealed); err != nil {
		return src, err
	}
	archives.drop(p)
	// The stored definition comes back, not the copy with parameter values filled in.
	stored.Fields, stored.ArchiveRows, stored.RefreshedAt = fields, n, time.Now().UTC().Format(time.RFC3339)
	return stored, nil
}

// biCopyRows reads a live source's rows into a table of an in-memory database,
// typed by fields; more than limit rows fails with over.
func (m *Manager) biCopyRows(ctx context.Context, src bi.DataSource, fields []bi.Field, db *sql.DB, limit int64, over error) (int64, error) {
	cols := make([]string, len(fields))
	for i, f := range fields {
		affinity := "TEXT"
		if f.Type == bi.TypeNumber {
			affinity = "NUMERIC"
		}
		cols[i] = bi.SQLite.QuoteIdent(f.Name) + " " + affinity
	}
	if _, err := db.ExecContext(ctx, "CREATE TABLE "+bi.SQLite.QuoteIdent(bi.ArchiveTable)+" ("+strings.Join(cols, ", ")+")"); err != nil {
		return 0, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ph := strings.TrimSuffix(strings.Repeat("?, ", len(fields)), ", ")
	ins, err := tx.PrepareContext(ctx, "INSERT INTO "+bi.SQLite.QuoteIdent(bi.ArchiveTable)+" VALUES ("+ph+")")
	if err != nil {
		return 0, err
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
			if n >= limit {
				_ = ds.Cancel(context.Background())
				return over
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
		return 0, err
	}
	return n, tx.Commit()
}

// biArchiveQuery runs a query on a data source's archive snapshot.
func (m *Manager) biArchiveQuery(ctx context.Context, w bi.Workspace, src bi.DataSource, q bi.Stmt, limit int) ([]model.ResultColumn, [][]*string, bool, error) {
	db, release, err := m.archiveDB(w, src)
	if err != nil {
		return nil, nil, false, err
	}
	defer release()
	return memQuery(ctx, db, q, limit)
}

// memQuery runs a query on an in-memory copy of a source (archive or cached).
func memQuery(ctx context.Context, db *sql.DB, q bi.Stmt, limit int) ([]model.ResultColumn, [][]*string, bool, error) {
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
// A snapshot replaced or evicted while a read still uses it is closed when that
// read releases it.
var archives = &archiveCache{dbs: map[string]*cachedArchive{}}

type archiveCache struct {
	mu  sync.Mutex
	dbs map[string]*cachedArchive
}

type cachedArchive struct {
	db      *sql.DB
	mod     time.Time
	used    time.Time
	refs    int  // reads using db
	retired bool // no longer in dbs: close once refs reaches 0
}

const maxCachedArchives = 8

// retire takes a out of the cache, closing it unless a read still holds it. c.mu is held.
func (c *archiveCache) retire(p string) {
	a := c.dbs[p]
	if a == nil {
		return
	}
	delete(c.dbs, p)
	a.retired = true
	if a.refs == 0 {
		a.db.Close()
	}
}

func (c *archiveCache) drop(p string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retire(p)
}

// hold returns a's database and a func that gives it back. c.mu is held.
func (c *archiveCache) hold(a *cachedArchive) (*sql.DB, func()) {
	a.refs++
	a.used = time.Now()
	return a.db, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if a.refs--; a.refs == 0 && a.retired {
			a.db.Close()
		}
	}
}

// archiveDB opens a source's snapshot. The caller calls release once it's done with the database.
func (m *Manager) archiveDB(w bi.Workspace, src bi.DataSource) (db *sql.DB, release func(), err error) {
	p := m.Store.BI().ArchivePath(w.ID, src.ID)
	info, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("data source %q has no archive yet; refresh its data", src.Name)
	}
	if err != nil {
		return nil, nil, err
	}
	archives.mu.Lock()
	if a := archives.dbs[p]; a != nil && a.mod.Equal(info.ModTime()) {
		db, release := archives.hold(a)
		archives.mu.Unlock()
		return db, release, nil
	}
	archives.mu.Unlock()

	// Load without the lock, so other snapshots stay readable meanwhile.
	db, err = m.loadArchive(p, src)
	if err != nil {
		return nil, nil, err
	}
	archives.mu.Lock()
	defer archives.mu.Unlock()
	if a := archives.dbs[p]; a != nil && !a.mod.Before(info.ModTime()) {
		// Another read loaded it first.
		db.Close()
		db, release := archives.hold(a)
		return db, release, nil
	}
	archives.retire(p)
	a := &cachedArchive{db: db, mod: info.ModTime()}
	archives.dbs[p] = a
	db, release = archives.hold(a)
	for len(archives.dbs) > maxCachedArchives {
		var oldest string
		for k, a := range archives.dbs {
			if oldest == "" || a.used.Before(archives.dbs[oldest].used) {
				oldest = k
			}
		}
		archives.retire(oldest)
	}
	return db, release, nil
}

// loadArchive reads, decrypts and unpacks a snapshot file into a new in-memory database.
func (m *Manager) loadArchive(p string, src bi.DataSource) (*sql.DB, error) {
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
