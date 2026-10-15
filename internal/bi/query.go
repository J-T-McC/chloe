package bi

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"chloe/internal/driver"
	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// Stmt is SQL with its bind arguments.
type Stmt struct {
	SQL  string
	Args []any
}

// now is the clock relative filters use; tests replace it.
var now = time.Now

// Column describes a column of a chart's data.
type Column struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Role is dimension, series, value, secondary, target or column (table charts).
	Role      string `json:"role"`
	Field     string `json:"field"`
	Aggregate string `json:"aggregate,omitempty"`
	// Type is string, number or datetime; a bucketed date is a string whose
	// Aggregate names the bucket.
	Type string `json:"type"`
}

// FilterOps are the filter operators and how many values each takes (-1: a list).
var FilterOps = map[string]int{
	"=": 1, "!=": 1, "<": 1, "<=": 1, ">": 1, ">=": 1,
	"contains": 1, "not_contains": 1, "begins_with": 1, "not_begins_with": 1, "ends_with": 1, "not_ends_with": 1,
	"is_null": 0, "is_not_null": 0, "is_empty": 0, "is_not_empty": 0,
	"between": 2, "not_between": 2, "in": -1, "not_in": -1,
	// in_last: [n, unit] — the last n days, weeks, months, quarters or years, the current one included.
	"in_last": 2,
	// in_period: [this|last|next, unit] — one whole calendar period, e.g. last month.
	"in_period": 2,
	// to_date: [unit] — from the start of the current week, month, quarter or year to today.
	"to_date": 1,
}

// LastUnits are the units of in_last and in_period.
var LastUnits = []string{"day", "week", "month", "quarter", "year"}

// PeriodWhich are the periods in_period names.
var PeriodWhich = []string{"this", "last", "next"}

// ToDateUnits are the units of to_date.
var ToDateUnits = []string{"week", "month", "quarter", "year"}

// PeriodStart is the start of the unit containing t; weeks start on Sunday.
func PeriodStart(t time.Time, unit string) time.Time {
	d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	switch unit {
	case "week":
		return d.AddDate(0, 0, -int(d.Weekday()))
	case "month":
		return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, d.Location())
	case "quarter":
		return time.Date(d.Year(), (d.Month()-1)/3*3+1, 1, 0, 0, 0, 0, d.Location())
	case "year":
		return time.Date(d.Year(), 1, 1, 0, 0, 0, 0, d.Location())
	}
	return d
}

// AddPeriods moves a period start by n units.
func AddPeriods(t time.Time, unit string, n int) time.Time {
	switch unit {
	case "week":
		return t.AddDate(0, 0, 7*n)
	case "month":
		return t.AddDate(0, n, 0)
	case "quarter":
		return t.AddDate(0, 3*n, 0)
	case "year":
		return t.AddDate(n, 0, 0)
	}
	return t.AddDate(0, 0, n)
}

// Since is where the last n units start, counting the current one; weeks start on Sunday.
func Since(now time.Time, n int, unit string) time.Time {
	return AddPeriods(PeriodStart(now, unit), unit, -(n - 1))
}

// DynamicRange is the [from, to) range of an in_period or to_date filter at now.
func DynamicRange(f Filter, now time.Time) (from, to time.Time, ok bool) {
	switch f.Op {
	case "in_period":
		start := PeriodStart(now, f.Values[1])
		shift := map[string]int{"this": 0, "last": -1, "next": 1}[f.Values[0]]
		from = AddPeriods(start, f.Values[1], shift)
		return from, AddPeriods(from, f.Values[1], 1), true
	case "to_date":
		return PeriodStart(now, f.Values[0]), PeriodStart(now, "day").AddDate(0, 0, 1), true
	}
	return from, to, false
}

// unit normalizes a unit as typed: "Days" → "day".
func unit(s string) string { return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "s") }

