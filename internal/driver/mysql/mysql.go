// Package mysql implements the MySQL and MariaDB drivers.
package mysql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
	"chloe/internal/tlsconf"
	"chloe/internal/tunnel"
)

func init() {
	driver.Register(model.MySQL, Driver{})
	driver.Register(model.MariaDB, Driver{})
}

var netSeq atomic.Int64

type Driver struct{}

var dialect = driver.Dialect{Name: "mysql", IdentQuote: "`", Split: sqlsplit.MySQL}

func (Driver) Open(ctx context.Context, c model.Connection, d tunnel.Dialer) (driver.Conn, error) {
	a := c.Advanced
	cfg := gomysql.NewConfig()
	cfg.User = c.User
	cfg.Passwd = c.Password
	port := c.Port
	if port == 0 {
		port = 3306
	}
	cfg.Addr = net.JoinHostPort(c.Host, strconv.Itoa(port))
	cfg.AllowNativePasswords = true
	cfg.AllowCleartextPasswords = a.CleartextAuth
	cfg.Timeout = seconds(a.ConnectTimeoutSec, 15)
	cfg.ReadTimeout = seconds(a.ReadTimeoutSec, 0)
	cfg.WriteTimeout = seconds(a.WriteTimeoutSec, 0)
	cfg.ParseTime = false // keep server text for DATETIME so zero dates and precision survive
	cfg.ConnectionAttributes = "program_name:Chloe"
	charset := "utf8mb4"
	if a.Encoding != "" && a.Encoding != "auto" {
		charset = a.Encoding
	}
	cfg.InterpolateParams = interpolateSafe(charset)
	if err := cfg.Apply(gomysql.Charset(charset, ""), gomysql.EnableCompression(a.Compression)); err != nil {
		return nil, err
	}

	// Each opened profile gets its own registered network so the dialer can be an SSH
	// tunnel, or a local socket file.
	netName := fmt.Sprintf("chloe-%d", netSeq.Add(1))
	if a.SocketFile != "" && !c.SSH.Enabled {
		sock := a.SocketFile
		gomysql.RegisterDialContext(netName, func(ctx context.Context, _ string) (net.Conn, error) {
			var nd net.Dialer
			return nd.DialContext(ctx, "unix", sock)
		})
	} else {
		gomysql.RegisterDialContext(netName, func(ctx context.Context, addr string) (net.Conn, error) {
			return d.DialContext(ctx, "tcp", addr)
		})
	}
	cfg.Net = netName

	if c.SSL.Enabled {
		tlsName := netName + "-tls"
		tc, err := tlsconf.Config(c.SSL, c.Host)
		if err != nil {
			return nil, err
		}
		if err := gomysql.RegisterTLSConfig(tlsName, tc); err != nil {
			return nil, err
		}
		cfg.TLSConfig = tlsName
	} else if a.SocketFile == "" || c.SSH.Enabled {
		// Without "Use SSL", encrypt when the server offers TLS and fall back to plain
		// when it doesn't, like the mysql client's default (and libpq's sslmode=prefer).
		// The certificate isn't checked; "Use SSL" requires TLS and can verify it.
		cfg.TLSConfig = "preferred"
	}

	connector, err := gomysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	stop := sqlcommon.ConfigurePool(db, a)
	// The driver's Timeout only covers the dial; through an SSH tunnel the dial succeeds
	// at once and a silent server would block the greeting read, so bound the whole
	// first connection (dial, handshake, auth) by the connect timeout.
	pctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	err = db.PingContext(pctx)
	cancel()
	if err != nil {
		stop()
		db.Close()
		return nil, err
	}
	var version string
	_ = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	return &Conn{db: db, stopKeepalive: stop, initial: a.InitialQueries, version: version,
		mariadb: c.Type == model.MariaDB || strings.Contains(strings.ToLower(version), "mariadb")}, nil
}

func seconds(n, def int) time.Duration {
	if n <= 0 {
		n = def
	}
	return time.Duration(n) * time.Second
}

type Conn struct {
	db            *sql.DB
	stopKeepalive func()
	initial       string
	version       string
	mariadb       bool
}

func (c *Conn) Close() error {
	c.stopKeepalive()
	return c.db.Close()
}

// CloseDatabase is a no-op: MySQL sessions aren't bound to a database.
func (c *Conn) CloseDatabase(string)           {}
func (c *Conn) Ping(ctx context.Context) error { return c.db.PingContext(ctx) }
func (c *Conn) ServerVersion() string          { return c.version }
func (c *Conn) Dialect() driver.Dialect        { return dialect }
func (c *Conn) BuiltinFunctions() []string     { return builtinFunctions }

func (c *Conn) ListDatabases(ctx context.Context) ([]string, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r[0].String
	}
	return out, nil
}

func (c *Conn) ListSchemas(context.Context, string) ([]string, error) { return nil, nil }

