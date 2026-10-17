package design

import (
	"fmt"
	"slices"
	"strings"

	"chloe/internal/sqlsplit"
)

// SQLite generates SQLite DDL. SQLite's ALTER TABLE only renames tables and
// columns and adds or drops columns, so other changes rebuild the table: create
// it under a temporary name, copy the rows, drop the old table and rename.
type SQLite struct{}

func sq(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func sqStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// RebuildPrefix names the temporary table of a rebuild.
const RebuildPrefix = "_chloe_new_"

func (SQLite) qual(db, name string) string {
	if db != "" {
		return sq(db) + "." + sq(name)
	}
	return sq(name)
}

// inlinePK reports whether the primary key is written on its column: a
// single-column key, which AUTOINCREMENT requires.
func inlinePK(t Table) string {
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) == 1 && t.PrimaryKey.Name == "" {
		return t.PrimaryKey.Columns[0]
	}
	return ""
}

func (SQLite) column(c Column, pk bool) string {
	var b strings.Builder
	b.WriteString(sq(c.Name))
	if typ := TypeSQL(c); typ != "" {
		b.WriteString(" " + typ)
	}
	if pk {
		b.WriteString(" PRIMARY KEY")
		if c.AutoIncrement {
			b.WriteString(" AUTOINCREMENT")
		}
	}
	if !c.Nullable {
		b.WriteString(" NOT NULL")
	}
	if c.Default != nil && c.GeneratedExpr == "" {
		b.WriteString(" DEFAULT " + *c.Default)
	}
	if c.Collation != "" {
		b.WriteString(" COLLATE " + c.Collation)
	}
	if c.GeneratedExpr != "" {
		kind := "VIRTUAL"
		if c.GeneratedStored {
			kind = "STORED"
		}
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) %s", c.GeneratedExpr, kind)
	}
	return b.String()
}

func constraintName(name string) string {
	if name == "" {
		return ""
	}
	return "CONSTRAINT " + sq(name) + " "
}

func (g SQLite) createTable(t Table, db, name string) string {
	pkCol := inlinePK(t)
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+g.column(c, c.Name == pkCol))
	}
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) > 0 && pkCol == "" {
		lines = append(lines, "  "+constraintName(t.PrimaryKey.Name)+"PRIMARY KEY ("+quoteList(t.PrimaryKey.Columns, sq)+")")
	}
	for _, u := range t.Uniques {
		lines = append(lines, "  "+constraintName(u.Name)+"UNIQUE ("+quoteList(u.Columns, sq)+")")
	}
	for _, fk := range t.ForeignKeys {
		s := constraintName(fk.Name) + "FOREIGN KEY (" + quoteList(fk.Columns, sq) + ") REFERENCES " + sq(fk.RefTable)
		if len(fk.RefColumns) > 0 {
			s += " (" + quoteList(fk.RefColumns, sq) + ")"
		}
		if fk.OnDelete != "" {
			s += " ON DELETE " + fk.OnDelete
		}
		if fk.OnUpdate != "" {
			s += " ON UPDATE " + fk.OnUpdate
		}
		lines = append(lines, "  "+s)
	}
	for _, c := range t.Checks {
		lines = append(lines, "  "+constraintName(c.Name)+"CHECK ("+c.Expr+")")
	}
	stmt := fmt.Sprintf("CREATE TABLE %s (\n%s\n)", g.qual(db, name), strings.Join(lines, ",\n"))
	var opts []string
	if t.Options.WithoutRowid {
		opts = append(opts, "WITHOUT ROWID")
	}
	if t.Options.Strict {
		opts = append(opts, "STRICT")
	}
	if len(opts) > 0 {
		stmt += " " + strings.Join(opts, ", ")
	}
	return stmt
}

func (g SQLite) createIndex(t Table, ix Index) string {
	kind := "INDEX"
	if ix.Kind == "unique" {
		kind = "UNIQUE INDEX"
	}
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		p := sq(c.Name)
		if c.Expr != "" {
			p = c.Expr
		}
		if c.Desc {
			p += " DESC"
		}
		cols[i] = p
	}
	s := fmt.Sprintf("CREATE %s %s ON %s (%s)", kind, g.qual(t.Database, ix.Name), sq(t.Name), strings.Join(cols, ", "))
	if ix.Where != "" {
		s += " WHERE " + ix.Where
	}
	return s
}

// triggerBody wraps a bare statement list in BEGIN … END.
func triggerBody(body string) string {
	body = strings.TrimSpace(body)
	if len(body) >= 5 && strings.EqualFold(body[:5], "BEGIN") {
		return body
	}
	body = strings.TrimRight(body, "; \n\t")
	return "BEGIN\n  " + body + ";\nEND"
}