func (f Filter) check() error {
	n, ok := FilterOps[f.Op]
	switch {
	case f.Field == "":
		return errors.New("a filter needs a field")
	case !ok:
		ops := make([]string, 0, len(FilterOps))
		for k := range FilterOps {
			ops = append(ops, k)
		}
		slices.Sort(ops)
		return fmt.Errorf("unknown filter operator %q (operators: %s)", f.Op, strings.Join(ops, " "))
	case n == -1 && len(f.Values) == 0:
		return fmt.Errorf("%s on %s needs a list of values", f.Op, f.Field)
	case n >= 0 && len(f.Values) != n:
		return fmt.Errorf("%s on %s takes %d value(s), got %d", f.Op, f.Field, n, len(f.Values))
	case f.Op == "in_last":
		f.Values[1] = unit(f.Values[1]) // days, Weeks…
		if k, err := strconv.Atoi(f.Values[0]); err != nil || k < 1 {
			return fmt.Errorf("in_last on %s needs a count of 1 or more, not %q", f.Field, f.Values[0])
		}
		if !slices.Contains(LastUnits, f.Values[1]) {
			return fmt.Errorf("in_last on %s counts %s, not %q", f.Field, strings.Join(LastUnits, ", "), f.Values[1])
		}
	case f.Op == "in_period":
		f.Values[0], f.Values[1] = strings.ToLower(strings.TrimSpace(f.Values[0])), unit(f.Values[1])
		if !slices.Contains(PeriodWhich, f.Values[0]) {
			return fmt.Errorf("in_period on %s is this, last or next, not %q", f.Field, f.Values[0])
		}
		if !slices.Contains(LastUnits, f.Values[1]) {
			return fmt.Errorf("in_period on %s takes a %s, not %q", f.Field, strings.Join(LastUnits, ", "), f.Values[1])
		}
	case f.Op == "to_date":
		f.Values[0] = unit(f.Values[0])
		if !slices.Contains(ToDateUnits, f.Values[0]) {
			return fmt.Errorf("to_date on %s takes a %s, not %q", f.Field, strings.Join(ToDateUnits, ", "), f.Values[0])
		}
	}
	return nil
}

// sourceStart lists the statements a query data source may be, per engine:
// ones that can sit in FROM (…) and read data.
var sourceStart = map[string][]string{
	"mysql":    {"SELECT", "WITH", "VALUES", "TABLE"},
	"postgres": {"SELECT", "WITH", "VALUES", "TABLE"},
	"sqlite":   {"SELECT", "WITH", "VALUES"},
}

var outfileRe = regexp.MustCompile(`(?i)\bINTO\s+(OUTFILE|DUMPFILE)\b`)

// CheckSQL accepts exactly one read statement for a query data source and returns
// it without its delimiter. Data sources also always run in a read-only
// transaction; this keeps out what would end that transaction or write files.
func CheckSQL(d driver.Dialect, sql string) (string, error) {
	stmts := sqlsplit.Split(sql, d.Split)
	if len(stmts) != 1 {
		return "", fmt.Errorf("a data source query is exactly one statement (got %d)", len(stmts))
	}
	s := strings.TrimSpace(stmts[0].SQL)
	if kw := sqlcommon.LeadingKeyword(s); !slices.Contains(sourceStart[d.Name], kw) {
		return "", fmt.Errorf("a data source query reads data: it starts with %s", strings.Join(sourceStart[d.Name], ", "))
	}
	if d.Name == "mysql" && outfileRe.MatchString(s) {
		return "", errors.New("a data source query can't write files")
	}
	return s, nil
}

// From is the FROM target of a data source: its table, or its query in parentheses.
func From(d driver.Dialect, src DataSource) (string, error) {
	if src.Table != "" {
		ref := model.ObjectRef{Database: src.Database, Schema: src.Schema, Name: src.Table}
		if d.HasSchemas && ref.Schema == "" {
			ref.Schema = d.DefaultSchema
		}
		return d.Qualified(ref), nil
	}
	s, err := CheckSQL(d, src.SQL)
	if err != nil {
		return "", err
	}
	return "(" + s + ")", nil
}

// ArchiveTable is the table an archive snapshot keeps its rows in.
const ArchiveTable = "data"

// SQLite is the dialect of archive snapshots.
var SQLite = driver.Dialect{Name: "sqlite", IdentQuote: `"`, Split: sqlsplit.SQLite}

// ProbeQuery reads a few rows of the source, for its columns and to guess the
// types of columns the database doesn't type.
func ProbeQuery(from string, limit int) Stmt {
	return Stmt{SQL: fmt.Sprintf("SELECT * FROM %s AS s LIMIT %d", from, limit)}
}

