// Package datamodel holds offline data models: table designs that live in a file
// rather than a database, with diagram positions. Models can be reverse engineered
// from a database, turned into SQL, and synchronized back to a database.
package datamodel

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"chloe/internal/design"
	"chloe/internal/sqlq"
)

const Format = "chloe-model"

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Note struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   Point  `json:"at"`
}

type Model struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Name    string `json:"name"`
	// Dialect decides which generator turns the model into SQL: mysql or postgres.
	Dialect string `json:"dialect"`
	// Schema is the default PostgreSQL schema for tables without one.
	Schema    string           `json:"schema,omitempty"`
	Tables    []design.Table   `json:"tables"`
	Positions map[string]Point `json:"positions"` // by table name (schema.name for PG)
	Notes     []Note           `json:"notes,omitempty"`
	// ColumnMode is the diagram's column display: all, keys or none.
	ColumnMode string `json:"columnMode,omitempty"`
	// CreatedBy is "mcp:<endpoint>" for models an agent created; agents may edit only those.
	CreatedBy string `json:"createdBy,omitempty"`
	// Link is the database the model was built from or last synchronized to.
	Link *Link `json:"link,omitempty"`
}

// New returns an empty model.
func New(name, dialect string) Model {
	m := Model{Format: Format, Version: 1, Name: name, Dialect: dialect, Positions: map[string]Point{}, Tables: []design.Table{}}
	if dialect == "postgres" {
		m.Schema = "public"
	}
	return m
}

func Parse(data []byte) (Model, error) {
	var m Model
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("not a Chloe model: %w", err)
	}
	if m.Format != Format {
		return m, errors.New("not a Chloe model file")
	}
	if m.Positions == nil {
		m.Positions = map[string]Point{}
	}
	return m, m.Validate()
}

func (m Model) Marshal() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// Key identifies a table in Positions.
func Key(t design.Table) string {
	if t.Schema != "" {
		return t.Schema + "." + t.Name
	}
	return t.Name
}

func (m Model) Generator() (design.Generator, error) {
	switch m.Dialect {
	case "mysql", "mariadb":
		return design.MySQL{}, nil
	case "postgres":
		return design.Postgres{}, nil
	}
	return nil, fmt.Errorf("unsupported model dialect %q", m.Dialect)
}

// Validate checks every table and that names are unique.
func (m Model) Validate() error {
	seen := map[string]bool{}
	for _, t := range m.Tables {
		k := Key(t)
		if seen[k] {
			return fmt.Errorf("duplicate table %q", k)
		}
		seen[k] = true
		if err := design.Validate(t); err != nil {
			return fmt.Errorf("table %s: %w", k, err)
		}
	}
	return nil
}

// Target places model tables in a database (and, for PostgreSQL, a schema).
type Target struct {
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"` // PostgreSQL: overrides table schemas
}

func (m Model) placed(t design.Table, to Target) design.Table {
	t.Database = to.Database
	if m.Dialect == "postgres" {
		switch {
		case to.Schema != "":
			t.Schema = to.Schema
		case t.Schema == "":
			t.Schema = m.Schema
		}
	} else {
		t.Schema = ""
	}
	return t
}

// GenerateSQL renders CREATE statements for the whole model. Foreign keys are added
// after every table exists, so table order doesn't matter.
func (m Model) GenerateSQL(to Target) ([]string, error) {
	gen, err := m.Generator()
	if err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var out, fks []string
	for _, t := range m.sortedTables() {
		t = m.placed(t, to)
		bare := t
		bare.ForeignKeys = nil
		stmts, err := gen.Create(bare)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t.Name, err)
		}
		before, after := m.sequences(t)
		out = append(out, before...)
		out = append(out, stmts...)
		out = append(out, after...)
		add, err := addForeignKeys(gen, t)
		if err != nil {
			return nil, err
		}
		fks = append(fks, add...)
	}
	return append(out, fks...), nil
}

func (m Model) sortedTables() []design.Table {
	ts := slices.Clone(m.Tables)
	sort.SliceStable(ts, func(i, j int) bool { return Key(ts[i]) < Key(ts[j]) })
	return ts
}

