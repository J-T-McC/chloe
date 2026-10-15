// Package sqlite implements the SQLite driver on modernc.org/sqlite (pure Go).
//
// A connection is a database file opened as "main", plus files attached under
// other names. Attachments are per physical connection in SQLite, so every pooled
// connection and every editor session attaches the current set before use.
package sqlite

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gosqlite "modernc.org/sqlite"

	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
	"chloe/internal/tunnel"
)

func init() { driver.Register(model.SQLite, Driver{}) }

type Driver struct{}

var dialect = driver.Dialect{Name: "sqlite", IdentQuote: `"`, Split: sqlsplit.SQLite}

// ErrUnsupported is returned for server features SQLite doesn't have.
var ErrUnsupported = errors.New("not available for SQLite")

// uri builds a SQLite URI filename; mode is ro, rw or rwc.
func uri(path, mode string) string {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
	return "file:" + esc + "?mode=" + mode
}

func (Driver) Open(ctx context.Context, c model.Connection, _ tunnel.Dialer) (driver.Conn, error) {
	path := strings.TrimSpace(c.SQLite.Path)
	if path == "" {
		return nil, errors.New("choose a database file")
	}
	path = expandHome(path)
	mode := "rw"
	if c.SQLite.Create {
		mode = "rwc"
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("database file: %w", err)
	}
	conn := &Conn{path: path, initial: c.Advanced.InitialQueries}
	for _, a := range c.SQLite.Attached {
		conn.attached = append(conn.attached, model.AttachedDB{Name: a.Name, Path: expandHome(a.Path)})
	}
	timeout := c.Advanced.ConnectTimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	dsn := uri(path, mode) + "&_pragma=foreign_keys(1)&_pragma=busy_timeout(" + strconv.Itoa(timeout*1000) + ")"
	inner, err := gosqlite.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	conn.db = sql.OpenDB(&connector{Connector: inner, c: conn})
	conn.stopKeepalive = sqlcommon.ConfigurePool(conn.db, c.Advanced)
	if err := conn.db.PingContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&conn.version)
	return conn, nil
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

type Conn struct {
	db            *sql.DB
	stopKeepalive func()
	path          string
	initial       string
	version       string

	mu       sync.Mutex
	attached []model.AttachedDB
	// gen counts attachment changes; connections compare it to know when to re-sync.
	gen atomic.Int64
}

func (c *Conn) attachments() (int64, []model.AttachedDB) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gen.Load(), slices.Clone(c.attached)
}

func (c *Conn) Close() error {
	if c.stopKeepalive != nil {
		c.stopKeepalive()
	}
	return c.db.Close()
}

func (c *Conn) CloseDatabase(string)           {}
func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) ServerVersion() string          { return c.version }
func (c *Conn) Dialect() driver.Dialect        { return dialect }
func (c *Conn) BuiltinFunctions() []string     { return builtinFunctions }
func (c *Conn) OpenConnections() int           { return c.db.Stats().OpenConnections }

// Path is the main database file.
func (c *Conn) Path() string { return c.path }

// ---- attached databases -------------------------------------------------------

// Attach attaches a database file under name, creating the file if create is set.
func (c *Conn) Attach(ctx context.Context, name, path string, create bool) error {
	name, path = strings.TrimSpace(name), expandHome(strings.TrimSpace(path))
	if name == "" || path == "" {
		return errors.New("enter a database name and file")
	}
	if strings.EqualFold(name, "main") || strings.EqualFold(name, "temp") {
		return fmt.Errorf("%q is reserved", name)
	}
	mode := "rw"
	if create {
		mode = "rwc"
	} else if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("database file: %w", err)
	}
	// Attach on a pooled connection first: it reports a bad file or name before
	// the attachment is recorded.
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	_, err = conn.ExecContext(ctx, "ATTACH DATABASE "+literal(uri(path, mode))+" AS "+dialect.QuoteIdent(name))
	sqlcommon.DiscardConn(conn)
	if err != nil {
		return err
	}
	c.mu.Lock()
	for _, a := range c.attached {
		if strings.EqualFold(a.Name, name) {
			c.mu.Unlock()
			return fmt.Errorf("a database named %q is already attached", name)
		}
	}
	c.attached = append(c.attached, model.AttachedDB{Name: name, Path: path})
	c.gen.Add(1)
	c.mu.Unlock()
	c.flushIdle()
	return nil
}

