package design

import (
	"fmt"
	"slices"
	"strings"
)

// findBy returns the first element whose key equals name.
func findBy[T any](list []T, name string, key func(T) string) (T, bool) {
	var zero T
	if name == "" {
		return zero, false
	}
	for _, x := range list {
		if key(x) == name {
			return x, true
		}
	}
	return zero, false
}

func equalStrings(a, b []string) bool { return slices.Equal(a, b) }

func pkCols(pk *PrimaryKey) []string {
	if pk == nil {
		return nil
	}
	return pk.Columns
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func identityEqual(a, b *IdentityOpts) bool {
	if a == nil {
		a = &IdentityOpts{}
	}
	if b == nil {
		b = &IdentityOpts{}
	}
	return *a == *b
}

// typeAliases maps type spellings to one canonical name, so a model saying "int" and
// a server reporting "integer" (or "bool"/"boolean", "decimal"/"numeric") agree.
var typeAliases = map[string]string{
	"int": "integer", "int4": "integer", "integer": "integer",
	"int8": "bigint", "bigint": "bigint",
	"int2": "smallint", "smallint": "smallint",
	"bool": "boolean", "boolean": "boolean",
	"decimal": "numeric", "numeric": "numeric",
	"float8": "double precision", "double precision": "double precision", "double": "double precision",
	"float4": "real", "real": "real",
	"character varying": "varchar", "varchar": "varchar",
	"character": "char", "char": "char", "bpchar": "char",
	"timestamp with time zone": "timestamptz", "timestamptz": "timestamptz",
	"timestamp without time zone": "timestamp", "timestamp": "timestamp",
	"time with time zone": "timetz", "timetz": "timetz",
	"time without time zone": "time", "time": "time",
	"bit varying": "varbit", "varbit": "varbit",
}

// CanonType returns the canonical spelling of a base type (arrays keep their []).
func CanonType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	arr := ""
	for strings.HasSuffix(t, "[]") {
		t, arr = strings.TrimSpace(t[:len(t)-2]), arr+"[]"
	}
	if c, ok := typeAliases[t]; ok {
		t = c
	}
	return t + arr
}

// sameType compares types including modifiers, treating aliases as equal.
func sameType(a, b Column) bool {
	return CanonType(a.Type) == CanonType(b.Type) && a.Length == b.Length && a.Scale == b.Scale
}

// sameColumn compares everything but the name.
func sameColumn(a, b Column) bool {
	return sameType(a, b) && a.Scale == b.Scale && a.Nullable == b.Nullable &&
		strPtrEqual(a.Default, b.Default) && a.Comment == b.Comment && a.AutoIncrement == b.AutoIncrement &&
		a.Unsigned == b.Unsigned && a.Zerofill == b.Zerofill && a.OnUpdateNow == b.OnUpdateNow &&
		a.Charset == b.Charset && a.Collation == b.Collation && a.GeneratedExpr == b.GeneratedExpr &&
		a.GeneratedStored == b.GeneratedStored && a.Identity == b.Identity && identityEqual(a.IdentityOpts, b.IdentityOpts)
}

func sameIndex(a, b Index) bool {
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) && a.Kind == b.Kind &&
		strings.EqualFold(a.Method, b.Method) && a.Where == b.Where && a.Comment == b.Comment && a.Invisible == b.Invisible
}

func sameFK(a, b ForeignKey) bool {
	norm := func(s string) string {
		if s == "" {
			return "NO ACTION"
		}
		return strings.ToUpper(s)
	}
	return a.Name == b.Name && slices.Equal(a.Columns, b.Columns) && a.RefDatabase == b.RefDatabase && a.RefSchema == b.RefSchema &&
		a.RefTable == b.RefTable && slices.Equal(a.RefColumns, b.RefColumns) && norm(a.OnDelete) == norm(b.OnDelete) && norm(a.OnUpdate) == norm(b.OnUpdate)
}

func sameTrigger(a, b Trigger) bool {
	return a.Name == b.Name && a.Timing == b.Timing && slices.Equal(a.Events, b.Events) && strings.TrimSpace(a.Body) == strings.TrimSpace(b.Body) &&
		a.ForEachRow == b.ForEachRow && a.Function == b.Function && a.When == b.When
}

// renamer maps original column names to their new names, so keys and indexes that
// only changed because a column was renamed aren't rebuilt (servers rename references
// in place).
type renamer map[string]string

func renames(old, nw Table) renamer {
	r := renamer{}
	for _, c := range nw.Columns {
		if c.OrigName != "" && c.OrigName != c.Name {
			r[c.OrigName] = c.Name
		}
	}
	return r
}

