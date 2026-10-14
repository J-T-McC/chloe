package design

import (
	"fmt"
	"strings"
)

// Postgres generates PostgreSQL DDL. Its statements are meant to run in one
// transaction (PostgreSQL DDL is transactional), so ordering only has to be valid,
// not atomic per statement.
type Postgres struct{}

func pgq(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// pgIdent quotes only when needed, keeping generated SQL readable.
func pgIdent(s string) string {
	if s == "" {
		return s
	}
	for i, r := range s {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (i > 0 && r >= '0' && r <= '9')) {
			return pgq(s)
		}
	}
	if pgReserved[s] {
		return pgq(s)
	}
	return s
}

var pgReserved = map[string]bool{"all": true, "and": true, "any": true, "array": true, "as": true, "asc": true, "both": true, "case": true, "cast": true,
	"check": true, "collate": true, "column": true, "constraint": true, "create": true, "default": true, "desc": true, "distinct": true, "do": true,
	"else": true, "end": true, "except": true, "false": true, "for": true, "foreign": true, "from": true, "grant": true, "group": true, "having": true,
	"in": true, "into": true, "leading": true, "limit": true, "not": true, "null": true, "offset": true, "on": true, "only": true, "or": true,
	"order": true, "primary": true, "references": true, "select": true, "table": true, "then": true, "to": true, "true": true, "union": true,
	"unique": true, "user": true, "using": true, "when": true, "where": true, "with": true}

func pgStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (Postgres) table(t Table, name string) string {
	if t.Schema != "" {
		return pgIdent(t.Schema) + "." + pgIdent(name)
	}
	return pgIdent(name)
}

func identityClause(c Column) string {
	s := " GENERATED " + strings.ToUpper(c.Identity) + " AS IDENTITY"
	if o := c.IdentityOpts; o != nil {
		var parts []string
		if o.Start != "" {
			parts = append(parts, "START WITH "+o.Start)
		}
		if o.Increment != "" {
			parts = append(parts, "INCREMENT BY "+o.Increment)
		}
		if o.Min != "" {
			parts = append(parts, "MINVALUE "+o.Min)
		}
		if o.Max != "" {
			parts = append(parts, "MAXVALUE "+o.Max)
		}
		if o.Cache != "" {
			parts = append(parts, "CACHE "+o.Cache)
		}
		if o.Cycle {
			parts = append(parts, "CYCLE")
		}
		if len(parts) > 0 {
			s += " (" + strings.Join(parts, " ") + ")"
		}
	}
	return s
}

func (Postgres) column(c Column) string {
	var b strings.Builder
	b.WriteString(pgIdent(c.Name) + " " + TypeSQL(c))
	if c.Collation != "" {
		b.WriteString(" COLLATE " + pgq(c.Collation))
	}
	switch {
	case c.Identity != "":
		b.WriteString(identityClause(c))
	case c.GeneratedExpr != "":
		kind := "STORED"
		if !c.GeneratedStored {
			kind = "VIRTUAL" // PostgreSQL 18+
		}
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) %s", c.GeneratedExpr, kind)
	}
	if !c.Nullable {
		b.WriteString(" NOT NULL")
	}
	if c.Default != nil && c.Identity == "" && c.GeneratedExpr == "" {
		b.WriteString(" DEFAULT " + *c.Default)
	}
	return b.String()
}

func (g Postgres) indexDDL(t Table, ix Index) string {
	unique := ""
	if ix.Kind == "unique" {
		unique = "UNIQUE "
	}
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		p := pgIdent(c.Name)
		if c.Expr != "" {
			p = "(" + c.Expr + ")"
		}
		if c.Desc {
			p += " DESC"
		}
		cols[i] = p
	}
	s := fmt.Sprintf("CREATE %sINDEX %s ON %s", unique, pgIdent(ix.Name), g.table(t, t.Name))
	if ix.Method != "" && !strings.EqualFold(ix.Method, "btree") {
		s += " USING " + strings.ToLower(ix.Method)
	}
	s += " (" + strings.Join(cols, ", ") + ")"
	if ix.Where != "" {
		s += " WHERE " + ix.Where
	}
	return s
}

