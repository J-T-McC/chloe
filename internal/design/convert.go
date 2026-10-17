package design

import (
	"cmp"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"chloe/internal/sqlq"
)

// Convert rewrites a table loaded from one engine (dialect "mysql", "postgres" or
// "sqlite") so the other engine's generator can create it: column types, defaults,
// auto increment and indexes are mapped, and what has no equivalent is dropped
// with a note. Converting to the same dialect returns the table unchanged apart
// from clearing engine-specific placement (database, schema).
func Convert(t Table, from, to string) (Table, []string) {
	c := converter{from: from, to: to, table: t.Name}
	out := t
	out.OrigName = ""
	out.Columns = make([]Column, len(t.Columns))
	for i, col := range t.Columns {
		out.Columns[i] = c.column(t, col)
	}
	out.Indexes = c.indexes(t.Indexes)
	out.Uniques = append([]Unique(nil), t.Uniques...)
	out.ForeignKeys = c.foreignKeys(t.ForeignKeys)
	out.Checks = c.checks(t.Checks)
	out.Triggers = c.triggers(t.Triggers)
	out.Options = c.options(t.Options)
	if from != to {
		if to == "mysql" {
			// MySQL can't index TEXT/BLOB columns without a prefix length.
			keyed := keyColumns(out)
			for i, col := range out.Columns {
				if keyed[col.Name] {
					out.Columns[i] = indexableMySQL(col)
				}
			}
		}
		if to == "mysql" && len(out.Uniques) > 0 {
			// MySQL has no unique constraints apart from unique indexes.
			for _, u := range out.Uniques {
				ix := Index{Name: u.Name, Kind: "unique"}
				for _, n := range u.Columns {
					ix.Columns = append(ix.Columns, IndexColumn{Name: n})
				}
				out.Indexes = append(out.Indexes, ix)
			}
			out.Uniques = nil
		}
		if to != "sqlite" {
			nameConstraints(&out)
		}
		if out.PrimaryKey != nil {
			// Key columns can't be NULL outside SQLite (which allows it for legacy reasons).
			for i, col := range out.Columns {
				if slicesContains(out.PrimaryKey.Columns, col.Name) {
					out.Columns[i].Nullable = false
				}
			}
		}
		if to != "postgres" && out.PrimaryKey != nil {
			// Named only in PostgreSQL; unnamed keeps SQLite's single-column key inline (INTEGER PRIMARY KEY).
			pk := *out.PrimaryKey
			pk.Name = ""
			out.PrimaryKey = &pk
		}
	}
	return out, c.notes
}

type converter struct {
	from, to, table string
	notes           []string
}

func (c *converter) note(format string, args ...any) {
	c.notes = append(c.notes, c.table+": "+fmt.Sprintf(format, args...))
}

func (c *converter) same() bool { return c.from == c.to }

// ---- types -------------------------------------------------------------------------------

// kind is an engine-neutral column type.
type kind struct {
	name          string // bool, int8, int16, int32, int64, decimal, float32, float64, char, varchar, text, binary, varbinary, blob, date, time, datetime, timestamptz, json, uuid
	length, scale string
	unsigned      bool
}

var sizeRe = regexp.MustCompile(`^\d+$`)

func (c *converter) sourceKind(col Column) kind {
	t := CanonType(col.Type)
	switch c.from {
	case "mysql":
		return mysqlKind(col, t)
	case "postgres":
		return pgKind(col, t)
	default:
		return sqliteKind(col, t)
	}
}

