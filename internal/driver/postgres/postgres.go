// Package postgres implements the PostgreSQL driver.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
	"chloe/internal/tunnel"
)

func init() { driver.Register(model.PostgreSQL, Driver{}) }

type Driver struct{}

var dialect = driver.Dialect{Name: "postgres", IdentQuote: `"`, HasSchemas: true, DefaultSchema: "public", Split: sqlsplit.Postgres}

func (Driver) Open(ctx context.Context, c model.Connection, d tunnel.Dialer) (driver.Conn, error) {
	conn := &Conn{profile: c, dialer: d, pools: map[string]*pool{}}
	conn.defaultDB = c.InitialDatabase
	if conn.defaultDB == "" {
		conn.defaultDB = "postgres"
	}
	db, err := conn.pool(ctx, conn.defaultDB)
	if err != nil {
		return nil, err
	}
	_ = db.QueryRowContext(ctx, "SHOW server_version").Scan(&conn.version)
	return conn, nil
}

type Conn struct {
	profile   model.Connection
	dialer    tunnel.Dialer
	defaultDB string
	version   string

	mu    sync.Mutex
	pools map[string]*pool
}

type pool struct {
	db            *sql.DB
	stopKeepalive func()
}

// pool returns the connection pool for a database, opening it on first use.
// PostgreSQL connections are bound to one database, so each needs its own pool.
func (c *Conn) pool(ctx context.Context, database string) (*sql.DB, error) {
	if database == "" {
		database = c.defaultDB
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pools[database]; ok {
		return p.db, nil
	}
	cfg, err := pgx.ParseConfig(c.dsn(database))
	if err != nil {
		return nil, err
	}
	cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return c.dialer.DialContext(ctx, network, addr)
	}
	// Leave name resolution to the dialer: through an SSH tunnel the database host
	// must be resolved on the jump host, not on this machine.
	cfg.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	cfg.RuntimeParams["application_name"] = "Chloe"
	if crl := c.profile.SSL.CRLFile; c.profile.SSL.Enabled && crl != "" {
		if err := addCRLCheck(cfg, crl); err != nil {
			return nil, err
		}
	}
	db := stdlib.OpenDB(*cfg)
	stop := sqlcommon.ConfigurePool(db, c.profile.Advanced)
	if err := db.PingContext(ctx); err != nil {
		stop()
		db.Close()
		return nil, err
	}
	c.pools[database] = &pool{db: db, stopKeepalive: stop}
	return db, nil
}

