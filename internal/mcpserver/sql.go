package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

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

func (b *builder) update(set map[string]any, conds []Condition) (stmt, error) {
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
	return stmt{fmt.Sprintf("UPDATE %s SET %s WHERE %s", b.d.Qualified(b.ref), strings.Join(sets, ", "), w), args}, nil
}

func (b *builder) delete(conds []Condition) (stmt, error) {
	if len(conds) == 0 {
		return stmt{}, errors.New("where needs at least one condition")
	}
	var args []any
	w, err := b.where(conds, &args)
	if err != nil {
		return stmt{}, err
	}
	return stmt{"DELETE FROM " + b.d.Qualified(b.ref) + " WHERE " + w, args}, nil
}

var placeholderRe = regexp.MustCompile(`\$\d+|\?`)

// display renders a statement with its values inlined, for the approval dialog
// and the activity log only; it is never executed.
func display(d driver.Dialect, s stmt) string {
	lit := func(a any) string {
		if a == nil {
			return "NULL"
		}
		return "'" + strings.ReplaceAll(fmt.Sprint(a), "'", "''") + "'"
	}
	if d.Name == "postgres" {
		return placeholderRe.ReplaceAllStringFunc(s.SQL, func(m string) string {
			if m == "?" {
				return m
			}
			n, _ := strconv.Atoi(m[1:])
			if n >= 1 && n <= len(s.Args) {
				return lit(s.Args[n-1])
			}
			return m
		})
	}
	// Placeholders are the only ? outside literals: identifiers are quoted with
	// backticks or double quotes, and values never appear in the SQL.
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
		case ch == '?' && i < len(s.Args):
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

var outfileRe = regexp.MustCompile(`(?i)\bINTO\s+(OUTFILE|DUMPFILE)\b`)

// checkReadOnly accepts exactly one read statement. The read-only transaction
// it then runs in is what really stops writes; this keeps out statements that
// would end that transaction (MySQL DDL commits implicitly) or write files.
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
	if d.Name == "mysql" && outfileRe.MatchString(s) {
		return "", errors.New("run_query can't write files")
	}
	return s, nil
}