func mysqlKind(col Column, t string) kind {
	k := kind{length: col.Length, scale: col.Scale, unsigned: col.Unsigned}
	switch t {
	case "tinyint":
		if col.Length == "1" && !col.Unsigned {
			k.name, k.length = "bool", ""
		} else {
			k.name, k.length = "int8", ""
		}
	case "boolean":
		k.name = "bool"
	case "smallint", "year":
		k.name, k.length = "int16", ""
	case "mediumint", "integer":
		k.name, k.length = "int32", ""
	case "bigint":
		k.name, k.length = "int64", ""
	case "numeric":
		k.name = "decimal"
	case "float", "real":
		k.name, k.length, k.scale = "float32", "", ""
	case "double precision":
		k.name, k.length, k.scale = "float64", "", ""
	case "bit":
		if col.Length == "" || col.Length == "1" {
			k.name, k.length = "bool", ""
		} else {
			k.name, k.length = "int64", ""
		}
	case "char":
		k.name = "char"
	case "varchar":
		k.name = "varchar"
	case "tinytext", "text", "mediumtext", "longtext":
		k.name, k.length = "text", ""
	case "binary":
		k.name = "binary"
	case "varbinary":
		k.name = "varbinary"
	case "tinyblob", "blob", "mediumblob", "longblob":
		k.name, k.length = "blob", ""
	case "date":
		k.name = "date"
	case "time":
		k.name = "time"
	case "datetime", "timestamp":
		k.name = "datetime"
	case "json":
		k.name = "json"
	case "enum", "set":
		// The longest allowed value bounds the column.
		n := 1
		for _, v := range splitEnum(col.Length) {
			if l := len(v); l > n {
				n = l
			}
		}
		if t == "set" {
			n = len(strings.Join(splitEnum(col.Length), ","))
		}
		k.name, k.length = "varchar", strconv.Itoa(n)
	case "geometry", "point", "linestring", "polygon", "multipoint", "multilinestring", "multipolygon", "geometrycollection":
		k.name, k.length = "blob", ""
	default:
		k.name, k.length, k.scale = "text", "", ""
	}
	return k
}

func pgKind(col Column, t string) kind {
	k := kind{length: col.Length, scale: col.Scale}
	if strings.HasSuffix(t, "[]") {
		return kind{name: "text"}
	}
	switch t {
	case "boolean":
		k.name = "bool"
	case "smallint", "smallserial":
		k.name = "int16"
	case "integer", "serial":
		k.name = "int32"
	case "bigint", "bigserial":
		k.name = "int64"
	case "numeric":
		k.name = "decimal"
	case "money":
		k.name, k.length, k.scale = "decimal", "19", "2"
	case "real":
		k.name = "float32"
	case "double precision":
		k.name = "float64"
	case "char":
		k.name = "char"
	case "varchar":
		k.name = "varchar"
	case "text", "citext", "xml", "tsvector", "tsquery":
		k.name, k.length = "text", ""
	case "bytea":
		k.name, k.length = "blob", ""
	case "date":
		k.name = "date"
	case "time", "timetz":
		k.name = "time"
	case "timestamp":
		k.name = "datetime"
	case "timestamptz":
		k.name = "timestamptz"
	case "json", "jsonb":
		k.name, k.length = "json", ""
	case "uuid":
		k.name, k.length = "uuid", ""
	case "interval", "inet", "cidr", "macaddr", "macaddr8":
		k.name, k.length = "varchar", "64"
	case "bit", "varbit":
		if col.Length == "1" {
			k.name, k.length = "bool", ""
		} else {
			k.name, k.length = "varchar", col.Length
		}
	default:
		k.name, k.length, k.scale = "text", "", ""
	}
	if k.name != "decimal" && k.name != "char" && k.name != "varchar" && k.name != "datetime" && k.name != "timestamptz" && k.name != "time" {
		k.length, k.scale = "", ""
	}
	if (k.name == "datetime" || k.name == "timestamptz" || k.name == "time") && k.length == "" {
		k.length = "6" // PostgreSQL's default precision
	}
	return k
}

// sqliteKind follows SQLite's type affinity rules, recognising common declared names first.
func sqliteKind(col Column, t string) kind {
	k := kind{length: col.Length, scale: col.Scale}
	switch t {
	case "boolean":
		return kind{name: "bool"}
	case "date":
		return kind{name: "date"}
	case "datetime", "timestamp":
		return kind{name: "datetime"}
	case "time":
		return kind{name: "time"}
	case "json":
		return kind{name: "json"}
	case "char", "varchar":
		if sizeRe.MatchString(col.Length) {
			k.name = t
			k.scale = ""
			return k
		}
		return kind{name: "text"}
	case "numeric":
		k.name = "decimal"
		return k
	case "double precision", "real", "float":
		return kind{name: "float64"}
	}
	u := strings.ToUpper(t)
	switch {
	case strings.Contains(u, "INT"):
		if t == "integer" || t == "bigint" || t == "int8" {
			return kind{name: "int64"}
		}
		if t == "smallint" || t == "tinyint" {
			return kind{name: "int16"}
		}
		return kind{name: "int64"}
	case strings.Contains(u, "CHAR"), strings.Contains(u, "CLOB"), strings.Contains(u, "TEXT"):
		return kind{name: "text"}
	case u == "" || strings.Contains(u, "BLOB"):
		return kind{name: "blob"}
	case strings.Contains(u, "REAL"), strings.Contains(u, "FLOA"), strings.Contains(u, "DOUB"):
		return kind{name: "float64"}
	}
	return kind{name: "decimal", length: col.Length, scale: col.Scale}
}