// Detach removes an attached database.
func (c *Conn) Detach(_ context.Context, name string) error {
	c.mu.Lock()
	i := slices.IndexFunc(c.attached, func(a model.AttachedDB) bool { return a.Name == name })
	if i < 0 {
		c.mu.Unlock()
		if name == "main" {
			return errors.New("the main database can't be detached")
		}
		return fmt.Errorf("%q isn't attached", name)
	}
	c.attached = slices.Delete(c.attached, i, i+1)
	c.gen.Add(1)
	c.mu.Unlock()
	c.flushIdle()
	return nil
}

// Attached lists the attached databases.
func (c *Conn) Attached() []model.AttachedDB {
	_, a := c.attachments()
	return a
}

// flushIdle closes idle pooled connections so new ones attach the current set.
func (c *Conn) flushIdle() {
	c.db.SetMaxIdleConns(0)
	c.db.SetMaxIdleConns(4)
}

func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// syncAttachments brings one physical connection's attachments up to date.
func (c *Conn) syncAttachments(ctx context.Context, q interface {
	ExecContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Result, error)
	QueryContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error)
}, want []model.AttachedDB) error {
	rows, err := q.QueryContext(ctx, "SELECT name FROM pragma_database_list", nil)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	dest := make([]sqldriver.Value, 1)
	for rows.Next(dest) == nil {
		if n, ok := dest[0].(string); ok && n != "main" && n != "temp" {
			have[n] = true
		}
	}
	rows.Close()
	keep := map[string]bool{}
	for _, a := range want {
		keep[a.Name] = true
		if !have[a.Name] {
			if _, err := q.ExecContext(ctx, "ATTACH DATABASE "+literal(uri(a.Path, "rw"))+" AS "+dialect.QuoteIdent(a.Name), nil); err != nil {
				return fmt.Errorf("attach %s: %w", a.Name, err)
			}
		}
	}
	for n := range have {
		if !keep[n] {
			if _, err := q.ExecContext(ctx, "DETACH DATABASE "+dialect.QuoteIdent(n), nil); err != nil {
				return fmt.Errorf("detach %s: %w", n, err)
			}
		}
	}
	return nil
}

// connector attaches the connection's databases to every new physical connection,
// and re-syncs pooled ones when the set changed.
type connector struct {
	sqldriver.Connector
	c *Conn
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Result, error)
	QueryContext(ctx context.Context, query string, args []sqldriver.NamedValue) (sqldriver.Rows, error)
}

func (k *connector) Connect(ctx context.Context) (sqldriver.Conn, error) {
	dc, err := k.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	eq, ok := dc.(execQuerier)
	if !ok {
		dc.Close()
		return nil, errors.New("sqlite: connection can't execute statements")
	}
	gen, want := k.c.attachments()
	if len(want) > 0 {
		if err := k.c.syncAttachments(ctx, eq, want); err != nil {
			dc.Close()
			return nil, err
		}
	}
	return &physConn{Conn: dc, eq: eq, c: k.c, gen: gen}, nil
}

// physConn wraps a driver connection to re-sync attachments when it's reused.
type physConn struct {
	sqldriver.Conn
	eq  execQuerier
	c   *Conn
	gen int64
}

func (p *physConn) sync(ctx context.Context) error {
	gen, want := p.c.attachments()
	if gen == p.gen {
		return nil
	}
	if err := p.c.syncAttachments(ctx, p.eq, want); err != nil {
		return err
	}
	p.gen = gen
	return nil
}

