// Package driver defines the engine-neutral interface the rest of Chloe uses to
// talk to databases. Each engine lives in its own sub-package and registers itself.
package driver

import (
	"context"
	"fmt"
	"sync"

	"chloe/internal/design"
	"chloe/internal/model"
	"chloe/internal/privs"
	"chloe/internal/sqlsplit"
	"chloe/internal/tunnel"
)

// Driver opens connections for one engine.
type Driver interface {
	Open(ctx context.Context, c model.Connection, d tunnel.Dialer) (Conn, error)
}

// Conn is an open connection profile: a pool plus metadata access.
// It is safe for concurrent use.
type Conn interface {
	Close() error
	Ping(ctx context.Context) error
	ServerVersion() string
	Dialect() Dialect

	// ListDatabases lists databases visible to the user.
	ListDatabases(ctx context.Context) ([]string, error)
	// ListSchemas lists schemas in a database (PostgreSQL); engines without schemas return nil.
	ListSchemas(ctx context.Context, database string) ([]string, error)
	// ListObjects lists objects of one kind. schema is ignored by engines without schemas.
	ListObjects(ctx context.Context, database, schema string, kind model.ObjectKind) ([]model.ObjectInfo, error)
	// TableDetail loads columns, indexes and foreign keys of a table or view.
	TableDetail(ctx context.Context, ref model.ObjectRef) (model.TableDetail, error)
	// Catalog bulk-loads names, columns and FKs for a database (all its schemas) for autocomplete.
	Catalog(ctx context.Context, database string) (model.Catalog, error)
	// DDL returns the CREATE statement for an object.
	DDL(ctx context.Context, ref model.ObjectRef) (string, error)
	// Functions returns built-in function names for autocomplete.
	BuiltinFunctions() []string

	// NewSession reserves a dedicated server connection, e.g. for one editor tab, so that
	// session state (USE, SET, transactions) persists across statements.
	NewSession(ctx context.Context, database, schema string) (Session, error)
	// CloseDatabase releases resources held for one database (PostgreSQL pools).
	CloseDatabase(database string)
	// OpenConnections reports how many server connections the profile holds.
	OpenConnections() int

	// Table designer: load a table definition, list designer choices, and the DDL
	// generator for this dialect.
	LoadDesign(ctx context.Context, ref model.ObjectRef) (design.Table, error)
	DesignMeta(ctx context.Context, database string) (design.Meta, error)
	DesignGenerator() design.Generator
	// Users and privileges. LoadUser reads object privileges inside database
	// (PostgreSQL); MySQL accounts are server-wide.
	ListUsers(ctx context.Context) ([]privs.UserInfo, error)
	LoadUser(ctx context.Context, name, host, database string) (privs.User, error)
	UserMeta(ctx context.Context) (privs.Meta, error)
	// ObjectGrants lists every account's privileges on one object (database,
	// schema, table/view or routine); target's Privilege is ignored.
	ObjectGrants(ctx context.Context, target privs.Grant) ([]privs.ObjectGrant, error)

	// LoadView reads a view (or materialized view) for the view designer.
	LoadView(ctx context.Context, ref model.ObjectRef) (design.View, error)

	// New / Edit Database: current properties and the dialog's choices.
	LoadDatabase(ctx context.Context, name string) (design.Database, error)
	DatabaseMeta(ctx context.Context) (design.DatabaseMeta, error)

	// Server monitor.
	Processes(ctx context.Context) ([]model.Process, error)
	// KillProcess terminates a session, or only its running query when queryOnly is set.
	KillProcess(ctx context.Context, id string, queryOnly bool) error
	Variables(ctx context.Context) ([]model.ServerVar, error)
	Status(ctx context.Context) ([]model.ServerVar, error)
}

// Session is a single dedicated server connection.
type Session interface {
	// Query executes one statement. If it produces rows, the returned Rows must be closed.
	Query(ctx context.Context, sql string) (Rows, error)
	// Exec runs a parameterised statement (placeholders per Dialect.Placeholder).
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	// Cancel aborts the statement currently running on this session (server-side where possible).
	Cancel(ctx context.Context) error
	// Use switches the session's current database/schema.
	Use(ctx context.Context, database, schema string) error
	Close() error
}

// Rows streams a result. Next fills dst with display-formatted cells (nil = NULL).
type Rows interface {
	Columns() []model.ResultColumn
	HasRows() bool
	RowsAffected() int64
	Next(dst []*string) bool
	Err() error
	Close() error
}

// Dialect captures quoting and syntax differences used by the core and the UI.
type Dialect struct {
	Name          string           `json:"name"`
	IdentQuote    string           `json:"identQuote"` // ` or "
	HasSchemas    bool             `json:"hasSchemas"`
	DefaultSchema string           `json:"defaultSchema,omitempty"`
	Split         sqlsplit.Dialect `json:"-"`
}

// QuoteIdent quotes an identifier, doubling embedded quote characters.
func (d Dialect) QuoteIdent(s string) string {
	q := d.IdentQuote
	out := q
	for _, r := range s {
		if string(r) == q {
			out += q
		}
		out += string(r)
	}
	return out + q
}

// Placeholder returns the n-th (1-based) bind parameter marker.
func (d Dialect) Placeholder(n int) string {
	if d.Name == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}

// Qualified returns a fully quoted object name.
func (d Dialect) Qualified(ref model.ObjectRef) string {
	if d.HasSchemas && ref.Schema != "" {
		return d.QuoteIdent(ref.Schema) + "." + d.QuoteIdent(ref.Name)
	}
	if !d.HasSchemas && ref.Database != "" {
		return d.QuoteIdent(ref.Database) + "." + d.QuoteIdent(ref.Name)
	}
	return d.QuoteIdent(ref.Name)
}

var (
	regMu    sync.RWMutex
	registry = map[model.DBType]Driver{}
)

// Register makes a driver available for a database type.
func Register(t model.DBType, d Driver) {
	regMu.Lock()
	defer regMu.Unlock()
	registry[t] = d
}

// Get returns the driver for a database type.
func Get(t model.DBType) (Driver, error) {
	regMu.RLock()
	defer regMu.RUnlock()
	d, ok := registry[t]
	if !ok {
		return nil, fmt.Errorf("no driver for %q", t)
	}
	return d, nil
}