func (c *Conn) ListObjects(ctx context.Context, db, _ string, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	var q string
	switch kind {
	case model.KindTable, model.KindView:
		typeCond := "TABLE_TYPE IN ('BASE TABLE','SYSTEM VERSIONED')"
		if kind == model.KindView {
			typeCond = "TABLE_TYPE = 'VIEW'"
		}
		q = `SELECT TABLE_NAME, IFNULL(TABLE_COMMENT,''), IFNULL(TABLE_ROWS,0), IFNULL(DATA_LENGTH,0)+IFNULL(INDEX_LENGTH,0),
			IFNULL(ENGINE,''), IFNULL(CREATE_TIME,''), IFNULL(UPDATE_TIME,''), ''
			FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND ` + typeCond + ` ORDER BY TABLE_NAME`
	case model.KindFunction, model.KindProcedure:
		rt := "FUNCTION"
		if kind == model.KindProcedure {
			rt = "PROCEDURE"
		}
		q = `SELECT ROUTINE_NAME, IFNULL(ROUTINE_COMMENT,''), 0, 0, '', IFNULL(CREATED,''), IFNULL(LAST_ALTERED,''), IFNULL(DTD_IDENTIFIER,'')
			FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = ? AND ROUTINE_TYPE = '` + rt + `' ORDER BY ROUTINE_NAME`
	case model.KindTrigger:
		q = `SELECT TRIGGER_NAME, CONCAT(ACTION_TIMING,' ',EVENT_MANIPULATION,' ON ',EVENT_OBJECT_TABLE), 0, 0, '', IFNULL(CREATED,''), '', ''
			FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = ? ORDER BY TRIGGER_NAME`
	case model.KindEvent:
		q = `SELECT EVENT_NAME, IFNULL(EVENT_COMMENT,''), 0, 0, STATUS, IFNULL(CREATED,''), IFNULL(LAST_ALTERED,''), ''
			FROM information_schema.EVENTS WHERE EVENT_SCHEMA = ? ORDER BY EVENT_NAME`
	default:
		return nil, nil
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, q, db)
	if err != nil {
		return nil, err
	}
	out := make([]model.ObjectInfo, 0, len(recs))
	for _, r := range recs {
		rows, _ := strconv.ParseInt(r[2].String, 10, 64)
		size, _ := strconv.ParseInt(r[3].String, 10, 64)
		out = append(out, model.ObjectInfo{
			ObjectRef:  model.ObjectRef{Database: db, Name: r[0].String, Kind: kind},
			Comment:    r[1].String,
			Rows:       rows,
			DataSize:   size,
			Engine:     r[4].String,
			Created:    r[5].String,
			Modified:   r[6].String,
			ReturnType: r[7].String,
		})
	}
	return out, nil
}

func (c *Conn) TableDetail(ctx context.Context, ref model.ObjectRef) (model.TableDetail, error) {
	td := model.TableDetail{ObjectRef: ref}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
		COLUMN_KEY, EXTRA, COLUMN_COMMENT, ORDINAL_POSITION
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY ORDINAL_POSITION`, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	for _, r := range recs {
		pos, _ := strconv.Atoi(r[8].String)
		col := model.Column{
			Name: r[0].String, Type: r[1].String, DataType: r[2].String,
			Nullable: r[3].String == "YES", PrimaryKey: r[5].String == "PRI",
			AutoIncrement: strings.Contains(r[6].String, "auto_increment"),
			Comment:       r[7].String, Position: pos,
		}
		if r[4].Valid {
			v := r[4].String
			col.Default = &v
		}
		td.Columns = append(td.Columns, col)
	}

	idx, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE, INDEX_TYPE
		FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? ORDER BY INDEX_NAME, SEQ_IN_INDEX`, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	for _, r := range idx {
		n := len(td.Indexes)
		if n == 0 || td.Indexes[n-1].Name != r[0].String {
			td.Indexes = append(td.Indexes, model.Index{Name: r[0].String, Unique: r[2].String == "0", Primary: r[0].String == "PRIMARY", Method: r[3].String})
			n++
		}
		td.Indexes[n-1].Columns = append(td.Indexes[n-1].Columns, r[1].String)
	}

	fks, err := c.foreignKeys(ctx, ref.Database, ref.Name)
	if err != nil {
		return td, err
	}
	for _, f := range fks {
		td.ForeignKeys = append(td.ForeignKeys, model.ForeignKey{
			Name: f.name, Columns: f.cols, RefDatabase: f.refDB, RefTable: f.refTable, RefColumns: f.refCols,
			OnUpdate: f.onUpdate, OnDelete: f.onDelete,
		})
	}
	return td, nil
}

type fkRow struct {
	table, name, refDB, refTable, onUpdate, onDelete string
	cols, refCols                                    []string
}

