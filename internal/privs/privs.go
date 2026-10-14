// Package privs models database users/roles and their privileges, and generates
// the CREATE/ALTER/GRANT/REVOKE statements that turn one definition into another.
package privs

import (
	"fmt"
	"sort"
	"strings"
)

// Levels of a privilege grant.
const (
	Global   = "global"   // MySQL *.*
	Database = "database" // MySQL db.*, PostgreSQL DATABASE
	Schema   = "schema"   // PostgreSQL SCHEMA
	Table    = "table"    // tables and views
	Column   = "column"
	Sequence = "sequence" // PostgreSQL
	Function = "function" // MySQL routines (FUNCTION/PROCEDURE), PostgreSQL functions
)

// Grant is one privilege on one object.
type Grant struct {
	Level    string `json:"level"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	// Object is the table, sequence or function (with its argument list for
	// PostgreSQL functions, e.g. "order_total(bigint)").
	Object string `json:"object,omitempty"`
	Column string `json:"column,omitempty"`
	// RoutineType is FUNCTION or PROCEDURE for MySQL routine grants.
	RoutineType string `json:"routineType,omitempty"`
	Privilege   string `json:"privilege"`
	Grantable   bool   `json:"grantable,omitempty"`
}

// target identifies the object of a grant (everything but the privilege).
func (g Grant) target() string {
	return strings.Join([]string{g.Level, g.Database, g.Schema, g.Object, g.Column, g.RoutineType}, "\x00")
}

func (g Grant) key() string { return g.target() + "\x00" + g.Privilege }

// User is a MySQL account or a PostgreSQL role.
type User struct {
	Name string `json:"name"`
	// Host is the MySQL account host ('%', 'localhost', …).
	Host string `json:"host,omitempty"`
	// OrigName/OrigHost identify the account as loaded; empty for a new one.
	OrigName string `json:"origName,omitempty"`
	OrigHost string `json:"origHost,omitempty"`
	// Password is set only to change it (passwords can't be read back).
	Password *string `json:"password,omitempty"`

	// MySQL
	Plugin             string `json:"plugin,omitempty"`
	MaxQueries         int    `json:"maxQueries,omitempty"`
	MaxUpdates         int    `json:"maxUpdates,omitempty"`
	MaxConnections     int    `json:"maxConnections,omitempty"`
	MaxUserConnections int    `json:"maxUserConnections,omitempty"`
	SSLType            string `json:"sslType,omitempty"` // "", "SSL", "X509"
	Locked             bool   `json:"locked,omitempty"`
	PasswordExpired    bool   `json:"passwordExpired,omitempty"`

	// PostgreSQL
	CanLogin    bool   `json:"canLogin,omitempty"`
	Superuser   bool   `json:"superuser,omitempty"`
	CreateDB    bool   `json:"createDb,omitempty"`
	CreateRole  bool   `json:"createRole,omitempty"`
	Inherit     bool   `json:"inherit,omitempty"`
	Replication bool   `json:"replication,omitempty"`
	BypassRLS   bool   `json:"bypassRls,omitempty"`
	ConnLimit   int    `json:"connLimit"` // -1: no limit
	ValidUntil  string `json:"validUntil,omitempty"`
	Comment     string `json:"comment,omitempty"`

	// MemberOf lists granted roles (MySQL "role@host", PostgreSQL role names).
	MemberOf   []string `json:"memberOf"`
	Privileges []Grant  `json:"privileges"`
}

// UserInfo is a row of the user list.
type UserInfo struct {
	Name     string `json:"name"`
	Host     string `json:"host,omitempty"`
	Plugin   string `json:"plugin,omitempty"`
	CanLogin bool   `json:"canLogin"`
	Locked   bool   `json:"locked,omitempty"`
	Super    bool   `json:"super,omitempty"`
	// IsRole marks MySQL roles (locked, passwordless accounts granted to others)
	// and PostgreSQL roles that can't log in.
	IsRole bool `json:"isRole,omitempty"`
}

// Meta lists choices for the user editor.
type Meta struct {
	Roles   []string `json:"roles"`
	Plugins []string `json:"plugins,omitempty"`
	// Privileges available at each level.
	Levels map[string][]string `json:"levels"`
}

var levelRank = map[string]int{Global: 0, Database: 1, Schema: 2, Table: 3, Column: 4, Sequence: 5, Function: 6}

// Stmt is a statement and the database it must run in ("" for any).
type Stmt struct {
	Database string `json:"database,omitempty"`
	SQL      string `json:"sql"`
}

// SQL returns the statements that create u (old nil) or change old into u.
func SQL(dialect string, old *User, u User) ([]Stmt, error) {
	if strings.TrimSpace(u.Name) == "" {
		return nil, fmt.Errorf("enter a user name")
	}
	if dialect == "postgres" {
		return pgSQL(old, u)
	}
	return mySQL(old, u)
}

// DropSQL drops an account.
func DropSQL(dialect string, u UserInfo) string {
	if dialect == "postgres" {
		return "DROP ROLE " + pgq(u.Name)
	}
	return "DROP USER " + myAccount(u.Name, u.Host)
}

// grantDiff splits grants into added (or upgraded to grantable), removed, and
// grant options to revoke, each grouped by target.
type change struct {
	target Grant // a representative grant for the target (Privilege unused)
	privs  []string
}

func grantDiff(old, cur []Grant) (add, addGrantable, remove, revokeOption []change) {
	was := map[string]Grant{}
	for _, g := range old {
		was[g.key()] = g
	}
	now := map[string]Grant{}
	for _, g := range cur {
		now[g.key()] = g
	}
	group := func(list map[string][]Grant) []change {
		keys := make([]string, 0, len(list))
		for k := range list {
			keys = append(keys, k)
		}
		// Broadest first (global, database, schema, …), then by object.
		sort.Slice(keys, func(i, j int) bool {
			a, b := levelRank[list[keys[i]][0].Level], levelRank[list[keys[j]][0].Level]
			if a != b {
				return a < b
			}
			return keys[i] < keys[j]
		})
		out := make([]change, 0, len(keys))
		for _, k := range keys {
			gs := list[k]
			c := change{target: gs[0]}
			for _, g := range gs {
				c.privs = append(c.privs, g.Privilege)
			}
			sort.Strings(c.privs)
			out = append(out, c)
		}
		return out
	}
	a, ag, r, ro := map[string][]Grant{}, map[string][]Grant{}, map[string][]Grant{}, map[string][]Grant{}
	for k, g := range now {
		o, ok := was[k]
		switch {
		case !ok && g.Grantable:
			ag[g.target()] = append(ag[g.target()], g)
		case !ok:
			a[g.target()] = append(a[g.target()], g)
		case g.Grantable && !o.Grantable:
			ag[g.target()] = append(ag[g.target()], g)
		case !g.Grantable && o.Grantable:
			ro[g.target()] = append(ro[g.target()], g)
		}
	}
	for k, g := range was {
		if _, ok := now[k]; !ok {
			r[g.target()] = append(r[g.target()], g)
		}
	}
	return group(a), group(ag), group(r), group(ro)
}

func membershipDiff(old, cur []string) (add, remove []string) {
	was := map[string]bool{}
	for _, r := range old {
		was[r] = true
	}
	now := map[string]bool{}
	for _, r := range cur {
		now[r] = true
		if !was[r] {
			add = append(add, r)
		}
	}
	for _, r := range old {
		if !now[r] {
			remove = append(remove, r)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	return
}

// ObjectGrant is one account's privilege on an object (Set Privileges).
type ObjectGrant struct {
	User      string `json:"user"`
	Host      string `json:"host,omitempty"`
	Privilege string `json:"privilege"`
	Grantable bool   `json:"grantable,omitempty"`
}