// targetType renders a kind for the target dialect as (type, length, scale).
func (c *converter) targetType(k kind) (string, string, string) {
	switch c.to {
	case "mysql":
		switch k.name {
		case "bool":
			return "tinyint", "1", ""
		case "int8":
			return "tinyint", "", ""
		case "int16":
			return "smallint", "", ""
		case "int32":
			if k.unsigned {
				return "bigint", "", ""
			}
			return "int", "", ""
		case "int64":
			if k.unsigned {
				return "decimal", "20", "0"
			}
			return "bigint", "", ""
		case "decimal":
			if k.length == "" {
				return "decimal", "65", "30"
			}
			return "decimal", k.length, k.scale
		case "float32":
			return "float", "", ""
		case "float64":
			return "double", "", ""
		case "char":
			return "char", cmp.Or(k.length, "1"), ""
		case "varchar":
			if k.length == "" {
				return "longtext", "", ""
			}
			return "varchar", k.length, ""
		case "text", "json":
			if k.name == "json" {
				return "json", "", ""
			}
			return "longtext", "", ""
		case "binary":
			return "binary", cmp.Or(k.length, "1"), ""
		case "varbinary":
			return "varbinary", cmp.Or(k.length, "255"), ""
		case "blob":
			return "longblob", "", ""
		case "date":
			return "date", "", ""
		case "time":
			return "time", fsp(k.length), ""
		case "datetime", "timestamptz":
			return "datetime", fsp(k.length), ""
		case "uuid":
			return "char", "36", ""
		}
	case "postgres":
		switch k.name {
		case "bool":
			return "boolean", "", ""
		case "int8", "int16":
			if k.unsigned && k.name == "int16" {
				return "integer", "", ""
			}
			return "smallint", "", ""
		case "int32":
			if k.unsigned {
				return "bigint", "", ""
			}
			return "integer", "", ""
		case "int64":
			// Unsigned too: numeric(20,0) can't be an identity, and foreign keys
			// to a bigint id need the same type. Values above 2^63-1 won't fit.
			return "bigint", "", ""
		case "decimal":
			return "numeric", k.length, k.scale
		case "float32":
			return "real", "", ""
		case "float64":
			return "double precision", "", ""
		case "char":
			return "character", cmp.Or(k.length, "1"), ""
		case "varchar":
			return "character varying", k.length, ""
		case "text":
			return "text", "", ""
		case "binary", "varbinary", "blob":
			return "bytea", "", ""
		case "date":
			return "date", "", ""
		case "time":
			return "time without time zone", pgPrecision(k.length), ""
		case "datetime":
			return "timestamp without time zone", pgPrecision(k.length), ""
		case "timestamptz":
			return "timestamp with time zone", pgPrecision(k.length), ""
		case "json":
			return "jsonb", "", ""
		case "uuid":
			return "uuid", "", ""
		}
	case "sqlite":
		switch k.name {
		case "bool":
			return "BOOLEAN", "", ""
		case "int8", "int16", "int32", "int64":
			return "INTEGER", "", ""
		case "decimal":
			return "NUMERIC", k.length, k.scale
		case "float32", "float64":
			return "REAL", "", ""
		case "char", "varchar":
			if k.length == "" {
				return "TEXT", "", ""
			}
			return strings.ToUpper(k.name), k.length, ""
		case "text", "uuid", "json":
			return "TEXT", "", ""
		case "binary", "varbinary", "blob":
			return "BLOB", "", ""
		case "date":
			return "DATE", "", ""
		case "time":
			return "TIME", "", ""
		case "datetime", "timestamptz":
			return "DATETIME", "", ""
		}
	}
	return "text", "", ""
}