// asExisting marks every element as already present (OrigName = Name), which is
// how a table read from a database looks to the generators.
func asExisting(t design.Table) design.Table {
	t.OrigName = t.Name
	t.Columns = slices.Clone(t.Columns)
	for i := range t.Columns {
		t.Columns[i].OrigName = t.Columns[i].Name
	}
	t.Indexes = slices.Clone(t.Indexes)
	for i := range t.Indexes {
		t.Indexes[i].OrigName = t.Indexes[i].Name
	}
	t.Uniques = slices.Clone(t.Uniques)
	for i := range t.Uniques {
		t.Uniques[i].OrigName = t.Uniques[i].Name
	}
	t.ForeignKeys = slices.Clone(t.ForeignKeys)
	for i := range t.ForeignKeys {
		t.ForeignKeys[i].OrigName = t.ForeignKeys[i].Name
	}
	t.Checks = slices.Clone(t.Checks)
	for i := range t.Checks {
		t.Checks[i].OrigName = t.Checks[i].Name
	}
	t.Triggers = slices.Clone(t.Triggers)
	for i := range t.Triggers {
		t.Triggers[i].OrigName = t.Triggers[i].Name
	}
	return t
}

// addForeignKeys returns the ALTERs that add t's foreign keys to the table as created
// without them.
func addForeignKeys(gen design.Generator, t design.Table) ([]string, error) {
	if len(t.ForeignKeys) == 0 {
		return nil, nil
	}
	bare := t
	bare.ForeignKeys = nil
	want := asExisting(t)
	for i := range want.ForeignKeys {
		want.ForeignKeys[i].OrigName = ""
	}
	return gen.Alter(asExisting(bare), want)
}

var nextvalRe = regexp.MustCompile(`^nextval\('((?:[^']|'')+)'(?:::regclass)?\)$`)

// sequences wraps a PostgreSQL CREATE TABLE with the sequences its serial-style
// defaults (nextval('...')) need, so the model can be created in an empty database.
func (m Model) sequences(t design.Table) (before, after []string) {
	if m.Dialect != "postgres" {
		return nil, nil
	}
	tbl := sqlq.Quoted(t.Schema) + "." + sqlq.Quoted(t.Name)
	for _, c := range t.Columns {
		if c.Default == nil {
			continue
		}
		mm := nextvalRe.FindStringSubmatch(*c.Default)
		if mm == nil {
			continue
		}
		seq := strings.ReplaceAll(mm[1], "''", "'")
		before = append(before, "CREATE SEQUENCE IF NOT EXISTS "+seq)
		after = append(after, fmt.Sprintf(`ALTER SEQUENCE %s OWNED BY %s.%s`, seq, tbl, sqlq.Quoted(c.Name)))
	}
	return before, after
}

// SyncAction is one table-level step of a synchronization plan.
type SyncAction struct {
	Table      string   `json:"table"`
	Kind       string   `json:"kind"` // create, alter, drop
	Statements []string `json:"statements"`
}