func (g SQLite) createTrigger(t Table, tr Trigger) string {
	s := fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s", g.qual(t.Database, tr.Name), tr.Timing, tr.Events[0], sq(t.Name))
	if tr.ForEachRow {
		s += " FOR EACH ROW"
	}
	if tr.When != "" {
		s += " WHEN " + tr.When
	}
	return s + "\n" + triggerBody(tr.Body)
}

func (SQLite) validate(t Table) error {
	pk := inlinePK(t)
	for _, c := range t.Columns {
		if c.AutoIncrement && (c.Name != pk || !strings.EqualFold(strings.TrimSpace(c.Type), "integer")) {
			return fmt.Errorf("AUTOINCREMENT needs %q to be the table's only primary key column, of type INTEGER", c.Name)
		}
		if c.AutoIncrement && t.Options.WithoutRowid {
			return fmt.Errorf("WITHOUT ROWID tables can't use AUTOINCREMENT")
		}
	}
	if t.Options.WithoutRowid && (t.PrimaryKey == nil || len(t.PrimaryKey.Columns) == 0) {
		return fmt.Errorf("a WITHOUT ROWID table needs a primary key")
	}
	for _, tr := range t.Triggers {
		if len(tr.Events) != 1 {
			return fmt.Errorf("trigger %q: SQLite triggers fire on exactly one event", tr.Name)
		}
		if strings.TrimSpace(tr.Body) == "" {
			return fmt.Errorf("trigger %q has no body", tr.Name)
		}
	}
	return nil
}

func (g SQLite) Create(t Table) ([]string, error) {
	return checked(t, sqlsplit.SQLite, func() ([]string, error) { return g.create(t) })
}

func (g SQLite) create(t Table) ([]string, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if err := g.validate(t); err != nil {
		return nil, err
	}
	out := []string{g.createTable(t, t.Database, t.Name)}
	for _, ix := range t.Indexes {
		out = append(out, g.createIndex(t, ix))
	}
	for _, tr := range t.Triggers {
		out = append(out, g.createTrigger(t, tr))
	}
	return out, nil
}

// needsRebuild reports whether ALTER TABLE can't express the change: anything
// beyond renames, columns appended at the end, and index or trigger changes.
func needsRebuild(old, nw Table) bool {
	rn := renames(old, nw)
	if old.Options.WithoutRowid != nw.Options.WithoutRowid || old.Options.Strict != nw.Options.Strict {
		return true
	}
	if !slices.Equal(rn.pk(old.PrimaryKey), pkCols(nw.PrimaryKey)) || (inlinePK(old) != "") != (inlinePK(nw) != "") {
		return true
	}
	if len(old.Uniques) != len(nw.Uniques) || len(old.ForeignKeys) != len(nw.ForeignKeys) || len(old.Checks) != len(nw.Checks) {
		return true
	}
	for i := range old.Uniques {
		if !slices.Equal(rn.unique(old.Uniques[i]).Columns, nw.Uniques[i].Columns) || old.Uniques[i].Name != nw.Uniques[i].Name {
			return true
		}
	}
	for i := range old.ForeignKeys {
		if !sameFK(rn.fk(old.ForeignKeys[i]), nw.ForeignKeys[i]) {
			return true
		}
	}
	for i := range old.Checks {
		if old.Checks[i].Expr != nw.Checks[i].Expr || old.Checks[i].Name != nw.Checks[i].Name {
			return true
		}
	}
	// Surviving columns keep their order and definition, followed by new ones.
	kept := 0
	for _, c := range nw.Columns {
		if c.OrigName == "" {
			if !addable(c) {
				return true
			}
			continue
		}
		if kept >= len(old.Columns) || old.Columns[kept].Name != c.OrigName || !sameColumn(old.Columns[kept], c) {
			return true
		}
		kept++
	}
	if kept != len(old.Columns) {
		return true // a column was dropped
	}
	// New columns must all come after the existing ones.
	seenNew := false
	for _, c := range nw.Columns {
		if c.OrigName == "" {
			seenNew = true
		} else if seenNew {
			return true
		}
	}
	return false
}

// addable reports whether ALTER TABLE ADD COLUMN accepts the column.
func addable(c Column) bool {
	if c.AutoIncrement || c.GeneratedStored {
		return false
	}
	if c.Default != nil {
		d := strings.ToUpper(strings.TrimSpace(*c.Default))
		if strings.HasPrefix(d, "(") || strings.HasPrefix(d, "CURRENT_") {
			return false
		}
	}
	return c.Nullable || (c.Default != nil && !strings.EqualFold(strings.TrimSpace(*c.Default), "NULL"))
}

func (g SQLite) Alter(old, nw Table) ([]string, error) {
	return checked(nw, sqlsplit.SQLite, func() ([]string, error) { return g.alter(old, nw) })
}