// fsp is a MySQL fractional-seconds precision (0–6).
func fsp(p string) string {
	if n, err := strconv.Atoi(p); err == nil && n >= 0 {
		if n > 6 {
			n = 6
		}
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	return ""
}

// pgPrecision drops PostgreSQL's default precision (6) so the type reads naturally.
func pgPrecision(p string) string {
	if p == "6" {
		return ""
	}
	return p
}

// ---- columns -----------------------------------------------------------------------------

func (c *converter) column(t Table, col Column) Column {
	if c.same() {
		// A serial's default names the source's sequence: the copy gets its own identity.
		if c.from == "postgres" && col.Identity == "" && col.Default != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(*col.Default)), "nextval(") {
			col.Default, col.Identity = nil, "by default"
		}
		return col
	}
	out := col
	out.OrigName = ""
	k := c.sourceKind(col)
	out.Type, out.Length, out.Scale = c.targetType(k)
	out.Unsigned, out.Zerofill, out.OnUpdateNow = false, false, false
	out.Charset, out.Collation = "", ""
	out.Identity, out.IdentityOpts, out.AutoIncrement = "", nil, false
	if c.to != "mysql" && c.to != "postgres" {
		out.Comment = ""
	}
	if col.OnUpdateNow {
		c.note("column %s: ON UPDATE CURRENT_TIMESTAMP has no equivalent and was left out", col.Name)
	}
	if col.Collation != "" || col.Charset != "" {
		// Collation names differ between engines.
		c.note("column %s: collation %s left out", col.Name, strings.TrimSpace(col.Charset+" "+col.Collation))
	}
	if col.GeneratedExpr != "" {
		c.note("column %s: generated column copied as a plain column", col.Name)
		out.GeneratedExpr, out.GeneratedStored = "", false
	}

	auto := c.isAuto(t, col)
	if auto {
		switch c.to {
		case "mysql", "sqlite":
			out.AutoIncrement = true
		case "postgres":
			out.Identity = "by default"
		}
		out.Default = nil
		if c.to == "sqlite" {
			// Only INTEGER PRIMARY KEY can auto-increment.
			out.Type, out.Length, out.Scale = "INTEGER", "", ""
			if t.PrimaryKey == nil || len(t.PrimaryKey.Columns) != 1 || t.PrimaryKey.Columns[0] != col.Name {
				out.AutoIncrement = false
				c.note("column %s: auto increment needs a single-column primary key in SQLite and was left out", col.Name)
			}
		}
		return out
	}
	out.Default = c.defaultValue(col, k, out)
	return out
}

// isAuto reports whether a column is filled in automatically by its engine.
func (c *converter) isAuto(t Table, col Column) bool {
	switch {
	case col.AutoIncrement || col.Identity != "":
		return true
	case col.Default != nil && strings.HasPrefix(strings.ToLower(strings.TrimSpace(*col.Default)), "nextval("):
		return true // PostgreSQL serial
	case c.from == "sqlite":
		// INTEGER PRIMARY KEY is an alias of the rowid.
		return CanonType(col.Type) == "integer" && t.PrimaryKey != nil && len(t.PrimaryKey.Columns) == 1 && t.PrimaryKey.Columns[0] == col.Name
	}
	return false
}

var (
	pgCast      = regexp.MustCompile(`^\((.*)\)::[\w\s".\[\]]+$|^(.*?)::[\w\s".\[\]]+$`)
	numberRe    = regexp.MustCompile(`^[-+]?(\d+\.?\d*|\.\d+)([eE][-+]?\d+)?$`)
	nowRe       = regexp.MustCompile(`(?i)^(current_timestamp|now|localtimestamp|getdate|datetime)\s*(\(\s*(\d*)\s*\)|\('now'(,\s*'localtime')?\))?$`)
	bitLiteral  = regexp.MustCompile(`(?i)^b'([01]+)'$`)
	currentDate = regexp.MustCompile(`(?i)^(current_date|curdate\(\)|date\('now'\))$`)
)