// SyncPlan lists what synchronizing the model to a database would do.
type SyncPlan struct {
	Actions []SyncAction `json:"actions"`
	// Unchanged lists tables that already match.
	Unchanged []string `json:"unchanged"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Statements flattens the plan in execution order: drops of foreign keys happen in
// the per-table ALTERs; new tables are created before alters that may reference them.
func (p SyncPlan) Statements() []string {
	var out []string
	for _, kind := range []string{"create", "alter", "drop"} {
		for _, a := range p.Actions {
			if a.Kind == kind {
				out = append(out, a.Statements...)
			}
		}
	}
	return out
}

// Plan diffs the model against the tables currently in the database. existing holds
// the database's table designs (as loaded by a driver). Tables are matched by name;
// renames show up as a drop plus a create, as in other modelling tools.
func (m Model) Plan(existing []design.Table, to Target, dropMissing bool) (SyncPlan, error) {
	if err := m.Validate(); err != nil {
		return SyncPlan{}, err
	}
	diffs, err := m.Compare(existing, to)
	if err != nil {
		return SyncPlan{}, err
	}
	var plan SyncPlan
	var newFKs []string
	for _, d := range diffs {
		for _, w := range d.Warnings {
			plan.Warnings = append(plan.Warnings, d.Name+": "+w)
		}
		switch d.Op {
		case "create":
			plan.Actions = append(plan.Actions, SyncAction{Table: d.Name, Kind: "create", Statements: d.Statements})
			newFKs = append(newFKs, d.Post...)
		case "modify":
			plan.Actions = append(plan.Actions, SyncAction{Table: d.Name, Kind: "alter", Statements: d.Statements})
		case "same":
			plan.Unchanged = append(plan.Unchanged, d.Name)
		case "drop":
			if !dropMissing {
				continue
			}
			stmts := d.Statements
			if m.Dialect == "postgres" {
				stmts = []string{stmts[0] + " CASCADE"}
			}
			plan.Actions = append(plan.Actions, SyncAction{Table: d.Name, Kind: "drop", Statements: stmts})
		}
	}
	if len(newFKs) > 0 {
		// After every create (Statements orders creates first), so the referenced tables exist.
		plan.Actions = append(plan.Actions, SyncAction{Table: "(foreign keys of new tables)", Kind: "alter", Statements: newFKs})
	}
	return plan, nil
}

// matchByName sets OrigName on the model table's elements wherever the database
// table has an element of the same name, so the generator alters rather than
// recreates them.
func matchByName(cur, mt design.Table) design.Table {
	mt.OrigName = cur.Name
	has := func(names []string, n string) string {
		for _, x := range names {
			if strings.EqualFold(x, n) {
				return x
			}
		}
		return ""
	}
	var cols, idx, uq, fk, ck, tr []string
	for _, c := range cur.Columns {
		cols = append(cols, c.Name)
	}
	for _, x := range cur.Indexes {
		idx = append(idx, x.Name)
	}
	for _, x := range cur.Uniques {
		uq = append(uq, x.Name)
	}
	for _, x := range cur.ForeignKeys {
		fk = append(fk, x.Name)
	}
	for _, x := range cur.Checks {
		ck = append(ck, x.Name)
	}
	for _, x := range cur.Triggers {
		tr = append(tr, x.Name)
	}
	mt.Columns = slices.Clone(mt.Columns)
	for i := range mt.Columns {
		mt.Columns[i].OrigName = has(cols, mt.Columns[i].Name)
	}
	mt.Indexes = slices.Clone(mt.Indexes)
	for i := range mt.Indexes {
		mt.Indexes[i].OrigName = has(idx, mt.Indexes[i].Name)
	}
	mt.Uniques = slices.Clone(mt.Uniques)
	for i := range mt.Uniques {
		mt.Uniques[i].OrigName = has(uq, mt.Uniques[i].Name)
	}
	mt.ForeignKeys = slices.Clone(mt.ForeignKeys)
	for i := range mt.ForeignKeys {
		mt.ForeignKeys[i].OrigName = has(fk, mt.ForeignKeys[i].Name)
	}
	mt.Checks = slices.Clone(mt.Checks)
	for i := range mt.Checks {
		mt.Checks[i].OrigName = has(ck, mt.Checks[i].Name)
	}
	mt.Triggers = slices.Clone(mt.Triggers)
	for i := range mt.Triggers {
		mt.Triggers[i].OrigName = has(tr, mt.Triggers[i].Name)
	}
	// The primary key keeps the server's constraint name unless the model names one.
	if mt.PrimaryKey != nil && cur.PrimaryKey != nil && mt.PrimaryKey.Name == "" {
		pk := *mt.PrimaryKey
		pk.Name = cur.PrimaryKey.Name
		mt.PrimaryKey = &pk
	}
	return mt
}

// keepForeignKeyIndexes keeps MySQL's automatically created foreign key indexes:
// the server adds one per foreign key and refuses to drop it while the key exists.
func keepForeignKeyIndexes(cur, want design.Table) design.Table {
	modelIdx := map[string]bool{}
	for _, ix := range want.Indexes {
		modelIdx[strings.ToLower(ix.Name)] = true
	}
	for _, ix := range cur.Indexes {
		if modelIdx[strings.ToLower(ix.Name)] {
			continue
		}
		for _, fk := range want.ForeignKeys {
			if indexCovers(ix, fk.Columns) {
				kept := ix
				kept.OrigName = ix.Name
				want.Indexes = append(want.Indexes, kept)
				break
			}
		}
	}
	return want
}

// indexCovers reports whether an index starts with exactly the given columns.
func indexCovers(ix design.Index, cols []string) bool {
	if len(ix.Columns) < len(cols) {
		return false
	}
	for i, c := range cols {
		if ix.Columns[i].Expr != "" || !strings.EqualFold(ix.Columns[i].Name, c) {
			return false
		}
	}
	return true
}
