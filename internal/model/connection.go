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
	Snowflake  DBType = "snowflake"
)

// Connection is a saved connection profile. Secrets (passwords, passphrases)
// are never persisted in this struct's JSON form; they live in the OS keychain.
type Connection struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         DBType `json:"type"`
	Group        string `json:"group,omitempty"`
	Color        string `json:"color,omitempty"`
	Host         string `json:"host"`
	Port         int    `json:"port"`
	User         string `json:"user"`
	Password     string `json:"password,omitempty"` // in-memory only; stripped before saving
	SavePassword bool   `json:"savePassword"`

	// InitialDatabase limits the tree to one database/schema when set.
	InitialDatabase string `json:"initialDatabase,omitempty"`
	// ShowDatabases optionally restricts which databases appear in the tree.
	ShowDatabases []string `json:"showDatabases,omitempty"`

	SSH SSHConfig `json:"ssh"`
	SSL SSLConfig `json:"ssl"`

	// Engine-specific settings (e.g. MySQL encoding, PG sslmode overrides, keepalive).
	Options map[string]string `json:"options,omitempty"`
	Remark  string            `json:"remark,omitempty"`
}

// SSHAuth selects how the SSH tunnel authenticates.
type SSHAuth string

const (
	SSHPassword  SSHAuth = "password"
	SSHPublicKey SSHAuth = "publickey"
	SSHAgent     SSHAuth = "agent"
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
	Enabled    bool   `json:"enabled"`
	Mode       string `json:"mode,omitempty"` // postgres: require/verify-ca/verify-full; mysql: true/skip-verify/preferred
	CAFile     string `json:"caFile,omitempty"`
	CertFile   string `json:"certFile,omitempty"`
	KeyFile    string `json:"keyFile,omitempty"`
	VerifyHost bool   `json:"verifyHost"`
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