// defaultValue maps a default to the target: literals are re-quoted, the current
// time keeps its meaning, and other expressions are dropped with a note.
func (c *converter) defaultValue(src Column, k kind, dst Column) *string {
	if src.Default == nil {
		return nil
	}
	d := strings.TrimSpace(*src.Default)
	if c.from == "postgres" {
		for {
			m := pgCast.FindStringSubmatch(d)
			if m == nil {
				break
			}
			d = strings.TrimSpace(m[1] + m[2])
		}
	}
	for strings.HasPrefix(d, "(") && strings.HasSuffix(d, ")") && sqlq.Balanced(d[1:len(d)-1], c.from == "mysql") {
		d = strings.TrimSpace(d[1 : len(d)-1])
	}
	set := func(s string) *string { return &s }
	upper := strings.ToUpper(d)
	switch {
	case upper == "NULL":
		return nil
	case upper == "TRUE" || upper == "FALSE":
		if c.to == "postgres" {
			return set(strings.ToLower(d))
		}
		if upper == "TRUE" {
			return set("1")
		}
		return set("0")
	case bitLiteral.MatchString(d):
		v, _ := strconv.ParseInt(bitLiteral.FindStringSubmatch(d)[1], 2, 64)
		return c.boolOrNumber(k, strconv.FormatInt(v, 10))
	case numberRe.MatchString(d):
		return c.boolOrNumber(k, d)
	case nowRe.MatchString(d):
		switch c.to {
		case "mysql":
			if p := fsp(dst.Length); p != "" {
				return set("CURRENT_TIMESTAMP(" + p + ")")
			}
			return set("CURRENT_TIMESTAMP")
		default:
			return set("CURRENT_TIMESTAMP")
		}
	case currentDate.MatchString(d):
		if c.to == "mysql" {
			return set("(CURRENT_DATE)")
		}
		return set("CURRENT_DATE")
	}
	if s, ok := c.parseLiteral(d); ok {
		if k.name == "bool" {
			switch strings.ToLower(s) {
			case "1", "t", "true", "y", "yes", "on":
				return c.boolOrNumber(k, "1")
			case "0", "f", "false", "n", "no", "off":
				return c.boolOrNumber(k, "0")
			}
		}
		if c.to == "mysql" && (dst.Type == "longtext" || dst.Type == "longblob" || dst.Type == "json") {
			// MySQL only takes expression defaults on TEXT/BLOB/JSON.
			return set("(" + c.quote(s) + ")")
		}
		return set(c.quote(s))
	}
	c.note("column %s: default %s left out", src.Name, *src.Default)
	return nil
}

func (c *converter) boolOrNumber(k kind, n string) *string {
	if k.name == "bool" && c.to == "postgres" {
		v := "false"
		if n != "0" && n != "0.0" {
			v = "true"
		}
		return &v
	}
	return &n
}

// parseLiteral reads a quoted string in the source dialect.
func (c *converter) parseLiteral(s string) (string, bool) {
	if len(s) >= 3 && (s[0] == 'E' || s[0] == 'e') && s[1] == '\'' && c.from == "postgres" {
		return unquote(s[1:], true)
	}
	if len(s) < 2 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return "", false
	}
	return unquote(s, c.from == "mysql")
}