func (r renamer) cols(cols []string) []string {
	if len(r) == 0 {
		return cols
	}
	out := make([]string, len(cols))
	for i, c := range cols {
		if n, ok := r[c]; ok {
			c = n
		}
		out[i] = c
	}
	return out
}

func (r renamer) index(ix Index) Index {
	if len(r) == 0 {
		return ix
	}
	cols := make([]IndexColumn, len(ix.Columns))
	for i, c := range ix.Columns {
		if n, ok := r[c.Name]; ok && c.Expr == "" {
			c.Name = n
		}
		cols[i] = c
	}
	ix.Columns = cols
	return ix
}

func (r renamer) fk(f ForeignKey) ForeignKey {
	f.Columns = r.cols(f.Columns)
	return f
}

func (r renamer) unique(u Unique) Unique {
	u.Columns = r.cols(u.Columns)
	return u
}

func (r renamer) pk(pk *PrimaryKey) []string { return r.cols(pkCols(pk)) }

// changed splits a kind of table part (indexes, foreign keys…) into the old
// ones to drop, gone or changed, and the new ones to add, new or changed.
// Parts are paired by the new one's OrigName; same compares an old part with
// its new version.
func changed[T any](old, nw []T, name, orig func(T) string, same func(o, n T) bool) (drop, add []T) {
	for _, o := range old {
		if n, ok := findBy(nw, name(o), orig); !ok || !same(o, n) {
			drop = append(drop, o)
		}
	}
	for _, n := range nw {
		if o, ok := findBy(old, orig(n), name); ok && orig(n) != "" && same(o, n) {
			continue
		}
		add = append(add, n)
	}
	return drop, add
}

// tableDiff pairs the parts of a table before and after an edit; parts are
// compared with the old ones' columns renamed to their new names.
type tableDiff struct {
	old, nw Table
	rn      renamer
}

func diffTables(old, nw Table) tableDiff { return tableDiff{old, nw, renames(old, nw)} }

func (d tableDiff) triggers() (drop, add []Trigger) {
	return changed(d.old.Triggers, d.nw.Triggers, func(t Trigger) string { return t.Name }, func(t Trigger) string { return t.OrigName }, sameTrigger)
}

func (d tableDiff) foreignKeys() (drop, add []ForeignKey) {
	return changed(d.old.ForeignKeys, d.nw.ForeignKeys, func(f ForeignKey) string { return f.Name }, func(f ForeignKey) string { return f.OrigName },
		func(o, n ForeignKey) bool { return sameFK(d.rn.fk(o), n) })
}

func (d tableDiff) uniques() (drop, add []Unique) {
	return changed(d.old.Uniques, d.nw.Uniques, func(u Unique) string { return u.Name }, func(u Unique) string { return u.OrigName },
		func(o, n Unique) bool { return o.Name == n.Name && equalStrings(d.rn.unique(o).Columns, n.Columns) })
}

func (d tableDiff) indexes() (drop, add []Index) {
	return changed(d.old.Indexes, d.nw.Indexes, func(i Index) string { return i.Name }, func(i Index) string { return i.OrigName },
		func(o, n Index) bool { return sameIndex(d.rn.index(o), n) })
}

// checks pairs check constraints; enforcement also compares NOT ENFORCED (MySQL).
func (d tableDiff) checks(enforcement bool) (drop, add []Check) {
	return changed(d.old.Checks, d.nw.Checks, func(c Check) string { return c.Name }, func(c Check) string { return c.OrigName },
		func(o, n Check) bool {
			return o.Expr == n.Expr && o.Name == n.Name && (!enforcement || o.NotEnforced == n.NotEnforced)
		})
}

// pk returns the old primary key's columns (renamed) and the new one's.
func (d tableDiff) pk() (old, nw []string) { return d.rn.pk(d.old.PrimaryKey), pkCols(d.nw.PrimaryKey) }

// survivors are the old columns still in the table, in their old order.
func (d tableDiff) survivors() []string {
	var out []string
	for _, c := range d.old.Columns {
		if _, ok := findBy(d.nw.Columns, c.Name, func(x Column) string { return x.OrigName }); ok {
			out = append(out, c.Name)
		}
	}
	return out
}

// droppedColumns are the old columns no longer in the table.
func (d tableDiff) droppedColumns() []Column {
	var out []Column
	for _, c := range d.old.Columns {
		if _, ok := findBy(d.nw.Columns, c.Name, func(x Column) string { return x.OrigName }); !ok {
			out = append(out, c)
		}
	}
	return out
}

// original is the old version of an edited column.
func (d tableDiff) original(c Column) (Column, error) {
	o, ok := findBy(d.old.Columns, c.OrigName, func(x Column) string { return x.Name })
	if !ok {
		return o, fmt.Errorf("column %q refers to unknown original column %q", c.Name, c.OrigName)
	}
	return o, nil
}