// fkDef qualifies the referenced table with its schema (the table's own schema when
// unset) so the DDL doesn't depend on search_path.
func (Postgres) fkDef(t Table, fk ForeignKey) string {
	ref := pgIdent(fk.RefTable)
	if s := fk.RefSchema; s != "" {
		ref = pgIdent(s) + "." + ref
	} else if t.Schema != "" {
		ref = pgIdent(t.Schema) + "." + ref
	}
	s := fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)", pgIdent(fk.Name), quoteList(fk.Columns, pgIdent), ref, quoteList(fk.RefColumns, pgIdent))
	if fk.OnDelete != "" && !strings.EqualFold(fk.OnDelete, "NO ACTION") {
		s += " ON DELETE " + fk.OnDelete
	}
	if fk.OnUpdate != "" && !strings.EqualFold(fk.OnUpdate, "NO ACTION") {
		s += " ON UPDATE " + fk.OnUpdate
	}
	return s
}

func (g Postgres) triggerDDL(t Table, tr Trigger) string {
	each := "STATEMENT"
	if tr.ForEachRow {
		each = "ROW"
	}
	s := fmt.Sprintf("CREATE TRIGGER %s %s %s ON %s FOR EACH %s", pgIdent(tr.Name), tr.Timing, strings.Join(tr.Events, " OR "), g.table(t, t.Name), each)
	if tr.When != "" {
		s += " WHEN (" + tr.When + ")"
	}
	fn := strings.TrimSpace(tr.Function)
	if !strings.HasSuffix(fn, ")") {
		fn += "()"
	}
	return s + " EXECUTE FUNCTION " + fn
}

func (g Postgres) validate(t Table) error {
	for _, c := range t.Columns {
		if c.Identity != "" && c.GeneratedExpr != "" {
			return fmt.Errorf("column %q can't be both an identity and a generated column", c.Name)
		}
		if c.AutoIncrement || c.Unsigned || c.Zerofill || c.OnUpdateNow {
			return fmt.Errorf("column %q uses MySQL-only options", c.Name)
		}
	}
	for _, tr := range t.Triggers {
		if strings.TrimSpace(tr.Function) == "" {
			return fmt.Errorf("trigger %q has no function", tr.Name)
		}
	}
	return nil
}

func (g Postgres) comments(t Table, old *Table) []string {
	var out []string
	tbl := g.table(t, t.Name)
	if old == nil && t.Comment != "" || old != nil && old.Comment != t.Comment {
		out = append(out, fmt.Sprintf("COMMENT ON TABLE %s IS %s", tbl, commentValue(t.Comment)))
	}
	for _, c := range t.Columns {
		prev := ""
		if old != nil {
			if o, ok := findBy(old.Columns, c.OrigName, func(x Column) string { return x.Name }); ok {
				prev = o.Comment
			}
		}
		if c.Comment != prev {
			out = append(out, fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s", tbl, pgIdent(c.Name), commentValue(c.Comment)))
		}
	}
	return out
}

func commentValue(s string) string {
	if s == "" {
		return "NULL"
	}
	return pgStr(s)
}

func (g Postgres) Create(t Table) ([]string, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if err := g.validate(t); err != nil {
		return nil, err
	}
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+g.column(c))
	}
	if t.PrimaryKey != nil && len(t.PrimaryKey.Columns) > 0 {
		lines = append(lines, "  "+pkDef(t.PrimaryKey))
	}
	for _, u := range t.Uniques {
		lines = append(lines, fmt.Sprintf("  CONSTRAINT %s UNIQUE (%s)", pgIdent(u.Name), quoteList(u.Columns, pgIdent)))
	}
	for _, fk := range t.ForeignKeys {
		lines = append(lines, "  "+g.fkDef(t, fk))
	}
	for _, c := range t.Checks {
		lines = append(lines, fmt.Sprintf("  CONSTRAINT %s CHECK (%s)", pgIdent(c.Name), c.Expr))
	}
	kw := "TABLE"
	if t.Options.Unlogged {
		kw = "UNLOGGED TABLE"
	}
	stmt := fmt.Sprintf("CREATE %s %s (\n%s\n)", kw, g.table(t, t.Name), strings.Join(lines, ",\n"))
	if t.Options.Tablespace != "" {
		stmt += " TABLESPACE " + pgIdent(t.Options.Tablespace)
	}
	out := []string{stmt}
	for _, ix := range t.Indexes {
		out = append(out, g.indexDDL(t, ix))
	}
	out = append(out, g.comments(t, nil)...)
	for _, tr := range t.Triggers {
		out = append(out, g.triggerDDL(t, tr))
	}
	return out, nil
}

func pkDef(pk *PrimaryKey) string {
	s := "PRIMARY KEY (" + quoteList(pk.Columns, pgIdent) + ")"
	if pk.Name != "" {
		s = "CONSTRAINT " + pgIdent(pk.Name) + " " + s
	}
	return s
}