// PreviewQuery reads the data source as its preview grid shows it: projected,
// filtered and sorted.
func PreviewQuery(d driver.Dialect, from string, src DataSource, limit int) (Stmt, error) {
	b := qb{d: d, tz: src.Timezone}
	cols := "*"
	if src.Projection != nil && len(src.Projection.Fields) > 0 {
		vis := src.Visible()
		if len(vis) == 0 {
			return Stmt{}, errors.New("the projection leaves no fields")
		}
		q := make([]string, len(vis))
		for i, f := range vis {
			q[i] = d.QuoteIdent(f.Name)
		}
		cols = strings.Join(q, ", ")
	}
	sql := "SELECT " + cols + " FROM " + from + " AS s"
	w, err := b.where(src.Fields, src.Filters)
	if err != nil {
		return Stmt{}, err
	}
	sql += w
	if len(src.Sorts) > 0 {
		parts := make([]string, len(src.Sorts))
		for i, s := range src.Sorts {
			if _, ok := fieldIn(src.Fields, s.Field); !ok {
				return Stmt{}, fmt.Errorf("no field %q to sort by", s.Field)
			}
			parts[i] = d.QuoteIdent(s.Field)
			if s.Desc {
				parts[i] += " DESC"
			}
		}
		sql += " ORDER BY " + strings.Join(parts, ", ")
	}
	sql += fmt.Sprintf(" LIMIT %d", limit)
	return Stmt{sql, b.args}, nil
}

// ChartQuery builds the query for a chart: dimensions and series grouped (date
// fields by their bucket), values aggregated, the source's and the chart's
// filters applied. Every field name is checked against the data source.
func ChartQuery(d driver.Dialect, from string, src DataSource, c Chart, limit int) (Stmt, []Column, error) {
	if _, err := c.Check(src, d.Name); err != nil {
		return Stmt{}, nil, err
	}
	b := qb{d: d, tz: src.Timezone}
	var sel, group []string
	var cols []Column
	add := func(role string, cf ChartField, grouped bool) {
		f, _ := src.Field(cf.Field)
		col := Column{Key: fmt.Sprintf("c%d", len(cols)), Label: cf.Label(), Role: role, Field: cf.Field, Aggregate: cf.Aggregate, Type: f.Type}
		var expr string
		if grouped {
			expr = b.bucket(f, cf.Aggregate)
			if cf.Aggregate != "" {
				// Periods are dates (as sortable text); extracted parts aren't.
				col.Type = TypeString
				if !strings.HasSuffix(cf.Aggregate, "_x") {
					col.Type = TypeDateTime
				}
			}
			group = append(group, strconv.Itoa(len(cols)+1))
		} else {
			expr = b.aggregate(f, cf.Aggregate)
			if cf.Aggregate == "count" || cf.Aggregate == "count_distinct" {
				col.Type = TypeNumber
			}
		}
		sel = append(sel, expr+" AS "+d.QuoteIdent(col.Key))
		cols = append(cols, col)
	}
	for _, cf := range c.Dimension {
		add("dimension", cf, true)
	}
	for _, cf := range c.Series {
		add("series", cf, true)
	}
	raw := ChartTypes[c.Type].Raw
	for _, cf := range c.Value {
		role := "value"
		if raw {
			role = "column"
		}
		add(role, cf, raw && (cf.Aggregate == "" || IsBucket(cf.Aggregate)))
	}
	for _, cf := range c.Secondary {
		add("secondary", cf, false)
	}
	for _, cf := range c.Target {
		add("target", cf, false)
	}
	if len(sel) == 0 {
		return Stmt{}, nil, errors.New("the chart has no fields yet")
	}
	sql := "SELECT " + strings.Join(sel, ", ") + " FROM " + from + " AS s"
	w, err := b.where(src.Fields, append(slices.Clone(src.Filters), c.Filters...))
	if err != nil {
		return Stmt{}, nil, err
	}
	sql += w
	// A table of plain columns lists rows; anything else groups.
	if len(group) > 0 && (!raw || len(group) < len(cols)) {
		sql += " GROUP BY " + strings.Join(group, ", ")
	}
	if len(group) > 0 {
		sql += " ORDER BY " + strings.Join(group, ", ")
	}
	sql += fmt.Sprintf(" LIMIT %d", limit)
	return Stmt{sql, b.args}, cols, nil
}

// qb renders expressions for one statement, collecting bind arguments.
type qb struct {
	d    driver.Dialect
	tz   string // the data source's time zone
	args []any
}

func (b *qb) bind(f Field, v string) string {
	var a any = v
	// SQLite compares a text argument with an untyped number as text, so numbers
	// go as numbers there; other engines convert text to the column's type.
	if b.d.Name == "sqlite" && f.Type == TypeNumber {
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			a = i
		} else if x, err := strconv.ParseFloat(v, 64); err == nil {
			a = x
		}
	}
	b.args = append(b.args, a)
	return b.d.Placeholder(len(b.args))
}

