// Package design models a table definition for the table designer and generates the
// CREATE / ALTER statements that turn one definition into another.
//
// Every element carries the name it had when loaded (Orig*). The UI edits a copy, and
// the generators compare original and edited definitions by those names, so a renamed
// column becomes a rename rather than a drop and re-add.
package design

import (
	"fmt"
	"strings"
)

type Table struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	Name     string `json:"name"`
	OrigName string `json:"origName,omitempty"` // empty for a new table
	Comment  string `json:"comment,omitempty"`

	Columns     []Column     `json:"columns"`
	PrimaryKey  *PrimaryKey  `json:"primaryKey,omitempty"`
	Indexes     []Index      `json:"indexes"`
	Uniques     []Unique     `json:"uniques"` // PostgreSQL unique constraints
	ForeignKeys []ForeignKey `json:"foreignKeys"`
	Checks      []Check      `json:"checks"`
	Triggers    []Trigger    `json:"triggers"`
	Options     Options      `json:"options"`
}

type Column struct {
	Name     string `json:"name"`
	OrigName string `json:"origName,omitempty"`
	// Type is the base type (varchar, int, numeric, enum, text[] ...). Length and Scale
	// hold the modifiers shown in their own grid columns; for enum/set, Length holds the
	// quoted value list ('a','b').
	Type     string `json:"type"`
	Length   string `json:"length,omitempty"`
	Scale    string `json:"scale,omitempty"`
	Nullable bool   `json:"nullable"`
	// Default is a SQL expression exactly as it should appear after DEFAULT
	// ('abc', 0, CURRENT_TIMESTAMP, now()). Nil means no default.
	Default *string `json:"default,omitempty"`
	Comment string  `json:"comment,omitempty"`

	// MySQL
	AutoIncrement bool   `json:"autoIncrement,omitempty"`
	Unsigned      bool   `json:"unsigned,omitempty"`
	Zerofill      bool   `json:"zerofill,omitempty"`
	OnUpdateNow   bool   `json:"onUpdateNow,omitempty"`
	Charset       string `json:"charset,omitempty"`
	Collation     string `json:"collation,omitempty"`

	// Generated column: expression plus STORED (true) or VIRTUAL.
	GeneratedExpr   string `json:"generatedExpr,omitempty"`
	GeneratedStored bool   `json:"generatedStored,omitempty"`

	// PostgreSQL identity: "", "always" or "by default".
	Identity     string        `json:"identity,omitempty"`
	IdentityOpts *IdentityOpts `json:"identityOpts,omitempty"`
}

type IdentityOpts struct {
	Start     string `json:"start,omitempty"`
	Increment string `json:"increment,omitempty"`
	Min       string `json:"min,omitempty"`
	Max       string `json:"max,omitempty"`
	Cache     string `json:"cache,omitempty"`
	Cycle     bool   `json:"cycle,omitempty"`
}

type PrimaryKey struct {
	Name    string   `json:"name,omitempty"` // PG constraint name; MySQL is always PRIMARY
	Columns []string `json:"columns"`
}

type IndexColumn struct {
	Name   string `json:"name"`
	Length string `json:"length,omitempty"` // MySQL prefix length
	Desc   bool   `json:"desc,omitempty"`
	// Expr replaces Name for expression indexes (lower(email)).
	Expr string `json:"expr,omitempty"`
}

type Index struct {
	Name     string        `json:"name"`
	OrigName string        `json:"origName,omitempty"`
	Columns  []IndexColumn `json:"columns"`
	// Kind: "" (normal), "unique", "fulltext", "spatial" (the last two MySQL only).
	Kind    string `json:"kind,omitempty"`
	Method  string `json:"method,omitempty"` // BTREE, HASH, GIN, GIST, BRIN ...
	Where   string `json:"where,omitempty"`  // PG partial index predicate
	Comment string `json:"comment,omitempty"`
	// Invisible: MySQL 8 invisible index.
	Invisible bool `json:"invisible,omitempty"`
}

type Unique struct {
	Name     string   `json:"name"`
	OrigName string   `json:"origName,omitempty"`
	Columns  []string `json:"columns"`
}

type ForeignKey struct {
	Name        string   `json:"name"`
	OrigName    string   `json:"origName,omitempty"`
	Columns     []string `json:"columns"`
	RefDatabase string   `json:"refDatabase,omitempty"` // MySQL
	RefSchema   string   `json:"refSchema,omitempty"`   // PostgreSQL
	RefTable    string   `json:"refTable"`
	RefColumns  []string `json:"refColumns"`
	OnDelete    string   `json:"onDelete,omitempty"` // NO ACTION, RESTRICT, CASCADE, SET NULL, SET DEFAULT
	OnUpdate    string   `json:"onUpdate,omitempty"`
}

type Check struct {
	Name     string `json:"name"`
	OrigName string `json:"origName,omitempty"`
	Expr     string `json:"expr"`
	// NotEnforced: MySQL 8 NOT ENFORCED; PostgreSQL NOT VALID is not modelled.
	NotEnforced bool `json:"notEnforced,omitempty"`
}