func (p *physConn) ResetSession(ctx context.Context) error {
	if r, ok := p.Conn.(sqldriver.SessionResetter); ok {
		if err := r.ResetSession(ctx); err != nil {
			return err
		}
	}
	if err := p.sync(ctx); err != nil {
		return sqldriver.ErrBadConn
	}
	return nil
}

func (p *physConn) IsValid() bool {
	if v, ok := p.Conn.(sqldriver.Validator); ok {
		return v.IsValid()
	}
	return true
}

func (p *physConn) ExecContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Result, error) {
	return p.eq.ExecContext(ctx, q, args)
}

func (p *physConn) QueryContext(ctx context.Context, q string, args []sqldriver.NamedValue) (sqldriver.Rows, error) {
	return p.eq.QueryContext(ctx, q, args)
}

func (p *physConn) PrepareContext(ctx context.Context, q string) (sqldriver.Stmt, error) {
	if pc, ok := p.Conn.(sqldriver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, q)
	}
	return p.Conn.Prepare(q)
}

func (p *physConn) BeginTx(ctx context.Context, opts sqldriver.TxOptions) (sqldriver.Tx, error) {
	if b, ok := p.Conn.(sqldriver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return p.Conn.Begin() //nolint:staticcheck // fallback for drivers without BeginTx
}

func (p *physConn) Ping(ctx context.Context) error {
	if pg, ok := p.Conn.(sqldriver.Pinger); ok {
		return pg.Ping(ctx)
	}
	return nil
}

// ---- metadata -------------------------------------------------------------------

func (c *Conn) ListDatabases(context.Context) ([]string, error) {
	out := []string{"main"}
	for _, a := range c.Attached() {
		out = append(out, a.Name)
	}
	return out, nil
}

func (c *Conn) ListSchemas(context.Context, string) ([]string, error) { return nil, nil }

// master is the schema table of one database.
func master(db string) string {
	if db == "" {
		db = "main"
	}
	return dialect.QuoteIdent(db) + ".sqlite_master"
}

func (c *Conn) ListObjects(ctx context.Context, db, _ string, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	var typ string
	switch kind {
	case model.KindTable:
		typ = "table"
	case model.KindView:
		typ = "view"
	case model.KindTrigger:
		typ = "trigger"
	case model.KindIndex:
		typ = "index"
	default:
		return nil, nil
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT name, tbl_name, IFNULL(sql, '') FROM `+master(db)+
		` WHERE type = ? AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`, typ)
	if err != nil {
		return nil, err
	}
	out := make([]model.ObjectInfo, 0, len(recs))
	for _, r := range recs {
		o := model.ObjectInfo{ObjectRef: model.ObjectRef{Database: db, Name: r[0].String, Kind: kind}}
		switch kind {
		case model.KindTable:
			if strings.Contains(strings.ToUpper(r[2].String), "VIRTUAL TABLE") {
				o.Engine = "virtual"
			}
		case model.KindTrigger, model.KindIndex:
			o.Comment = "ON " + r[1].String
		}
		out = append(out, o)
	}
	if kind == model.KindTable {
		// Row counts are exact (SQLite keeps no statistics); skip them for big files.
		if st, err := os.Stat(c.fileOf(db)); err == nil && st.Size() < 64<<20 {
			for i := range out {
				var n int64
				if err := c.db.QueryRowContext(ctx, "SELECT count(*) FROM "+dialect.Qualified(out[i].ObjectRef)).Scan(&n); err == nil {
					out[i].Rows = n
				}
			}
		}
	}
	return out, nil
}

// fileOf returns the file behind a database name.
func (c *Conn) fileOf(db string) string {
	if db == "" || db == "main" {
		return c.path
	}
	for _, a := range c.Attached() {
		if a.Name == db {
			return a.Path
		}
	}
	return ""
}

type colInfo struct {
	name, typ string
	notNull   bool
	def       sql.NullString
	pk        int
	hidden    int // 2: virtual generated, 3: stored generated
}

func (c *Conn) columns(ctx context.Context, db, table string) ([]colInfo, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT name, type, "notnull", dflt_value, pk, hidden FROM pragma_table_xinfo(?, ?) ORDER BY cid`, table, dbName(db))
	if err != nil {
		return nil, err
	}
	out := make([]colInfo, len(recs))
	for i, r := range recs {
		pk, _ := strconv.Atoi(r[4].String)
		hidden, _ := strconv.Atoi(r[5].String)
		out[i] = colInfo{name: r[0].String, typ: r[1].String, notNull: r[2].String == "1", def: r[3], pk: pk, hidden: hidden}
	}
	return out, nil
}

func dbName(db string) string {
	if db == "" {
		return "main"
	}
	return db
}

// baseType is the declared type without its modifiers, lower-cased.
func baseType(t string) string {
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	return strings.ToLower(strings.TrimSpace(t))
}

// rowidAlias reports whether the table's only primary key column is an INTEGER
// PRIMARY KEY, which aliases the rowid and is assigned automatically.
func rowidAlias(cols []colInfo) string {
	alias := ""
	for _, c := range cols {
		if c.pk > 0 {
			if alias != "" || !strings.EqualFold(strings.TrimSpace(c.typ), "integer") {
				return ""
			}
			alias = c.name
		}
	}
	return alias
}

type fkInfo struct {
	id                int
	table, onUpdate   string
	onDelete          string
	cols, refCols     []string
	refColsUnresolved bool
}

func (c *Conn) foreignKeys(ctx context.Context, db, table string) ([]fkInfo, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT id, "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?, ?) ORDER BY id, seq`, table, dbName(db))
	if err != nil {
		return nil, err
	}
	var out []fkInfo
	for _, r := range recs {
		id, _ := strconv.Atoi(r[0].String)
		if n := len(out); n == 0 || out[n-1].id != id {
			out = append(out, fkInfo{id: id, table: r[1].String, onUpdate: r[4].String, onDelete: r[5].String})
		}
		f := &out[len(out)-1]
		f.cols = append(f.cols, r[2].String)
		f.refCols = append(f.refCols, r[3].String)
		if !r[3].Valid || r[3].String == "" {
			f.refColsUnresolved = true
		}
	}
	// REFERENCES parent without columns means the parent's primary key.
	for i := range out {
		if !out[i].refColsUnresolved {
			continue
		}
		pcols, err := c.columns(ctx, db, out[i].table)
		if err != nil {
			continue
		}
		var pk []string
		for _, pc := range pcols {
			if pc.pk > 0 {
				pk = append(pk, pc.name)
			}
		}
		if len(pk) == len(out[i].cols) {
			out[i].refCols = pk
		}
	}
	return out, nil
}

func (c *Conn) TableDetail(ctx context.Context, ref model.ObjectRef) (model.TableDetail, error) {
	td := model.TableDetail{ObjectRef: ref}
	cols, err := c.columns(ctx, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	if len(cols) == 0 {
		return td, fmt.Errorf("%s not found", ref.Name)
	}
	alias := rowidAlias(cols)
	var pkCols []string
	for i, col := range cols {
		mc := model.Column{Name: col.name, Type: col.typ, DataType: baseType(col.typ), Nullable: !col.notNull && col.pk == 0,
			PrimaryKey: col.pk > 0, AutoIncrement: col.name == alias, Position: i + 1}
		if col.def.Valid {
			v := col.def.String
			mc.Default = &v
		}
		if col.pk > 0 {
			pkCols = append(pkCols, col.name)
		}
		td.Columns = append(td.Columns, mc)
	}
	if len(pkCols) > 0 {
		td.Indexes = append(td.Indexes, model.Index{Name: "PRIMARY", Columns: pkCols, Unique: true, Primary: true})
	}
	idx, err := c.indexes(ctx, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	for _, ix := range idx {
		if ix.origin == "pk" {
			continue
		}
		td.Indexes = append(td.Indexes, model.Index{Name: ix.name, Columns: ix.cols, Unique: ix.unique})
	}
	fks, err := c.foreignKeys(ctx, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	for _, f := range fks {
		td.ForeignKeys = append(td.ForeignKeys, model.ForeignKey{Name: fkName(ref.Name, f.id), Columns: f.cols, RefDatabase: ref.Database,
			RefTable: f.table, RefColumns: f.refCols, OnUpdate: f.onUpdate, OnDelete: f.onDelete})
	}
	return td, nil
}

// fkName names an unnamed foreign key (SQLite doesn't report constraint names).
func fkName(table string, id int) string { return fmt.Sprintf("fk_%s_%d", table, id) }

type indexInfo struct {
	name, origin string
	unique       bool
	cols         []string
}

func (c *Conn) indexes(ctx context.Context, db, table string) ([]indexInfo, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT l.name, l."unique", l.origin, IFNULL(i.name, '') FROM pragma_index_list(?, ?) l
		JOIN pragma_index_info(l.name, ?) i ORDER BY l.seq DESC, i.seqno`, table, dbName(db), dbName(db))
	if err != nil {
		return nil, err
	}
	var out []indexInfo
	for _, r := range recs {
		if n := len(out); n == 0 || out[n-1].name != r[0].String {
			out = append(out, indexInfo{name: r[0].String, unique: r[1].String == "1", origin: r[2].String})
		}
		out[len(out)-1].cols = append(out[len(out)-1].cols, r[3].String)
	}
	return out, nil
}

func (c *Conn) Catalog(ctx context.Context, db string) (model.Catalog, error) {
	cat := model.Catalog{Database: db}
	for _, k := range []model.ObjectKind{model.KindTable, model.KindView} {
		objs, err := c.ListObjects(ctx, db, "", k)
		if err != nil {
			return cat, err
		}
		cat.Objects = append(cat.Objects, objs...)
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT m.name, p.name, p.type, p.pk, p."notnull" FROM `+master(db)+` m
		JOIN pragma_table_info(m.name, ?) p WHERE m.type IN ('table', 'view') AND m.name NOT LIKE 'sqlite\_%' ESCAPE '\'
		ORDER BY m.name, p.cid`, dbName(db))
	if err != nil {
		return cat, err
	}
	for _, r := range recs {
		cat.Columns = append(cat.Columns, model.CatalogColumn{Table: r[0].String, Name: r[1].String, Type: r[2].String,
			PK: r[3].String != "0", Nullable: r[4].String == "0"})
	}
	for _, o := range cat.Objects {
		if o.Kind != model.KindTable {
			continue
		}
		fks, err := c.foreignKeys(ctx, db, o.Name)
		if err != nil {
			continue
		}
		for _, f := range fks {
			cat.FKs = append(cat.FKs, model.CatalogFK{Table: o.Name, Columns: f.cols, RefSchema: db, RefTable: f.table, RefColumns: f.refCols})
		}
	}
	return cat, nil
}

// objectSQL returns an object's CREATE statement as stored in the schema table.
func (c *Conn) objectSQL(ctx context.Context, db, typ, name string) (string, error) {
	var s sql.NullString
	err := c.db.QueryRowContext(ctx, "SELECT sql FROM "+master(db)+" WHERE type = ? AND name = ?", typ, name).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%s %s not found", typ, name)
	}
	return s.String, err
}

func (c *Conn) DDL(ctx context.Context, ref model.ObjectRef) (string, error) {
	switch ref.Kind {
	case model.KindTable:
		s, err := c.objectSQL(ctx, ref.Database, "table", ref.Name)
		if err != nil {
			return "", err
		}
		out := []string{s + ";"}
		recs, err := sqlcommon.QueryStrings(ctx, c.db, "SELECT sql FROM "+master(ref.Database)+
			" WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL ORDER BY type = 'trigger', name", ref.Name)
		if err != nil {
			return "", err
		}
		for _, r := range recs {
			out = append(out, r[0].String+";")
		}
		return strings.Join(out, "\n\n"), nil
	case model.KindView, model.KindTrigger, model.KindIndex:
		s, err := c.objectSQL(ctx, ref.Database, string(ref.Kind), ref.Name)
		if err != nil {
			return "", err
		}
		return s + ";", nil
	}
	return "", fmt.Errorf("DDL not supported for %s", ref.Kind)
}

// ---- sessions ---------------------------------------------------------------------

func (c *Conn) NewSession(ctx context.Context, _, _ string) (driver.Session, error) {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if err := sqlcommon.RunInitialQueries(ctx, conn, c.initial, sqlsplit.SQLite); err != nil {
		conn.Close()
		return nil, err
	}
	return &Session{c: c, conn: conn}, nil
}

// Session is one dedicated connection. SQLite has no USE: unqualified names
// resolve in main, then temp, then attached databases in attach order.
type Session struct {
	c    *Conn
	conn *sql.Conn

	mu      sync.Mutex
	cancels []context.CancelFunc
}

// run gives statements a context Cancel can interrupt. A statement's rows may
// still be streaming when the next one starts, so earlier contexts stay live.
func (s *Session) run(ctx context.Context) context.Context {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancels = append(s.cancels, cancel)
	s.mu.Unlock()
	return ctx
}

// sync re-attaches databases on the session's connection if the set changed.
func (s *Session) sync(ctx context.Context) error {
	return s.conn.Raw(func(dc any) error {
		if p, ok := dc.(*physConn); ok {
			return p.sync(ctx)
		}
		return nil
	})
}

func (s *Session) Query(ctx context.Context, stmt string, args ...any) (driver.Rows, error) {
	if err := s.sync(ctx); err != nil {
		return nil, err
	}
	return sqlcommon.Run(s.run(ctx), s.conn, stmt, FormatValue, args...)
}

func (s *Session) Exec(ctx context.Context, stmt string, args ...any) (int64, error) {
	if err := s.sync(ctx); err != nil {
		return 0, err
	}
	res, err := s.conn.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Cancel interrupts the running statement (sqlite3_interrupt via its context).
func (s *Session) Cancel(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.cancels {
		c()
	}
	s.cancels = nil
	return nil
}

func (s *Session) Use(context.Context, string, string) error { return nil }

func (s *Session) Close() error {
	s.Cancel(context.Background())
	return sqlcommon.DiscardConn(s.conn)
}

// FormatValue formats SQLite values. The driver parses DATE/DATETIME/TIMESTAMP
// text into times; they are shown in SQLite's own text format.
func FormatValue(v any, typeName string) string {
	switch x := v.(type) {
	case []byte:
		return "0x" + strings.ToUpper(fmt.Sprintf("%x", x))
	case time.Time:
		if typeName == "DATE" && x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0 {
			return x.Format("2006-01-02")
		}
		s := x.Format("2006-01-02 15:04:05.999999999")
		if _, off := x.Zone(); off != 0 {
			s += x.Format("-07:00")
		}
		return s
	}
	return sqlcommon.FormatValue(v, typeName)
}

// ---- server features SQLite doesn't have ------------------------------------------

func (c *Conn) Processes(context.Context) ([]model.Process, error) { return nil, ErrUnsupported }
func (c *Conn) KillProcess(context.Context, string, bool) error    { return ErrUnsupported }

// Variables lists the connection's PRAGMA settings.
func (c *Conn) Variables(ctx context.Context) ([]model.ServerVar, error) {
	var out []model.ServerVar
	for _, p := range []string{"application_id", "auto_vacuum", "busy_timeout", "cache_size", "encoding", "foreign_keys",
		"journal_mode", "locking_mode", "page_count", "page_size", "freelist_count", "synchronous", "temp_store", "user_version", "wal_autocheckpoint"} {
		var v sql.NullString
		if err := c.db.QueryRowContext(ctx, "PRAGMA "+p).Scan(&v); err == nil {
			out = append(out, model.ServerVar{Name: p, Value: v.String})
		}
	}
	return out, nil
}

func (c *Conn) Status(context.Context) ([]model.ServerVar, error) { return nil, ErrUnsupported }

var _ driver.Conn = (*Conn)(nil)