// foreignKeys loads FKs for one table, or all tables in db when table is "".
func (c *Conn) foreignKeys(ctx context.Context, db, table string) ([]fkRow, error) {
	q := `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME, k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME,
		k.REFERENCED_COLUMN_NAME, r.UPDATE_RULE, r.DELETE_RULE
		FROM information_schema.KEY_COLUMN_USAGE k
		JOIN information_schema.REFERENTIAL_CONSTRAINTS r
		  ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA AND r.CONSTRAINT_NAME = k.CONSTRAINT_NAME AND r.TABLE_NAME = k.TABLE_NAME
		WHERE k.TABLE_SCHEMA = ? AND k.REFERENCED_TABLE_NAME IS NOT NULL`
	args := []any{db}
	if table != "" {
		q += " AND k.TABLE_NAME = ?"
		args = append(args, table)
	}
	q += " ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION"
	recs, err := sqlcommon.QueryStrings(ctx, c.db, q, args...)
	if err != nil {
		return nil, err
	}
	var out []fkRow
	for _, r := range recs {
		n := len(out)
		if n == 0 || out[n-1].name != r[1].String || out[n-1].table != r[0].String {
			out = append(out, fkRow{table: r[0].String, name: r[1].String, refDB: r[3].String, refTable: r[4].String,
				onUpdate: r[6].String, onDelete: r[7].String})
			n++
		}
		out[n-1].cols = append(out[n-1].cols, r[2].String)
		out[n-1].refCols = append(out[n-1].refCols, r[5].String)
	}
	return out, nil
}

func (c *Conn) Catalog(ctx context.Context, db string) (model.Catalog, error) {
	cat := model.Catalog{Database: db}
	for _, k := range []model.ObjectKind{model.KindTable, model.KindView, model.KindFunction, model.KindProcedure} {
		objs, err := c.ListObjects(ctx, db, "", k)
		if err != nil {
			return cat, err
		}
		cat.Objects = append(cat.Objects, objs...)
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, COLUMN_KEY, IS_NULLABLE
		FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? ORDER BY TABLE_NAME, ORDINAL_POSITION`, db)
	if err != nil {
		return cat, err
	}
	cat.Columns = make([]model.CatalogColumn, len(recs))
	for i, r := range recs {
		cat.Columns[i] = model.CatalogColumn{Table: r[0].String, Name: r[1].String, Type: r[2].String, PK: r[3].String == "PRI", Nullable: r[4].String == "YES",
			EnumValues: sqlcommon.EnumValues(r[2].String)}
	}
	fks, err := c.foreignKeys(ctx, db, "")
	if err != nil {
		return cat, err
	}
	for _, f := range fks {
		cat.FKs = append(cat.FKs, model.CatalogFK{Table: f.table, Columns: f.cols, RefSchema: f.refDB, RefTable: f.refTable, RefColumns: f.refCols})
	}
	return cat, nil
}

func (c *Conn) DDL(ctx context.Context, ref model.ObjectRef) (string, error) {
	var kw string
	col := 1
	switch ref.Kind {
	case model.KindTable:
		kw = "TABLE"
	case model.KindView:
		kw = "VIEW"
	case model.KindFunction:
		kw, col = "FUNCTION", 2
	case model.KindProcedure:
		kw, col = "PROCEDURE", 2
	case model.KindTrigger:
		kw, col = "TRIGGER", 2
	case model.KindEvent:
		kw, col = "EVENT", 3
	default:
		return "", fmt.Errorf("DDL not supported for %s", ref.Kind)
	}
	recs, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW CREATE "+kw+" "+dialect.Qualified(ref))
	if err != nil {
		return "", err
	}
	if len(recs) == 0 || len(recs[0]) <= col {
		return "", errors.New("no definition returned (insufficient privileges?)")
	}
	return recs[0][col].String + ";", nil
}

func (c *Conn) NewSession(ctx context.Context, database, _ string) (driver.Session, error) {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	s := &Session{c: c, conn: conn}
	if err := conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&s.id); err != nil {
		conn.Close()
		return nil, err
	}
	if err := sqlcommon.RunInitialQueries(ctx, conn, c.initial, sqlsplit.MySQL); err != nil {
		conn.Close()
		return nil, err
	}
	if database != "" {
		if err := s.Use(ctx, database, ""); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return s, nil
}

type Session struct {
	c    *Conn
	conn *sql.Conn
	id   int64
}

func (s *Session) Query(ctx context.Context, stmt string, args ...any) (driver.Rows, error) {
	return sqlcommon.Run(ctx, s.conn, stmt, sqlcommon.FormatValue, args...)
}

// Cancel kills the running statement from a separate pool connection, which is how
// MySQL clients abort queries without dropping the session.
func (s *Session) Cancel(ctx context.Context) error {
	_, err := s.c.db.ExecContext(ctx, "KILL QUERY "+strconv.FormatInt(s.id, 10))
	return err
}

func (s *Session) Use(ctx context.Context, database, _ string) error {
	_, err := s.conn.ExecContext(ctx, "USE "+dialect.QuoteIdent(database))
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

// interpolateSafe reports whether arguments can be spliced into the SQL client-side. In
// multibyte encodings such as gbk, big5 or sjis the backslash the driver adds before a quote
// can be read as the second byte of a character, freeing the quote, so those connections
// send arguments as server-side prepared statements instead.
func interpolateSafe(charset string) bool {
	switch strings.ToLower(charset) {
	case "utf8", "utf8mb3", "utf8mb4", "latin1", "ascii", "binary":
		return true
	}
	return false
}