func (g SQLite) alter(old, nw Table) ([]string, error) {
	if err := Validate(nw); err != nil {
		return nil, err
	}
	if err := g.validate(nw); err != nil {
		return nil, err
	}
	if needsRebuild(old, nw) {
		return g.rebuild(old, nw), nil
	}
	var out []string
	if old.Name != nw.Name {
		out = append(out, "ALTER TABLE "+g.qual(old.Database, old.Name)+" RENAME TO "+sq(nw.Name))
	}
	tbl := g.qual(nw.Database, nw.Name)
	for _, c := range nw.Columns {
		switch {
		case c.OrigName == "":
			out = append(out, "ALTER TABLE "+tbl+" ADD COLUMN "+g.column(c, false))
		case c.OrigName != c.Name:
			out = append(out, "ALTER TABLE "+tbl+" RENAME COLUMN "+sq(c.OrigName)+" TO "+sq(c.Name))
		}
	}
	// Renames above already rewrote index and trigger definitions in place.
	rn := renames(old, nw)
	for _, ix := range old.Indexes {
		if n, ok := findBy(nw.Indexes, ix.Name, func(i Index) string { return i.OrigName }); !ok || !sameIndex(rn.index(ix), n) {
			out = append(out, "DROP INDEX "+g.qual(nw.Database, ix.Name))
		}
	}
	for _, ix := range nw.Indexes {
		if o, ok := findBy(old.Indexes, ix.OrigName, func(i Index) string { return i.Name }); ok && sameIndex(rn.index(o), ix) {
			continue
		}
		out = append(out, g.createIndex(nw, ix))
	}
	for _, tr := range old.Triggers {
		if n, ok := findBy(nw.Triggers, tr.Name, func(x Trigger) string { return x.OrigName }); !ok || !sameTrigger(tr, n) {
			out = append(out, "DROP TRIGGER "+g.qual(nw.Database, tr.Name))
		}
	}
	for _, tr := range nw.Triggers {
		if o, ok := findBy(old.Triggers, tr.OrigName, func(x Trigger) string { return x.Name }); ok && sameTrigger(o, tr) {
			continue
		}
		out = append(out, g.createTrigger(nw, tr))
	}
	return out, nil
}

// rebuild re-creates the table with the new definition and copies its rows.
// It runs with foreign key enforcement off and legacy_alter_table on (see
// core.ExecuteDDL), so dropping the old table doesn't cascade and views that
// name it still resolve after the rename.
func (g SQLite) rebuild(old, nw Table) []string {
	tmp := RebuildPrefix + nw.Name
	out := []string{g.createTable(nw, nw.Database, tmp)}
	var to, from []string
	for _, c := range nw.Columns {
		if c.OrigName == "" || c.GeneratedExpr != "" {
			continue
		}
		if o, ok := findBy(old.Columns, c.OrigName, func(x Column) string { return x.Name }); ok && o.GeneratedExpr == "" {
			to = append(to, sq(c.Name))
			from = append(from, sq(o.Name))
		}
	}
	if len(to) > 0 {
		out = append(out, fmt.Sprintf("INSERT INTO %s (%s)\n  SELECT %s FROM %s", g.qual(nw.Database, tmp), strings.Join(to, ", "),
			strings.Join(from, ", "), g.qual(old.Database, old.Name)))
	}
	out = append(out, "DROP TABLE "+g.qual(old.Database, old.Name),
		"ALTER TABLE "+g.qual(nw.Database, tmp)+" RENAME TO "+sq(nw.Name))
	for _, ix := range nw.Indexes {
		out = append(out, g.createIndex(nw, ix))
	}
	for _, tr := range nw.Triggers {
		out = append(out, g.createTrigger(nw, tr))
	}
	return out
}

// sqliteDatabaseSQL attaches a database file (old nil) or renames an attachment.
func sqliteDatabaseSQL(old *Database, d Database) ([]string, error) {
	if old != nil {
		return nil, fmt.Errorf("an attached database can't be changed; detach it and attach it again")
	}
	if strings.TrimSpace(d.File) == "" {
		return nil, fmt.Errorf("choose a database file")
	}
	return []string{"ATTACH DATABASE " + sqStr(d.File) + " AS " + sq(d.Name)}, nil
}

func sqliteViewSQL(old *View, v View) ([]string, []string, error) {
	if v.Materialized {
		return nil, nil, fmt.Errorf("SQLite has no materialized views")
	}
	qual := func(name string) string {
		if v.Database != "" {
			return sq(v.Database) + "." + sq(name)
		}
		return sq(name)
	}
	create := "CREATE VIEW " + qual(v.Name) + " AS\n" + v.definition()
	if old == nil {
		return []string{create}, nil, nil
	}
	if old.Name == v.Name && old.definition() == v.definition() {
		return nil, nil, nil
	}
	// SQLite can't alter or rename a view: drop and re-create it.
	return []string{"DROP VIEW " + qual(old.Name), create},
		[]string{fmt.Sprintf("%s is dropped and re-created; INSTEAD OF triggers on it are lost.", old.Name)}, nil
}