func (b *qb) text(col string) string {
	if b.d.Name == "mysql" {
		return "CAST(" + col + " AS CHAR)"
	}
	return "CAST(" + col + " AS TEXT)"
}

var likeEscape = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

func (b *qb) where(fields []Field, filters []Filter) (string, error) {
	var parts []string
	for _, f := range filters {
		if f.Disabled {
			continue
		}
		if err := f.check(); err != nil {
			return "", err
		}
		fd, ok := fieldIn(fields, f.Field)
		if !ok {
			return "", fmt.Errorf("no field %q to filter on", f.Field)
		}
		col := b.col(fd)
		like := "LIKE"
		if b.d.Name == "postgres" {
			like = "ILIKE" // contains / begins with match case-insensitively, as MySQL and SQLite do
		}
		var e string
		switch f.Op {
		case "=", "!=", "<", "<=", ">", ">=":
			// A plain date on a date/time field means the whole day.
			if day, ok := wholeDay(fd, f.Values[0]); ok {
				next := day.AddDate(0, 0, 1).Format("2006-01-02")
				switch f.Op {
				case "=", "!=":
					e = col + " >= " + b.bind(fd, f.Values[0]) + " AND " + col + " < " + b.bind(fd, next)
					if f.Op == "!=" {
						e = "NOT (" + e + ")"
					}
				case "<=":
					e = col + " < " + b.bind(fd, next)
				case ">":
					e = col + " >= " + b.bind(fd, next)
				default: // < and >= already start at midnight
					e = col + " " + f.Op + " " + b.bind(fd, f.Values[0])
				}
				break
			}
			op := map[string]string{"!=": "<>"}[f.Op]
			if op == "" {
				op = f.Op
			}
			e = col + " " + op + " " + b.bind(fd, f.Values[0])
		case "contains", "not_contains", "begins_with", "not_begins_with", "ends_with", "not_ends_with":
			v := likeEscape.Replace(f.Values[0])
			switch strings.TrimPrefix(f.Op, "not_") {
			case "contains":
				v = "%" + v + "%"
			case "begins_with":
				v += "%"
			case "ends_with":
				v = "%" + v
			}
			b.args = append(b.args, v)
			e = b.text(col) + " " + like + " " + b.d.Placeholder(len(b.args)) + " ESCAPE '!'"
			if strings.HasPrefix(f.Op, "not_") {
				e = "NOT (" + e + ")"
			}
		case "in_last":
			k, _ := strconv.Atoi(f.Values[0])
			// A date as text compares correctly with dates, timestamps and SQLite's date text.
			b.args = append(b.args, Since(now().In(location(b.tz)), k, f.Values[1]).Format("2006-01-02"))
			e = col + " >= " + b.d.Placeholder(len(b.args))
		case "in_period", "to_date":
			from, to, _ := DynamicRange(f, now().In(location(b.tz)))
			b.args = append(b.args, from.Format("2006-01-02"))
			e = col + " >= " + b.d.Placeholder(len(b.args))
			b.args = append(b.args, to.Format("2006-01-02"))
			e += " AND " + col + " < " + b.d.Placeholder(len(b.args))
		case "is_null":
			e = col + " IS NULL"
		case "is_not_null":
			e = col + " IS NOT NULL"
		case "is_empty":
			e = b.text(col) + " = ''"
		case "is_not_empty":
			e = b.text(col) + " <> ''"
		case "between", "not_between":
			if day, ok := wholeDay(fd, f.Values[1]); ok {
				// The end date is included whole: up to midnight the day after.
				e = col + " >= " + b.bind(fd, f.Values[0]) + " AND " + col + " < " + b.bind(fd, day.AddDate(0, 0, 1).Format("2006-01-02"))
			} else {
				e = col + " BETWEEN " + b.bind(fd, f.Values[0]) + " AND " + b.bind(fd, f.Values[1])
			}
			if f.Op == "not_between" {
				e = "NOT (" + e + ")"
			}
		case "in", "not_in":
			ph := make([]string, len(f.Values))
			for i, v := range f.Values {
				ph[i] = b.bind(fd, v)
			}
			e = col + " IN (" + strings.Join(ph, ", ") + ")"
			if f.Op == "not_in" {
				e = "NOT (" + e + ")"
			}
		}
		if f.Negate {
			e = "NOT (" + e + ")"
		}
		parts = append(parts, e)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), nil
}

