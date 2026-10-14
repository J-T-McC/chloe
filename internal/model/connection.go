// Package model holds the types shared between the database core and the UI bindings.
package model

// DBType identifies a database engine.
type DBType string

const (
	MySQL      DBType = "mysql"
	MariaDB    DBType = "mariadb"
	PostgreSQL DBType = "postgres"
	SQLite     DBType = "sqlite"
	MongoDB    DBType = "mongodb"
	Redis      DBType = "redis"
	SQLServer  DBType = "sqlserver"
	Oracle     DBType = "oracle"
)

// Connection is a saved connection profile. Secrets (passwords, passphrases)
// are never persisted in this struct's JSON form; they live in the OS keychain.
type Connection struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Type  DBType `json:"type"`
	Group string `json:"group,omitempty"` // folder in the connection tree
	Color string `json:"color,omitempty"`
	// Preset records the hosted service the profile was created for (supabase, neon, ...).
	// It only affects defaults and the icon; the engine is still Type.
	Preset string `json:"preset,omitempty"`

	Host         string `json:"host"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password,omitempty"` // in-memory only; stripped before saving
	SavePassword bool   `json:"savePassword"`

	// InitialDatabase is the database opened first: PostgreSQL connects to it; for
	// MySQL it is the default database of new query tabs and opens automatically.
	InitialDatabase string `json:"initialDatabase,omitempty"`

	Databases CustomDatabases `json:"databases"`
	Advanced  Advanced        `json:"advanced"`
	SSH       SSHConfig       `json:"ssh"`
	SSL       SSLConfig       `json:"ssl"`
	SQLite    SQLiteConfig    `json:"sqlite"`
	Remark    string          `json:"remark,omitempty"`
}

// CustomDatabases restricts the tree to a fixed list of databases.
type CustomDatabases struct {
	Enabled bool            `json:"enabled"`
	List    []DatabaseEntry `json:"list,omitempty"`
}

type DatabaseEntry struct {
	Name string `json:"name"`
	// AutoOpen opens the database as soon as the connection opens.
	AutoOpen bool `json:"autoOpen"`
}

// Advanced holds per-connection tuning. Zero values mean "off" or "driver default".
type Advanced struct {
	AutoConnect  bool `json:"autoConnect"`  // open at startup
	SessionLimit int  `json:"sessionLimit"` // max server connections per pool; 0 = unlimited
	KeepaliveSec int  `json:"keepaliveSec"` // ping idle pools this often; 0 = off
	// InitialQueries run on every new session (e.g. SET statements).
	InitialQueries string `json:"initialQueries,omitempty"`

	ConnectTimeoutSec int `json:"connectTimeoutSec"`
	// MySQL only.
	ReadTimeoutSec  int    `json:"readTimeoutSec"`
	WriteTimeoutSec int    `json:"writeTimeoutSec"`
	Compression     bool   `json:"compression"`
	SocketFile      string `json:"socketFile,omitempty"`
	Encoding        string `json:"encoding,omitempty"` // client character set; "" = auto
	CleartextAuth   bool   `json:"cleartextAuth"`
	// PostgreSQL only: extra libpq-style key=value parameters, one per line.
	ConnectionParams string `json:"connectionParams,omitempty"`
}

// SSHAuth selects how the SSH tunnel authenticates.
type SSHAuth string

const (
	SSHPassword          SSHAuth = "password"
	SSHPublicKey         SSHAuth = "publickey"
	SSHPasswordPublicKey SSHAuth = "password_publickey"
	SSHAgent             SSHAuth = "agent"
)

type SSHConfig struct {
	Enabled        bool    `json:"enabled"`
	Host           string  `json:"host"`
	Port           int     `json:"port"`
	User           string  `json:"user"`
	Auth           SSHAuth `json:"auth"`
	Password       string  `json:"password,omitempty"` // in-memory only
	PrivateKeyPath string  `json:"privateKeyPath,omitempty"`
	Passphrase     string  `json:"passphrase,omitempty"` // in-memory only
	SavePassword   bool    `json:"savePassword"`
}

type SSLConfig struct {
	Enabled bool `json:"enabled"`
	// PostgreSQL: prefer, allow, require, verify-ca, verify-full.
	Mode     string `json:"mode,omitempty"`
	CAFile   string `json:"caFile,omitempty"`
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
	CRLFile  string `json:"crlFile,omitempty"` // PostgreSQL
	// MySQL: verify the server certificate against the CA.
	VerifyHost bool   `json:"verifyHost"`
	Cipher     string `json:"cipher,omitempty"` // MySQL: restrict to this cipher suite
}

// SQLiteConfig locates a SQLite connection's database files. The main file opens
// as "main"; attached files appear as further databases.
type SQLiteConfig struct {
	Path string `json:"path,omitempty"`
	// Create makes a new, empty database file when Path doesn't exist.
	Create   bool         `json:"create,omitempty"`
	Attached []AttachedDB `json:"attached,omitempty"`
}

// AttachedDB is a database file attached to a SQLite connection under Name.
type AttachedDB struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// DefaultPort returns the default server port for a database type.
func DefaultPort(t DBType) int {
	switch t {
	case MySQL, MariaDB:
		return 3306
	case PostgreSQL:
		return 5432
	case MongoDB:
		return 27017
	case Redis:
		return 6379
	case SQLServer:
		return 1433
	case Oracle:
		return 1521
	}
	return 0
}

// Defaults fills unset settings with the values a new profile starts with.
func (c *Connection) Defaults() {
	if c.Port == 0 {
		c.Port = DefaultPort(c.Type)
	}
	if c.SSH.Port == 0 {
		c.SSH.Port = 22
	}
	if c.SSH.Auth == "" {
		c.SSH.Auth = SSHPassword
	}
}