func (g Postgres) Alter(old, nw Table) ([]string, error) {
	if err := Validate(nw); err != nil {
		return nil, err
	}
	if err := g.validate(nw); err != nil {
		return nil, err
	}
	var out []string
	rn := renames(old, nw)
	if nw.Name != old.Name {
		out = append(out, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", g.table(old, old.Name), pgIdent(nw.Name)))
	}
	tbl := g.table(nw, nw.Name)
	alter := func(clause string) { out = append(out, "ALTER TABLE "+tbl+" "+clause) }

	// Drop constraints and indexes that changed or went away (they may depend on
	// columns about to change).
	for _, tr := range old.Triggers {
		if n, ok := findBy(nw.Triggers, tr.Name, func(x Trigger) string { return x.OrigName }); !ok || !sameTrigger(tr, n) {
			out = append(out, fmt.Sprintf("DROP TRIGGER %s ON %s", pgIdent(tr.Name), tbl))
		}
	}
	for _, fk := range old.ForeignKeys {
		if n, ok := findBy(nw.ForeignKeys, fk.Name, func(f ForeignKey) string { return f.OrigName }); !ok || !sameFK(rn.fk(fk), n) {
			alter("DROP CONSTRAINT " + pgIdent(fk.Name))
		}
	}
	for _, ck := range old.Checks {
		if n, ok := findBy(nw.Checks, ck.Name, func(c Check) string { return c.OrigName }); !ok || n.Expr != ck.Expr || n.Name != ck.Name {
			alter("DROP CONSTRAINT " + pgIdent(ck.Name))
		}
	}
	for _, u := range old.Uniques {
		if n, ok := findBy(nw.Uniques, u.Name, func(x Unique) string { return x.OrigName }); !ok || n.Name != u.Name || !equalStrings(n.Columns, rn.unique(u).Columns) {
			alter("DROP CONSTRAINT " + pgIdent(u.Name))
		}
	}
	for _, ix := range old.Indexes {
		if n, ok := findBy(nw.Indexes, ix.Name, func(i Index) string { return i.OrigName }); !ok || !sameIndex(rn.index(ix), n) {
			name := pgIdent(ix.Name)
			if nw.Schema != "" {
				name = pgIdent(nw.Schema) + "." + name
			}
			out = append(out, "DROP INDEX "+name)
		}
	}
	oldPK, newPK := rn.pk(old.PrimaryKey), pkCols(nw.PrimaryKey)
	pkChanged := !equalStrings(oldPK, newPK) || (old.PrimaryKey != nil && nw.PrimaryKey != nil && old.PrimaryKey.Name != nw.PrimaryKey.Name)
	if pkChanged && len(oldPK) > 0 {
		alter("DROP CONSTRAINT " + pgIdent(old.PrimaryKey.Name))
	}

	// Columns.
	for _, c := range old.Columns {
		if _, ok := findBy(nw.Columns, c.Name, func(x Column) string { return x.OrigName }); !ok {
			alter("DROP COLUMN " + pgIdent(c.Name))
		}
	}
	for _, c := range nw.Columns {
		if c.OrigName == "" {
			alter("ADD COLUMN " + g.column(c))
			continue
		}
		o, ok := findBy(old.Columns, c.OrigName, func(x Column) string { return x.Name })
		if !ok {
			return nil, fmt.Errorf("column %q refers to unknown original column %q", c.Name, c.OrigName)
		}
		if o.Name != c.Name {
			alter(fmt.Sprintf("RENAME COLUMN %s TO %s", pgIdent(o.Name), pgIdent(c.Name)))
		}
		col := pgIdent(c.Name)
		typeChanged := !sameType(o, c) || o.Collation != c.Collation
		// Identity and defaults are dropped before a type change and restored after.
		if o.Identity != "" && (c.Identity == "" || typeChanged || !identityEqual(o.IdentityOpts, c.IdentityOpts) || o.Identity != c.Identity) {
			alter(fmt.Sprintf("ALTER COLUMN %s DROP IDENTITY", col))
		}
		if o.GeneratedExpr != "" && c.GeneratedExpr == "" {
			alter(fmt.Sprintf("ALTER COLUMN %s DROP EXPRESSION", col))
		}
		if typeChanged {
			if o.Default != nil {
				alter(fmt.Sprintf("ALTER COLUMN %s DROP DEFAULT", col))
			}
			coll := ""
			if c.Collation != "" {
				coll = " COLLATE " + pgq(c.Collation)
			}
			alter(fmt.Sprintf("ALTER COLUMN %s TYPE %s%s USING %s::%s", col, TypeSQL(c), coll, col, TypeSQL(c)))
		}
		if o.Nullable != c.Nullable {
			if c.Nullable {
				alter(fmt.Sprintf("ALTER COLUMN %s DROP NOT NULL", col))
			} else {
				alter(fmt.Sprintf("ALTER COLUMN %s SET NOT NULL", col))
			}
		}
		if c.Identity == "" && c.GeneratedExpr == "" && (typeChanged || !strPtrEqual(o.Default, c.Default)) {
			if c.Default != nil {
				alter(fmt.Sprintf("ALTER COLUMN %s SET DEFAULT %s", col, *c.Default))
			} else if !typeChanged && o.Default != nil {
				alter(fmt.Sprintf("ALTER COLUMN %s DROP DEFAULT", col))
			}
		}
		if c.Identity != "" && (o.Identity == "" || typeChanged || !identityEqual(o.IdentityOpts, c.IdentityOpts) || o.Identity != c.Identity) {
			if o.Identity == "" && o.Default != nil && !typeChanged {
				alter(fmt.Sprintf("ALTER COLUMN %s DROP DEFAULT", col))
			}
			alter(fmt.Sprintf("ALTER COLUMN %s ADD%s", col, identityClause(c)))
		}
		if c.GeneratedExpr != "" && c.GeneratedExpr != o.GeneratedExpr {
			if o.GeneratedExpr == "" {
				return nil, fmt.Errorf("column %q: PostgreSQL can't turn an existing column into a generated column; add a new column instead", c.Name)
			}
			alter(fmt.Sprintf("ALTER COLUMN %s SET EXPRESSION AS (%s)", col, c.GeneratedExpr))
		}
	}

	if pkChanged && len(newPK) > 0 {
		alter("ADD " + pkDef(nw.PrimaryKey))
	}
	for _, u := range nw.Uniques {
		if o, ok := findBy(old.Uniques, u.OrigName, func(x Unique) string { return x.Name }); ok && u.OrigName != "" && o.Name == u.Name && equalStrings(rn.unique(o).Columns, u.Columns) {
			continue
		}
		alter(fmt.Sprintf("ADD CONSTRAINT %s UNIQUE (%s)", pgIdent(u.Name), quoteList(u.Columns, pgIdent)))
	}
	for _, fk := range nw.ForeignKeys {
		if o, ok := findBy(old.ForeignKeys, fk.OrigName, func(f ForeignKey) string { return f.Name }); ok && fk.OrigName != "" && sameFK(rn.fk(o), fk) {
			continue
		}
		alter("ADD " + g.fkDef(nw, fk))
	}
	for _, ck := range nw.Checks {
		if o, ok := findBy(old.Checks, ck.OrigName, func(c Check) string { return c.Name }); ok && ck.OrigName != "" && o.Expr == ck.Expr && o.Name == ck.Name {
			continue
		}
		alter(fmt.Sprintf("ADD CONSTRAINT %s CHECK (%s)", pgIdent(ck.Name), ck.Expr))
	}
	for _, ix := range nw.Indexes {
		if o, ok := findBy(old.Indexes, ix.OrigName, func(i Index) string { return i.Name }); ok && ix.OrigName != "" && sameIndex(rn.index(o), ix) {
			continue
		}
		out = append(out, g.indexDDL(nw, ix))
	}
	if old.Options.Unlogged != nw.Options.Unlogged {
		if nw.Options.Unlogged {
			alter("SET UNLOGGED")
		} else {
			alter("SET LOGGED")
		}
	}
	if old.Options.Tablespace != nw.Options.Tablespace && nw.Options.Tablespace != "" {
		alter("SET TABLESPACE " + pgIdent(nw.Options.Tablespace))
	}
	out = append(out, g.comments(nw, &old)...)
	for _, tr := range nw.Triggers {
		if o, ok := findBy(old.Triggers, tr.OrigName, func(x Trigger) string { return x.Name }); ok && tr.OrigName != "" && sameTrigger(o, tr) {
			continue
		}
		out = append(out, g.triggerDDL(nw, tr))
	}
	return out, nil
}

// ColumnOrderChanged reports whether surviving columns were reordered, which
// PostgreSQL can't do in place (the UI warns about it).
func ColumnOrderChanged(old, nw Table) bool {
	var a, b []string
	for _, c := range old.Columns {
		if _, ok := findBy(nw.Columns, c.Name, func(x Column) string { return x.OrigName }); ok {
			a = append(a, c.Name)
		}
	}
	for _, c := range nw.Columns {
		if c.OrigName != "" {
			b = append(b, c.OrigName)
		}
	}
	return !equalStrings(a, b)
}