func (c *Conn) dsn(database string) string {
	p := c.profile
	port := p.Port
	if port == 0 {
		port = 5432
	}
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(p.User, p.Password),
		Host:   net.JoinHostPort(p.Host, strconv.Itoa(port)),
		Path:   "/" + database,
	}
	q := url.Values{}
	// Extra key=value parameters first, so the explicit settings below win.
	for _, line := range strings.Split(p.Advanced.ConnectionParams, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok && strings.TrimSpace(k) != "" {
			q.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	timeout := p.Advanced.ConnectTimeoutSec
	if timeout <= 0 {
		timeout = 15
	}
	q.Set("connect_timeout", strconv.Itoa(timeout))
	// Without "Use SSL", behave like libpq's default: use TLS if the server offers it.
	mode := "prefer"
	if p.SSL.Enabled {
		mode = p.SSL.Mode
		if mode == "" {
			mode = "require"
		}
		if p.SSL.CAFile != "" {
			q.Set("sslrootcert", p.SSL.CAFile)
		}
		if p.SSL.CertFile != "" {
			q.Set("sslcert", p.SSL.CertFile)
		}
		if p.SSL.KeyFile != "" {
			q.Set("sslkey", p.SSL.KeyFile)
		}
	}
	if q.Get("sslmode") == "" || p.SSL.Enabled {
		q.Set("sslmode", mode)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, p := range c.pools {
		p.stopKeepalive()
		p.db.Close()
		delete(c.pools, k)
	}
	return nil
}

// CloseDatabase releases the pool of a non-default database.
func (c *Conn) CloseDatabase(database string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pools[database]; ok && database != c.defaultDB {
		p.stopKeepalive()
		p.db.Close()
		delete(c.pools, database)
	}
}

func (c *Conn) Ping(ctx context.Context) error {
	db, err := c.pool(ctx, "")
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
}

func (c *Conn) ServerVersion() string      { return c.version }
func (c *Conn) Dialect() driver.Dialect    { return dialect }
func (c *Conn) BuiltinFunctions() []string { return builtinFunctions }

func (c *Conn) ListDatabases(ctx context.Context) ([]string, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	return column0(ctx, db, `SELECT datname FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname`)
}

func (c *Conn) ListSchemas(ctx context.Context, database string) ([]string, error) {
	db, err := c.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	return column0(ctx, db, `SELECT nspname FROM pg_namespace
		WHERE nspname NOT LIKE 'pg\_toast%' AND nspname NOT LIKE 'pg\_temp\_%'
		ORDER BY (nspname IN ('pg_catalog','information_schema')), nspname`)
}

func column0(ctx context.Context, db *sql.DB, q string, args ...any) ([]string, error) {
	recs, err := sqlcommon.QueryStrings(ctx, db, q, args...)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r[0].String
	}
	return out, nil
}

// objectQueries return: schema, name, comment, rows, size, owner, signature, return type.
var objectQueries = map[model.ObjectKind]string{
	model.KindTable:     relQuery("'r','p'"),
	model.KindView:      relQuery("'v'"),
	model.KindMatView:   relQuery("'m'"),
	model.KindSequence:  relQuery("'S'"),
	model.KindFunction:  procQuery("'f','a','w'"),
	model.KindProcedure: procQuery("'p'"),
	model.KindTrigger: `SELECT n.nspname, t.tgname, COALESCE(obj_description(t.oid,'pg_trigger'),''), 0, 0, '', c.relname, ''
		FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE NOT t.tgisinternal AND ($1 = '' OR n.nspname = $1) ORDER BY 2`,
	model.KindType: `SELECT n.nspname, t.typname, COALESCE(obj_description(t.oid,'pg_type'),''), 0, 0, pg_get_userbyid(t.typowner),
		CASE t.typtype WHEN 'c' THEN 'composite' WHEN 'e' THEN 'enum' WHEN 'd' THEN 'domain' WHEN 'r' THEN 'range' ELSE t.typtype::text END, ''
		FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		WHERE t.typtype IN ('c','e','d','r') AND (t.typrelid = 0 OR (SELECT c.relkind FROM pg_class c WHERE c.oid = t.typrelid) = 'c')
		  AND NOT EXISTS (SELECT 1 FROM pg_type el WHERE el.oid = t.typelem AND el.typarray = t.oid)
		  AND ($1 = '' OR n.nspname = $1) ORDER BY 2`,
}

func relQuery(kinds string) string {
	return `SELECT n.nspname, c.relname, COALESCE(obj_description(c.oid,'pg_class'),''),
		GREATEST(c.reltuples,0)::bigint, CASE WHEN c.relkind IN ('r','p','m') THEN pg_total_relation_size(c.oid) ELSE 0 END,
		pg_get_userbyid(c.relowner), '', ''
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN (` + kinds + `) AND ($1 = '' OR n.nspname = $1) AND NOT c.relispartition ORDER BY c.relname`
}

func procQuery(kinds string) string {
	return `SELECT n.nspname, p.proname, COALESCE(obj_description(p.oid,'pg_proc'),''), 0, 0, pg_get_userbyid(p.proowner),
		pg_get_function_identity_arguments(p.oid), COALESCE(pg_get_function_result(p.oid),'')
		FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE p.prokind IN (` + kinds + `) AND ($1 = '' OR n.nspname = $1) ORDER BY p.proname, 7`
}

func (c *Conn) ListObjects(ctx context.Context, database, schema string, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	q, ok := objectQueries[kind]
	if !ok {
		return nil, nil
	}
	db, err := c.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, q, schema)
	if err != nil {
		return nil, err
	}
	out := make([]model.ObjectInfo, 0, len(recs))
	for _, r := range recs {
		rows, _ := strconv.ParseInt(r[3].String, 10, 64)
		size, _ := strconv.ParseInt(r[4].String, 10, 64)
		info := model.ObjectInfo{
			ObjectRef: model.ObjectRef{Database: database, Schema: r[0].String, Name: r[1].String, Kind: kind, Signature: r[6].String},
			Comment:   r[2].String, Rows: rows, DataSize: size, Owner: r[5].String, ReturnType: r[7].String,
		}
		if kind == model.KindTrigger || kind == model.KindType {
			info.Signature, info.Engine = "", r[6].String // trigger: table name, type: type category
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *Conn) TableDetail(ctx context.Context, ref model.ObjectRef) (model.TableDetail, error) {
	td := model.TableDetail{ObjectRef: ref}
	db, err := c.pool(ctx, ref.Database)
	if err != nil {
		return td, err
	}
	schema := ref.Schema
	if schema == "" {
		schema = "public"
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, `SELECT a.attname, format_type(a.atttypid, a.atttypmod), t.typname, NOT a.attnotnull,
		pg_get_expr(d.adbin, d.adrelid), COALESCE(pk.is_pk, false), a.attidentity <> '' OR COALESCE(pg_get_expr(d.adbin, d.adrelid) LIKE 'nextval(%', false),
		COALESCE(col_description(c.oid, a.attnum),''), a.attnum
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_type t ON t.oid = a.atttypid
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		LEFT JOIN LATERAL (SELECT true AS is_pk FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey)) pk ON true
		WHERE n.nspname = $1 AND c.relname = $2 AND a.attnum > 0 AND NOT a.attisdropped ORDER BY a.attnum`, schema, ref.Name)
	if err != nil {
		return td, err
	}
	for _, r := range recs {
		pos, _ := strconv.Atoi(r[8].String)
		col := model.Column{Name: r[0].String, Type: r[1].String, DataType: r[2].String, Nullable: r[3].String == "true",
			PrimaryKey: r[5].String == "true", AutoIncrement: r[6].String == "true", Comment: r[7].String, Position: pos}
		if r[4].Valid {
			v := r[4].String
			col.Default = &v
		}
		td.Columns = append(td.Columns, col)
	}

	idx, err := sqlcommon.QueryStrings(ctx, db, `SELECT ic.relname, i.indisunique, i.indisprimary, am.amname,
		array_to_string(ARRAY(SELECT pg_get_indexdef(i.indexrelid, k, true) FROM generate_subscripts(i.indkey, 1) k ORDER BY k), E'\x1f')
		FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid JOIN pg_class c ON c.oid = i.indrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace JOIN pg_am am ON am.oid = ic.relam
		WHERE n.nspname = $1 AND c.relname = $2 ORDER BY i.indisprimary DESC, ic.relname`, schema, ref.Name)
	if err != nil {
		return td, err
	}
	for _, r := range idx {
		td.Indexes = append(td.Indexes, model.Index{Name: r[0].String, Unique: r[1].String == "true", Primary: r[2].String == "true",
			Method: r[3].String, Columns: strings.Split(r[4].String, "\x1f")})
	}

	fks, err := c.foreignKeys(ctx, db, schema, ref.Name)
	if err != nil {
		return td, err
	}
	for _, f := range fks {
		td.ForeignKeys = append(td.ForeignKeys, model.ForeignKey{Name: f.name, Columns: f.cols, RefSchema: f.refSchema,
			RefTable: f.refTable, RefColumns: f.refCols, OnUpdate: f.onUpdate, OnDelete: f.onDelete})
	}
	return td, nil
}

type fkRow struct {
	schema, table, name, refSchema, refTable, onUpdate, onDelete string
	cols, refCols                                                []string
}

var fkAction = map[string]string{"a": "NO ACTION", "r": "RESTRICT", "c": "CASCADE", "n": "SET NULL", "d": "SET DEFAULT"}

// foreignKeys loads FKs for one table, or for every table in schema ("" = all schemas) when table is "".
func (c *Conn) foreignKeys(ctx context.Context, db *sql.DB, schema, table string) ([]fkRow, error) {
	recs, err := sqlcommon.QueryStrings(ctx, db, `SELECT n.nspname, c.relname, con.conname, rn.nspname, rc.relname,
		con.confupdtype, con.confdeltype,
		array_to_string(ARRAY(SELECT a.attname FROM unnest(con.conkey) WITH ORDINALITY k(n, o) JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.n ORDER BY k.o), E'\x1f'),
		array_to_string(ARRAY(SELECT a.attname FROM unnest(con.confkey) WITH ORDINALITY k(n, o) JOIN pg_attribute a ON a.attrelid = con.confrelid AND a.attnum = k.n ORDER BY k.o), E'\x1f')
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_class rc ON rc.oid = con.confrelid JOIN pg_namespace rn ON rn.oid = rc.relnamespace
		WHERE con.contype = 'f' AND ($1 = '' OR n.nspname = $1) AND ($2 = '' OR c.relname = $2)
		ORDER BY n.nspname, c.relname, con.conname`, schema, table)
	if err != nil {
		return nil, err
	}
	out := make([]fkRow, len(recs))
	for i, r := range recs {
		out[i] = fkRow{schema: r[0].String, table: r[1].String, name: r[2].String, refSchema: r[3].String, refTable: r[4].String,
			onUpdate: fkAction[r[5].String], onDelete: fkAction[r[6].String],
			cols: strings.Split(r[7].String, "\x1f"), refCols: strings.Split(r[8].String, "\x1f")}
	}
	return out, nil
}

func (c *Conn) Catalog(ctx context.Context, database string) (model.Catalog, error) {
	cat := model.Catalog{Database: database}
	db, err := c.pool(ctx, database)
	if err != nil {
		return cat, err
	}
	if cat.Schemas, err = c.ListSchemas(ctx, database); err != nil {
		return cat, err
	}
	for _, k := range []model.ObjectKind{model.KindTable, model.KindView, model.KindMatView, model.KindFunction, model.KindProcedure} {
		objs, err := c.ListObjects(ctx, database, "", k)
		if err != nil {
			return cat, err
		}
		// Skip system catalog functions: there are thousands and they're covered by builtins.
		if k == model.KindFunction || k == model.KindProcedure {
			kept := objs[:0]
			for _, o := range objs {
				if o.Schema != "pg_catalog" && o.Schema != "information_schema" {
					kept = append(kept, o)
				}
			}
			objs = kept
		}
		cat.Objects = append(cat.Objects, objs...)
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, `SELECT n.nspname, c.relname, a.attname, format_type(a.atttypid, a.atttypmod),
		EXISTS (SELECT 1 FROM pg_index i WHERE i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey)), NOT a.attnotnull
		FROM pg_attribute a JOIN pg_class c ON c.oid = a.attrelid JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p','v','m','f') AND a.attnum > 0 AND NOT a.attisdropped
		  AND n.nspname NOT LIKE 'pg\_toast%' AND n.nspname NOT LIKE 'pg\_temp\_%'
		ORDER BY n.nspname, c.relname, a.attnum`)
	if err != nil {
		return cat, err
	}
	cat.Columns = make([]model.CatalogColumn, len(recs))
	for i, r := range recs {
		cat.Columns[i] = model.CatalogColumn{Schema: r[0].String, Table: r[1].String, Name: r[2].String, Type: r[3].String, PK: r[4].String == "true", Nullable: r[5].String == "true"}
	}
	fks, err := c.foreignKeys(ctx, db, "", "")
	if err != nil {
		return cat, err
	}
	for _, f := range fks {
		cat.FKs = append(cat.FKs, model.CatalogFK{Schema: f.schema, Table: f.table, Columns: f.cols,
			RefSchema: f.refSchema, RefTable: f.refTable, RefColumns: f.refCols})
	}
	return cat, nil
}

func (c *Conn) NewSession(ctx context.Context, database, schema string) (driver.Session, error) {
	db, err := c.pool(ctx, database)
	if err != nil {
		return nil, err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s := &Session{c: c, db: db, conn: conn, database: database}
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&s.pid); err != nil {
		conn.Close()
		return nil, err
	}
	if err := sqlcommon.RunInitialQueries(ctx, conn, c.profile.Advanced.InitialQueries, sqlsplit.Postgres); err != nil {
		conn.Close()
		return nil, err
	}
	if schema != "" {
		if err := s.Use(ctx, database, schema); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return s, nil
}

type Session struct {
	c        *Conn
	db       *sql.DB
	conn     *sql.Conn
	pid      int64
	database string
}

func (s *Session) Query(ctx context.Context, stmt string) (driver.Rows, error) {
	return sqlcommon.Run(ctx, s.conn, stmt, formatValue)
}

func (s *Session) Cancel(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "SELECT pg_cancel_backend($1)", s.pid)
	return err
}

// Use changes the search_path. Switching database requires a new session, which the
// caller handles by closing this one; here we only reject the mismatch.
func (s *Session) Use(ctx context.Context, database, schema string) error {
	if database != "" && database != s.database {
		return fmt.Errorf("session is bound to database %q", s.database)
	}
	if schema == "" {
		return nil
	}
	_, err := s.conn.ExecContext(ctx, "SET search_path TO "+dialect.QuoteIdent(schema)+", public")
	return err
}

func (s *Session) Exec(ctx context.Context, stmt string, args ...any) (int64, error) {
	res, err := s.conn.ExecContext(ctx, stmt, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Session) Close() error { return sqlcommon.DiscardConn(s.conn) }