func unquote(s string, backslashes bool) (string, bool) {
	var b strings.Builder
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); i++ {
		ch := body[i]
		switch {
		case ch == '\'' && i+1 < len(body) && body[i+1] == '\'':
			b.WriteByte('\'')
			i++
		case ch == '\'':
			return "", false // two literals or an expression
		case ch == '\\' && backslashes && i+1 < len(body):
			i++
			switch body[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			default:
				b.WriteByte(body[i])
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String(), true
}

// quote writes a string literal for the target dialect.
func (c *converter) quote(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	if c.to == "mysql" {
		s = strings.ReplaceAll(s, `\`, `\\`)
	}
	return "'" + s + "'"
}

func splitEnum(list string) []string {
	var out []string
	for i := 0; i < len(list); i++ {
		if list[i] != '\'' {
			continue
		}
		var b strings.Builder
		for i++; i < len(list); i++ {
			if list[i] == '\'' {
				if i+1 < len(list) && list[i+1] == '\'' {
					b.WriteByte('\'')
					i++
					continue
				}
				break
			}
			b.WriteByte(list[i])
		}
		out = append(out, b.String())
	}
	return out
}

// keyColumns lists the columns used by the primary key, indexes, uniques and foreign keys.
func keyColumns(t Table) map[string]bool {
	m := map[string]bool{}
	if t.PrimaryKey != nil {
		for _, n := range t.PrimaryKey.Columns {
			m[n] = true
		}
	}
	for _, ix := range t.Indexes {
		if ix.Kind == "fulltext" || ix.Kind == "spatial" {
			continue
		}
		for _, ic := range ix.Columns {
			if ic.Length == "" {
				m[ic.Name] = true
			}
		}
	}
	for _, u := range t.Uniques {
		for _, n := range u.Columns {
			m[n] = true
		}
	}
	for _, f := range t.ForeignKeys {
		for _, n := range f.Columns {
			m[n] = true
		}
	}
	return m
}

func indexableMySQL(col Column) Column {
	switch col.Type {
	case "longtext", "text", "json":
		col.Type, col.Length = "varchar", "255"
		if col.Default != nil && strings.HasPrefix(*col.Default, "(") {
			d := strings.TrimSuffix(strings.TrimPrefix(*col.Default, "("), ")")
			col.Default = &d
		}
	case "longblob", "blob":
		col.Type, col.Length = "varbinary", "255"
	}
	return col
}

// ---- indexes and constraints -------------------------------------------------------------

func (c *converter) indexes(list []Index) []Index {
	out := make([]Index, 0, len(list))
	for _, ix := range list {
		if c.same() {
			out = append(out, ix)
			continue
		}
		ix.OrigName = ""
		ix.Comment, ix.Invisible = "", false
		skip := false
		for _, ic := range ix.Columns {
			if ic.Expr != "" {
				skip = true
			}
		}
		if skip {
			c.note("index %s: expression index left out", ix.Name)
			continue
		}
		switch ix.Kind {
		case "fulltext", "spatial":
			c.note("index %s: %s index left out", ix.Name, ix.Kind)
			continue
		}
		if ix.Where != "" {
			c.note("index %s: partial index created on all rows", ix.Name)
			ix.Where = ""
		}
		ix.Method = ""
		cols := make([]IndexColumn, len(ix.Columns))
		for i, ic := range ix.Columns {
			if c.to != "mysql" {
				ic.Length = "" // prefix lengths are MySQL's
			}
			cols[i] = ic
		}
		ix.Columns = cols
		out = append(out, ix)
	}
	return out
}

func (c *converter) foreignKeys(list []ForeignKey) []ForeignKey {
	out := make([]ForeignKey, 0, len(list))
	for _, f := range list {
		if !c.same() {
			f.OrigName = ""
			f.RefDatabase, f.RefSchema = "", ""
		}
		out = append(out, f)
	}
	return out
}

func (c *converter) checks(list []Check) []Check {
	if c.same() {
		return append([]Check(nil), list...)
	}
	out := make([]Check, 0, len(list))
	for _, ch := range list {
		// Check expressions are mostly portable; a failing one is reported by the target.
		ch.OrigName = ""
		ch.NotEnforced = false
		out = append(out, ch)
	}
	return out
}

func (c *converter) triggers(list []Trigger) []Trigger {
	if c.same() {
		return append([]Trigger(nil), list...)
	}
	for _, tr := range list {
		c.note("trigger %s left out: trigger bodies are written for one engine", tr.Name)
	}
	return nil
}

func (c *converter) options(o Options) Options {
	if c.same() {
		return o
	}
	return Options{}
}

func slicesContains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// nameConstraints names SQLite's anonymous column constraints the way PostgreSQL would,
// so the target can create and later alter them.
func nameConstraints(t *Table) {
	taken := map[string]bool{}
	for _, u := range t.Uniques {
		taken[u.Name] = true
	}
	for _, c := range t.Checks {
		taken[c.Name] = true
	}
	for _, f := range t.ForeignKeys {
		taken[f.Name] = true
	}
	for _, ix := range t.Indexes {
		taken[ix.Name] = true
	}
	unique := func(base string) string {
		name := base
		for n := 1; taken[name]; n++ {
			name = fmt.Sprintf("%s%d", base, n)
		}
		taken[name] = true
		return name
	}
	for i := range t.Uniques {
		if t.Uniques[i].Name == "" {
			t.Uniques[i].Name = unique(t.Name + "_" + strings.Join(t.Uniques[i].Columns, "_") + "_key")
		}
	}
	for i := range t.Checks {
		if t.Checks[i].Name == "" {
			t.Checks[i].Name = unique(t.Name + "_check")
		}
	}
	for i := range t.ForeignKeys {
		if t.ForeignKeys[i].Name == "" {
			t.ForeignKeys[i].Name = unique(t.Name + "_" + strings.Join(t.ForeignKeys[i].Columns, "_") + "_fkey")
		}
	}
	for i := range t.Indexes {
		if t.Indexes[i].Name == "" {
			cols := make([]string, len(t.Indexes[i].Columns))
			for j, c := range t.Indexes[i].Columns {
				cols[j] = c.Name
			}
			t.Indexes[i].Name = unique(t.Name + "_" + strings.Join(cols, "_") + "_idx")
		}
	}
}
