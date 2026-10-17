package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// Condition is one `where` entry; conditions are joined with AND.
type Condition struct {
	Column string `json:"column"`
	Op     string `json:"op" jsonschema:"one of = != < <= > >= like not_like in not_in between is_null is_not_null"`
	Value  any    `json:"value,omitempty" jsonschema:"the value to compare with: a list for in/not_in, two values for between, nothing for is_null/is_not_null. Pass big integers as strings."`
}

// Order sorts by one column.
type Order struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc,omitempty"`
}

var compareOps = map[string]string{"=": "=", "!=": "<>", "<": "<", "<=": "<=", ">": ">", ">=": ">=", "like": "LIKE", "not_like": "NOT LIKE"}

// stmt is SQL with bind arguments.
type stmt struct {
	SQL  string
	Args []any
}

// builder renders statements for one table, checking every column name
// against the table so nothing the agent sends ends up in the SQL unquoted.
type builder struct {
	d     driver.Dialect
	ref   model.ObjectRef
	cols  map[string]model.Column
	order []string // column names in table order
}

func newBuilder(d driver.Dialect, t model.TableDetail) *builder {
	b := &builder{d: d, ref: t.ObjectRef, cols: map[string]model.Column{}}
	for _, c := range t.Columns {
		b.cols[c.Name] = c
		b.order = append(b.order, c.Name)
	}
	return b
}

func (b *builder) column(name string) (string, error) {
	if _, ok := b.cols[name]; !ok {
		return "", fmt.Errorf("table %s has no column %q (columns: %s)", b.ref.Name, name, strings.Join(b.order, ", "))
	}
	return b.d.QuoteIdent(name), nil
}

// bind converts a JSON value from the agent into a bind argument for column.
// Values are passed as text, like grid edits, and the server converts them to
// the column type.
func (b *builder) bind(column string, v any) (any, error) {
	if list, ok := v.([]any); ok && b.isArray(column) {
		return b.pgArray(list)
	}
	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		return x, nil
	case bool:
		if b.d.Name == "postgres" {
			return strconv.FormatBool(x), nil
		}
		if x {
			return "1", nil
		}
		return "0", nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case json.Number:
		return x.String(), nil
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case map[string]any, []any:
		data, err := json.Marshal(x)
		return string(data), err
	}
	return nil, fmt.Errorf("unsupported value %v (%T)", v, v)
}

// isArray reports whether column is a PostgreSQL array (text[], int4[]…).
func (b *builder) isArray(column string) bool {
	c := b.cols[column]
	return b.d.Name == "postgres" && (strings.HasSuffix(c.Type, "]") || strings.HasPrefix(c.DataType, "_"))
}