// wholeDay reports whether v is a plain date (2026-10-14) compared with a
// date/time field, where it should stand for the whole day.
func wholeDay(f Field, v string) (time.Time, bool) {
	if f.Type != TypeDateTime || strings.EqualFold(f.DBType, "date") || len(v) != 10 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", v)
	return t, err == nil
}

func (b *qb) aggregate(f Field, agg string) string {
	col := b.col(f)
	num := col
	if b.d.Name == "postgres" {
		num = "CAST(" + col + " AS numeric)"
	}
	switch agg {
	case "sum":
		return "SUM(" + num + ")"
	case "avg":
		return "AVG(" + num + ")"
	case "median":
		return "percentile_cont(0.5) WITHIN GROUP (ORDER BY " + num + ")"
	case "count":
		return "COUNT(" + col + ")"
	case "count_distinct":
		return "COUNT(DISTINCT " + col + ")"
	case "min":
		return "MIN(" + col + ")"
	case "max":
		return "MAX(" + col + ")"
	}
	return col
}

// bucket groups a date/time by period, as text that sorts in time order
// ("2026-03", "2026-Q1"…). A week is the date of its Sunday ("2026-09-13"), as
// Navicat's weeks start on Sunday; week_x numbers weeks like strftime's %U.
func (b *qb) bucket(f Field, bucket string) string {
	col := b.col(f)
	if bucket == "" {
		return col
	}
	switch b.d.Name {
	case "postgres":
		x := "CAST(" + col + " AS timestamp)"
		week := "lpad((((extract(doy from " + x + ")::int - 1) + 7 - extract(dow from " + x + ")::int) / 7)::text, 2, '0')"
		f := func(p string) string { return "to_char(" + x + ", '" + p + "')" }
		return map[string]string{
			"year": f("YYYY"), "quarter": f(`YYYY-"Q"Q`), "month": f("YYYY-MM"), "week": "to_char(CAST(" + x + " AS date) - extract(dow from " + x + ")::int, 'YYYY-MM-DD')",
			"day": f("YYYY-MM-DD"), "hour": f("YYYY-MM-DD HH24"), "minute": f("YYYY-MM-DD HH24:MI"), "second": f("YYYY-MM-DD HH24:MI:SS"),
			"quarter_x": f(`"Q"Q`), "month_x": f("MM"), "week_x": "'W' || " + week, "day_x": f("DD"),
			"weekday_x": "(extract(dow from " + x + ")::int)::text", "hour_x": f("HH24"), "minute_x": f("MI"), "second_x": f("SS"),
		}[bucket]
	case "mysql":
		f := func(p string) string { return "DATE_FORMAT(" + col + ", '" + p + "')" }
		return map[string]string{
			"year": f("%Y"), "quarter": "CONCAT(YEAR(" + col + "), '-Q', QUARTER(" + col + "))", "month": f("%Y-%m"),
			"week": "DATE_FORMAT(DATE_SUB(" + col + ", INTERVAL DAYOFWEEK(" + col + ") - 1 DAY), '%Y-%m-%d')",
			"day":  f("%Y-%m-%d"), "hour": f("%Y-%m-%d %H"), "minute": f("%Y-%m-%d %H:%i"), "second": f("%Y-%m-%d %H:%i:%s"),
			"quarter_x": "CONCAT('Q', QUARTER(" + col + "))", "month_x": f("%m"), "week_x": f("W%U"), "day_x": f("%d"),
			"weekday_x": "CAST(DAYOFWEEK(" + col + ") - 1 AS CHAR)", "hour_x": f("%H"), "minute_x": f("%i"), "second_x": f("%s"),
		}[bucket]
	default: // sqlite
		f := func(p string) string { return "strftime('" + p + "', " + col + ")" }
		q := "((CAST(strftime('%m', " + col + ") AS INTEGER) + 2) / 3)"
		return map[string]string{
			"year": f("%Y"), "quarter": f("%Y") + " || '-Q' || " + q, "month": f("%Y-%m"), "week": "date(" + col + ", '-' || strftime('%w', " + col + ") || ' days')",
			"day": f("%Y-%m-%d"), "hour": f("%Y-%m-%d %H"), "minute": f("%Y-%m-%d %H:%M"), "second": f("%Y-%m-%d %H:%M:%S"),
			"quarter_x": "'Q' || " + q, "month_x": f("%m"), "week_x": f("W%U"), "day_x": f("%d"),
			"weekday_x": f("%w"), "hour_x": f("%H"), "minute_x": f("%M"), "second_x": f("%S"),
		}[bucket]
	}
}