type Trigger struct {
	Name     string   `json:"name"`
	OrigName string   `json:"origName,omitempty"`
	Timing   string   `json:"timing"` // BEFORE, AFTER, INSTEAD OF
	Events   []string `json:"events"` // INSERT, UPDATE, DELETE, TRUNCATE
	// MySQL: the statement body (BEGIN ... END or a single statement).
	Body string `json:"body,omitempty"`
	// PostgreSQL: FOR EACH ROW (true) or STATEMENT, the function to execute and
	// an optional WHEN condition.
	ForEachRow bool   `json:"forEachRow,omitempty"`
	Function   string `json:"function,omitempty"` // schema.fn() including arguments
	When       string `json:"when,omitempty"`
}

type Options struct {
	// MySQL
	Engine        string `json:"engine,omitempty"`
	Charset       string `json:"charset,omitempty"`
	Collation     string `json:"collation,omitempty"`
	AutoIncrement string `json:"autoIncrement,omitempty"`
	RowFormat     string `json:"rowFormat,omitempty"`
	// PostgreSQL
	Unlogged   bool   `json:"unlogged,omitempty"`
	Tablespace string `json:"tablespace,omitempty"`
	// SQLite
	WithoutRowid bool `json:"withoutRowid,omitempty"`
	Strict       bool `json:"strict,omitempty"`
}

// Meta lists the choices the designer offers for a database.
type Meta struct {
	Types      []string        `json:"types"`
	Charsets   []string        `json:"charsets,omitempty"`
	Collations []CollationInfo `json:"collations"`
	Engines    []string        `json:"engines,omitempty"`
	// Defaults applied to new tables (MySQL server/database defaults).
	DefaultEngine    string   `json:"defaultEngine,omitempty"`
	DefaultCharset   string   `json:"defaultCharset,omitempty"`
	DefaultCollation string   `json:"defaultCollation,omitempty"`
	IndexMethods     []string `json:"indexMethods"`
	// Trigger functions available (PostgreSQL), as schema.name.
	TriggerFunctions []string `json:"triggerFunctions,omitempty"`
}

type CollationInfo struct {
	Name    string `json:"name"`
	Charset string `json:"charset,omitempty"`
	Default bool   `json:"default,omitempty"`
}

// Generator produces DDL for one dialect.
type Generator interface {
	// Create returns the statements that create t from nothing.
	Create(t Table) ([]string, error)
	// Alter returns the statements that change old into new. An empty result means
	// there is nothing to do.
	Alter(old, new Table) ([]string, error)
}

// Validate catches mistakes the server would otherwise report less clearly.
func Validate(t Table) error {
	if t.Name == "" {
		return fmt.Errorf("table name is required")
	}
	if len(t.Columns) == 0 {
		return fmt.Errorf("a table needs at least one column")
	}
	seen := map[string]bool{}
	for i, c := range t.Columns {
		if c.Name == "" {
			return fmt.Errorf("column %d has no name", i+1)
		}
		if seen[c.Name] {
			return fmt.Errorf("duplicate column name %q", c.Name)
		}
		seen[c.Name] = true
		if c.Type == "" {
			return fmt.Errorf("column %q has no type", c.Name)
		}
	}
	check := func(what string, cols []string) error {
		if len(cols) == 0 {
			return fmt.Errorf("%s has no columns", what)
		}
		for _, n := range cols {
			if !seen[n] {
				return fmt.Errorf("%s refers to unknown column %q", what, n)
			}
		}
		return nil
	}
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) > 0 {
		if err := check("primary key", t.PrimaryKey.Columns); err != nil {
			return err
		}
	}
	for _, ix := range t.Indexes {
		var cols []string
		for _, c := range ix.Columns {
			if c.Expr == "" {
				cols = append(cols, c.Name)
			}
		}
		if len(ix.Columns) == 0 {
			return fmt.Errorf("index %q has no columns", ix.Name)
		}
		if len(cols) > 0 {
			if err := check(fmt.Sprintf("index %q", ix.Name), cols); err != nil {
				return err
			}
		}
	}
	for _, u := range t.Uniques {
		if err := check(fmt.Sprintf("unique %q", u.Name), u.Columns); err != nil {
			return err
		}
	}
	for _, fk := range t.ForeignKeys {
		if err := check(fmt.Sprintf("foreign key %q", fk.Name), fk.Columns); err != nil {
			return err
		}
		if fk.RefTable == "" {
			return fmt.Errorf("foreign key %q has no referenced table", fk.Name)
		}
		if len(fk.RefColumns) != len(fk.Columns) {
			return fmt.Errorf("foreign key %q: %d columns but %d referenced columns", fk.Name, len(fk.Columns), len(fk.RefColumns))
		}
	}
	for _, c := range t.Checks {
		if c.Expr == "" {
			return fmt.Errorf("check %q has no expression", c.Name)
		}
	}
	for _, tr := range t.Triggers {
		if tr.Name == "" || len(tr.Events) == 0 {
			return fmt.Errorf("trigger %q needs a name and at least one event", tr.Name)
		}
	}
	return nil
}

// TypeSQL renders type(length,scale). Array suffixes stay after the modifiers:
// varchar[] with length 20 renders as varchar(20)[].
func TypeSQL(c Column) string {
	base, arr := c.Type, ""
	if i := strings.Index(base, "["); i > 0 {
		base, arr = base[:i], base[i:]
	}
	switch {
	case c.Length != "" && c.Scale != "":
		base += "(" + c.Length + "," + c.Scale + ")"
	case c.Length != "":
		base += "(" + c.Length + ")"
	}
	return base + arr
}