// pgArray renders a JSON list as a PostgreSQL array literal such as
// {"a","b\"c",NULL}; nested lists become nested arrays.
func (b *builder) pgArray(list []any) (string, error) {
	parts := make([]string, len(list))
	for i, v := range list {
		if v == nil {
			parts[i] = "NULL"
			continue
		}
		if sub, ok := v.([]any); ok {
			p, err := b.pgArray(sub)
			if err != nil {
				return "", err
			}
			parts[i] = p
			continue
		}
		a, err := b.bind("", v)
		if err != nil {
			return "", err
		}
		e := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(a.(string))
		parts[i] = `"` + e + `"`
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func (b *builder) where(conds []Condition, args *[]any) (string, error) {
	var parts []string
	add := func(column string, v any) (string, error) {
		a, err := b.bind(column, v)
		if err != nil {
			return "", err
		}
		*args = append(*args, a)
		return b.d.Placeholder(len(*args)), nil
	}
	for _, c := range conds {
		col, err := b.column(c.Column)
		if err != nil {
			return "", err
		}
		op := strings.ToLower(strings.TrimSpace(c.Op))
		switch op {
		case "is_null":
			parts = append(parts, col+" IS NULL")
		case "is_not_null":
			parts = append(parts, col+" IS NOT NULL")
		case "in", "not_in":
			list, ok := c.Value.([]any)
			if !ok || len(list) == 0 {
				return "", fmt.Errorf("%s on %s needs a non-empty list of values", op, c.Column)
			}
			ph := make([]string, len(list))
			for i, v := range list {
				if ph[i], err = add(c.Column, v); err != nil {
					return "", err
				}
			}
			kw := " IN ("
			if op == "not_in" {
				kw = " NOT IN ("
			}
			parts = append(parts, col+kw+strings.Join(ph, ", ")+")")
		case "between":
			list, ok := c.Value.([]any)
			if !ok || len(list) != 2 {
				return "", fmt.Errorf("between on %s needs two values", c.Column)
			}
			lo, err := add(c.Column, list[0])
			if err != nil {
				return "", err
			}
			hi, err := add(c.Column, list[1])
			if err != nil {
				return "", err
			}
			parts = append(parts, col+" BETWEEN "+lo+" AND "+hi)
		default:
			sqlOp, ok := compareOps[op]
			if !ok {
				return "", fmt.Errorf("unknown operator %q", c.Op)
			}
			if c.Value == nil {
				return "", fmt.Errorf("%s on %s needs a value (use is_null to match NULL)", c.Op, c.Column)
			}
			ph, err := add(c.Column, c.Value)
			if err != nil {
				return "", err
			}
			parts = append(parts, col+" "+sqlOp+" "+ph)
		}
	}
	return strings.Join(parts, " AND "), nil
}

func (b *builder) selectRows(columns []string, conds []Condition, order []Order, limit, offset int) (stmt, error) {
	var args []any
	list := "*"
	if len(columns) > 0 {
		q := make([]string, len(columns))
		for i, c := range columns {
			var err error
			if q[i], err = b.column(c); err != nil {
				return stmt{}, err
			}
		}
		list = strings.Join(q, ", ")
	}
	sql := "SELECT " + list + " FROM " + b.d.Qualified(b.ref)
	w, err := b.where(conds, &args)
	if err != nil {
		return stmt{}, err
	}
	if w != "" {
		sql += " WHERE " + w
	}
	if len(order) > 0 {
		parts := make([]string, len(order))
		for i, o := range order {
			col, err := b.column(o.Column)
			if err != nil {
				return stmt{}, err
			}
			parts[i] = col
			if o.Desc {
				parts[i] += " DESC"
			}
		}
		sql += " ORDER BY " + strings.Join(parts, ", ")
	}
	sql += fmt.Sprintf(" LIMIT %d", limit)
	if offset > 0 {
		sql += fmt.Sprintf(" OFFSET %d", offset)
	}
	return stmt{sql, args}, nil
}

func (b *builder) count(conds []Condition) (stmt, error) {
	var args []any
	w, err := b.where(conds, &args)
	if err != nil {
		return stmt{}, err
	}
	return stmt{"SELECT COUNT(*) FROM " + b.d.Qualified(b.ref) + " WHERE " + w, args}, nil
}

// primaryKey lists the primary key columns an insert reports back.
func (b *builder) primaryKey() []string {
	var pk []string
	for _, n := range b.order {
		if b.cols[n].PrimaryKey {
			pk = append(pk, n)
		}
	}
	return pk
}

func (b *builder) insert(row map[string]any) (stmt, error) {
	var args []any
	names := slices.Sorted(maps.Keys(row))
	table := b.d.Qualified(b.ref)
	var sql string
	if len(names) == 0 {
		if b.d.Name == "mysql" {
			sql = "INSERT INTO " + table + " () VALUES ()"
		} else {
			sql = "INSERT INTO " + table + " DEFAULT VALUES"
		}
	} else {
		cols := make([]string, len(names))
		ph := make([]string, len(names))
		for i, n := range names {
			col, err := b.column(n)
			if err != nil {
				return stmt{}, err
			}
			a, err := b.bind(n, row[n])
			if err != nil {
				return stmt{}, err
			}
			args = append(args, a)
			cols[i], ph[i] = col, b.d.Placeholder(len(args))
		}
		sql = fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "), strings.Join(ph, ", "))
	}
	if pk := b.primaryKey(); len(pk) > 0 && b.d.Name != "mysql" {
		q := make([]string, len(pk))
		for i, c := range pk {
			q[i] = b.d.QuoteIdent(c)
		}
		sql += " RETURNING " + strings.Join(q, ", ")
	}
	return stmt{sql, args}, nil
}

// maxPinned is how many rows an update or delete pins by primary key. Above it,
// or without a primary key, the approved count is checked again instead.
const maxPinned = 1000

// keySet holds the primary keys of the rows a change was approved for.
type keySet struct {
	cols []string
	rows [][]string
}

// keys reads the primary keys of the rows matching conds. It returns nil when
// the table has no primary key, a key is NULL, or more than maxPinned rows match.
func (b *builder) keys(ctx context.Context, ds driver.Session, conds []Condition) (*keySet, error) {
	pk := b.primaryKey()
	if len(pk) == 0 {
		return nil, nil
	}
	var args []any
	w, err := b.where(conds, &args)
	if err != nil {
		return nil, err
	}
	cols := make([]string, len(pk))
	for i, c := range pk {
		cols[i] = b.d.QuoteIdent(c)
	}
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s LIMIT %d", strings.Join(cols, ", "), b.d.Qualified(b.ref), w, maxPinned+1)
	rows, err := ds.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	k := &keySet{cols: pk}
	for {
		cells := make([]*string, len(pk))
		if !rows.Next(cells) {
			break
		}
		row := make([]string, len(pk))
		for i, c := range cells {
			if c == nil {
				return nil, rows.Err()
			}
			row[i] = *c
		}
		if k.rows = append(k.rows, row); len(k.rows) > maxPinned {
			return nil, rows.Err()
		}
	}
	return k, rows.Err()
}

// only limits a WHERE clause to the rows in k.
func (b *builder) only(w string, k *keySet, args *[]any) string {
	if k == nil {
		return w
	}
	if len(k.rows) == 0 {
		return w + " AND 1 = 0"
	}
	ph := func(v string) string {
		*args = append(*args, v)
		return b.d.Placeholder(len(*args))
	}
	if len(k.cols) == 1 {
		col := b.d.QuoteIdent(k.cols[0])
		list := make([]string, len(k.rows))
		for i, r := range k.rows {
			list[i] = ph(r[0])
		}
		return w + " AND " + col + " IN (" + strings.Join(list, ", ") + ")"
	}
	ors := make([]string, len(k.rows))
	for i, r := range k.rows {
		ands := make([]string, len(k.cols))
		for j, c := range k.cols {
			ands[j] = b.d.QuoteIdent(c) + " = " + ph(r[j])
		}
		ors[i] = "(" + strings.Join(ands, " AND ") + ")"
	}
	return w + " AND (" + strings.Join(ors, " OR ") + ")"
}

// list shows the pinned keys under the statement in the approval dialog.
func (k *keySet) list() string {
	if k == nil || len(k.rows) == 0 {
		return ""
	}
	shown := make([]string, 0, 5)
	for _, r := range k.rows[:min(5, len(k.rows))] {
		if len(r) == 1 {
			shown = append(shown, r[0])
		} else {
			shown = append(shown, "("+strings.Join(r, ", ")+")")
		}
	}
	s := "\n-- " + strings.Join(k.cols, ", ") + ": " + strings.Join(shown, ", ")
	if len(k.rows) > 5 {
		s += fmt.Sprintf(" and %d more", len(k.rows)-5)
	}
	return s
}

// update changes the rows matching conds, limited to only when it's set.
func (b *builder) update(set map[string]any, conds []Condition, only *keySet) (stmt, error) {
	if len(set) == 0 {
		return stmt{}, errors.New("set needs at least one column")
	}
	if len(conds) == 0 {
		return stmt{}, errors.New("where needs at least one condition; to change every row, match on a column that every row satisfies")
	}
	var args []any
	var sets []string
	for _, n := range slices.Sorted(maps.Keys(set)) {
		col, err := b.column(n)
		if err != nil {
			return stmt{}, err
		}
		a, err := b.bind(n, set[n])
		if err != nil {
			return stmt{}, err
		}
		args = append(args, a)
		sets = append(sets, col+" = "+b.d.Placeholder(len(args)))
	}
	w, err := b.where(conds, &args)
	if err != nil {
		return stmt{}, err
	}
	w = b.only(w, only, &args)
	return stmt{fmt.Sprintf("UPDATE %s SET %s WHERE %s", b.d.Qualified(b.ref), strings.Join(sets, ", "), w), args}, nil
}

// delete removes the rows matching conds, limited to only when it's set.
func (b *builder) delete(conds []Condition, only *keySet) (stmt, error) {
	if len(conds) == 0 {
		return stmt{}, errors.New("where needs at least one condition")
	}
	var args []any
	w, err := b.where(conds, &args)
	if err != nil {
		return stmt{}, err
	}
	w = b.only(w, only, &args)
	return stmt{"DELETE FROM " + b.d.Qualified(b.ref) + " WHERE " + w, args}, nil
}

// display renders a statement with its values inlined, for the approval dialog
// and the activity log only; it is never executed. Placeholders ($n on
// PostgreSQL, ? elsewhere) are the only ones outside quoted identifiers, since
// values never appear in the SQL; a doubled quote inside a name ends and
// restarts the quoting, so it needs no special case.
func display(d driver.Dialect, s stmt) string {
	lit := func(a any) string {
		if a == nil {
			return "NULL"
		}
		return "'" + strings.ReplaceAll(fmt.Sprint(a), "'", "''") + "'"
	}
	i := 0
	var out strings.Builder
	inQuote := byte(0)
	for j := 0; j < len(s.SQL); j++ {
		ch := s.SQL[j]
		switch {
		case inQuote != 0:
			if ch == inQuote {
				inQuote = 0
			}
		case ch == '`' || ch == '"':
			inQuote = ch
		case d.Name == "postgres" && ch == '$':
			k := j + 1
			for k < len(s.SQL) && s.SQL[k] >= '0' && s.SQL[k] <= '9' {
				k++
			}
			if n, err := strconv.Atoi(s.SQL[j+1 : k]); err == nil && n >= 1 && n <= len(s.Args) {
				out.WriteString(lit(s.Args[n-1]))
				j = k - 1
				continue
			}
		case d.Name != "postgres" && ch == '?' && i < len(s.Args):
			out.WriteString(lit(s.Args[i]))
			i++
			continue
		}
		out.WriteByte(ch)
	}
	return out.String()
}

// readOnlyStart lists the statements run_query accepts, per engine. PRAGMA is
// left out because it can switch SQLite's query_only guard off.
var readOnlyStart = map[string][]string{
	"mysql":    {"SELECT", "WITH", "EXPLAIN", "SHOW", "DESCRIBE", "DESC", "VALUES", "TABLE"},
	"postgres": {"SELECT", "WITH", "EXPLAIN", "SHOW", "VALUES", "TABLE"},
	"sqlite":   {"SELECT", "WITH", "EXPLAIN", "VALUES"},
}

// checkReadOnly accepts exactly one read statement. The read-only transaction
// it then runs in is what really stops writes; this keeps out statements that
// would end that transaction (MySQL DDL commits implicitly) or write files.
var modifyingRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|MERGE)\b`)

// limitRead lets the server stop after n rows of a read statement instead of planning and
// sorting the whole result for run_query to cut. MySQL gets a statement-scoped
// sql_select_limit hint after a leading SELECT (a derived table would refuse duplicate
// column names and could drop the ORDER BY; MariaDB reads the hint as a comment).
// PostgreSQL SELECT and WITH are wrapped in an outer LIMIT. SQLite steps rows lazily in
// process, so stopping early already costs nothing there.
func limitRead(d driver.Dialect, sql string, n int) string {
	switch d.Name {
	case "mysql":
		if len(sql) > 6 && strings.EqualFold(sql[:6], "SELECT") && unicode.IsSpace(rune(sql[6])) {
			hint := fmt.Sprintf("SET_VAR(sql_select_limit=%d)", n)
			// Only the first hint block after the keyword counts, so join one that's there.
			if rest := strings.TrimLeftFunc(sql[6:], unicode.IsSpace); strings.HasPrefix(rest, "/*+") {
				return sql[:6] + " /*+ " + hint + " " + strings.TrimLeftFunc(rest[3:], unicode.IsSpace)
			}
			return sql[:6] + " /*+ " + hint + " */" + sql[6:]
		}
	case "postgres":
		// A WITH that changes data must stay at the top level, where the read-only
		// transaction refuses it with a clear error.
		switch kw := sqlcommon.LeadingKeyword(sql); {
		case kw == "SELECT", kw == "WITH" && !modifyingRe.MatchString(sql):
			return fmt.Sprintf("SELECT * FROM (\n%s\n) q LIMIT %d", sql, n)
		}
	}
	return sql
}

func checkReadOnly(d driver.Dialect, sql string) (string, error) {
	stmts := sqlsplit.Split(sql, d.Split)
	if len(stmts) != 1 {
		return "", fmt.Errorf("run_query takes exactly one statement (got %d)", len(stmts))
	}
	s := strings.TrimSpace(stmts[0].SQL)
	kw := sqlcommon.LeadingKeyword(s)
	if !slices.Contains(readOnlyStart[d.Name], kw) {
		return "", fmt.Errorf("run_query only runs read statements (%s); use the row tools to change data", strings.Join(readOnlyStart[d.Name], ", "))
	}
	if d.Name == "mysql" {
		if err := sqlcommon.MySQLReadHazard(s); err != nil {
			return "", fmt.Errorf("run_query: %w", err)
		}
	}
	return s, nil
}
