package bi

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"chloe/internal/driver"
	"chloe/internal/sqlsplit"
)

var (
	pg    = driver.Dialect{Name: "postgres", IdentQuote: `"`, HasSchemas: true, DefaultSchema: "public", Split: sqlsplit.Postgres}
	mysql = driver.Dialect{Name: "mysql", IdentQuote: "`", Split: sqlsplit.MySQL}
)

func ordersSource() DataSource {
	return DataSource{Meta: Meta{ID: "s1", Name: "Orders"}, ConnID: "c", Database: "shop", Table: "orders", Mode: ModeLive,
		Fields: []Field{{Name: "placed_at", Type: TypeDateTime}, {Name: "total", Type: TypeNumber}, {Name: "status", Type: TypeString}, {Name: "customer", Type: TypeString}}}
}

func TestChartQueryPerDialect(t *testing.T) {
	src := ordersSource()
	src.Filters = []Filter{{Field: "status", Op: "!=", Values: []string{"new"}}}
	c := Chart{Meta: Meta{Name: "Revenue"}, DataSource: "s1", Type: "bar",
		Dimension: []ChartField{{Field: "placed_at", Aggregate: "month"}},
		Value:     []ChartField{{Field: "total", Aggregate: "sum"}},
		Filters:   []Filter{{Field: "customer", Op: "contains", Values: []string{"50%_off!"}}}}
	cases := map[string]struct {
		d    driver.Dialect
		sql  string
		args []any
	}{
		"postgres": {pg, `SELECT to_char(CAST("placed_at" AS timestamp), 'YYYY-MM') AS "c0", SUM(CAST("total" AS numeric)) AS "c1" FROM "public"."orders" AS s` +
			` WHERE "status" <> $1 AND CAST("customer" AS TEXT) ILIKE $2 ESCAPE '!' GROUP BY 1 ORDER BY 1 LIMIT 100`, []any{"new", "%50!%!_off!!%"}},
		"mysql": {mysql, "SELECT DATE_FORMAT(`placed_at`, '%Y-%m') AS `c0`, SUM(`total`) AS `c1` FROM `shop`.`orders` AS s" +
			" WHERE `status` <> ? AND CAST(`customer` AS CHAR) LIKE ? ESCAPE '!' GROUP BY 1 ORDER BY 1 LIMIT 100", []any{"new", "%50!%!_off!!%"}},
		"sqlite": {SQLite, `SELECT strftime('%Y-%m', "placed_at") AS "c0", SUM("total") AS "c1" FROM "shop"."orders" AS s` +
			` WHERE "status" <> ? AND CAST("customer" AS TEXT) LIKE ? ESCAPE '!' GROUP BY 1 ORDER BY 1 LIMIT 100`, []any{"new", "%50!%!_off!!%"}},
	}
	for name, tc := range cases {
		from, err := From(tc.d, src)
		if err != nil {
			t.Fatal(err)
		}
		q, cols, err := ChartQuery(tc.d, from, src, c, 100)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if q.SQL != tc.sql {
			t.Errorf("%s:\n got %s\nwant %s", name, q.SQL, tc.sql)
		}
		if !reflect.DeepEqual(q.Args, tc.args) {
			t.Errorf("%s: args %v", name, q.Args)
		}
		if len(cols) != 2 || cols[0].Role != "dimension" || cols[0].Label != "Placed At (Month)" || cols[1].Label != "Sum of Total" || cols[1].Type != TypeNumber {
			t.Errorf("%s: columns %+v", name, cols)
		}
	}
}

func TestChartQueryChecksFields(t *testing.T) {
	src := ordersSource()
	src.Projection = &Projection{Exclude: true, Fields: []string{"customer"}}
	for _, tc := range []struct {
		c    Chart
		want string
	}{
		{Chart{Type: "bar", Dimension: []ChartField{{Field: "customer"}}, Value: []ChartField{{Field: "total", Aggregate: "sum"}}}, `no field "customer"`},
		{Chart{Type: "bar", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "total"}}}, `can't use aggregate ""`},
		{Chart{Type: "bar", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "status", Aggregate: "sum"}}}, `can't use aggregate "sum"`},
		{Chart{Type: "pie", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "total", Aggregate: "sum"}, {Field: "total", Aggregate: "avg"}}}, "at most 1 value"},
		{Chart{Type: "value", Dimension: []ChartField{{Field: "status"}}}, "no dimension shelf"},
		{Chart{Type: "bar", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "total", Aggregate: "median"}}}, `can't use aggregate "median"`},
		{Chart{Type: "pyramid"}, "unknown chart type"},
	} {
		_, _, err := ChartQuery(mysql, "`t`", src, tc.c, 10)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: got %v, want %q", tc.c, err, tc.want)
		}
	}
	// Median is PostgreSQL's.
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "total", Aggregate: "median"}}}
	if q, _, err := ChartQuery(pg, `"t"`, src, c, 10); err != nil || !strings.Contains(q.SQL, "percentile_cont(0.5) WITHIN GROUP (ORDER BY CAST(\"total\" AS numeric))") {
		t.Errorf("median: %v %s", err, q.SQL)
	}
}

func TestIncompleteChart(t *testing.T) {
	c := Chart{Type: "bar", Value: []ChartField{{Field: "total", Aggregate: "sum"}}}
	complete, err := c.Check(ordersSource(), "mysql")
	if err != nil || complete {
		t.Fatalf("complete=%v err=%v", complete, err)
	}
}

func TestTableChart(t *testing.T) {
	src := ordersSource()
	raw := Chart{Type: "table", Value: []ChartField{{Field: "status"}, {Field: "total"}}}
	q, _, err := ChartQuery(SQLite, `"data"`, src, raw, 50)
	if err != nil || q.SQL != `SELECT "status" AS "c0", "total" AS "c1" FROM "data" AS s ORDER BY 1, 2 LIMIT 50` {
		t.Errorf("raw table: %v %s", err, q.SQL)
	}
	grouped := Chart{Type: "table", Value: []ChartField{{Field: "status"}, {Field: "total", Aggregate: "sum"}}}
	q, _, err = ChartQuery(SQLite, `"data"`, src, grouped, 50)
	if err != nil || q.SQL != `SELECT "status" AS "c0", SUM("total") AS "c1" FROM "data" AS s GROUP BY 1 ORDER BY 1 LIMIT 50` {
		t.Errorf("grouped table: %v %s", err, q.SQL)
	}
}

func TestFilters(t *testing.T) {
	src := ordersSource()
	src.Filters = []Filter{
		{Field: "total", Op: "between", Values: []string{"10", "20.5"}},
		{Field: "status", Op: "in", Values: []string{"paid", "shipped"}, Negate: true},
		{Field: "customer", Op: "is_empty"},
		{Field: "customer", Op: "is_null", Disabled: true},
		{Field: "customer", Op: "not_begins_with", Values: []string{"x"}},
	}
	q, err := PreviewQuery(SQLite, `"data"`, src, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT * FROM "data" AS s WHERE "total" BETWEEN ? AND ? AND NOT ("status" IN (?, ?)) AND CAST("customer" AS TEXT) = '' AND NOT (CAST("customer" AS TEXT) LIKE ? ESCAPE '!') LIMIT 10`
	if q.SQL != want {
		t.Errorf("got  %s\nwant %s", q.SQL, want)
	}
	// SQLite gets numbers as numbers.
	if !reflect.DeepEqual(q.Args, []any{int64(10), 20.5, "paid", "shipped", "x%"}) {
		t.Errorf("args %#v", q.Args)
	}
	for _, f := range []Filter{{Field: "total", Op: "between", Values: []string{"1"}}, {Field: "total", Op: "~"}, {Field: "nope", Op: "is_null"}} {
		src.Filters = []Filter{f}
		if _, err := PreviewQuery(SQLite, `"data"`, src, 10); err == nil {
			t.Errorf("%+v: no error", f)
		}
	}
}

func TestCheckSQL(t *testing.T) {
	for _, ok := range []string{"SELECT 1;", " with x as (select 1) select * from x", "(SELECT 1)"} {
		if _, err := CheckSQL(mysql, ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"UPDATE t SET a = 1", "SELECT 1; SELECT 2", "SELECT * FROM t INTO OUTFILE '/tmp/x'", "SHOW TABLES", "EXPLAIN SELECT 1", ""} {
		if _, err := CheckSQL(mysql, bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
	src := ordersSource()
	src.Table, src.SQL = "", "SELECT * FROM orders;"
	if from, err := From(pg, src); err != nil || from != "(SELECT * FROM orders)" {
		t.Errorf("from: %q %v", from, err)
	}
}

func TestShape(t *testing.T) {
	s := func(v string) *string { return &v }
	cols := []Column{{Role: "dimension", Type: TypeString}, {Role: "value", Type: TypeNumber}}
	raw := [][]*string{{s("a"), s("1")}, {s("b"), s("5")}, {s("c"), s("3")}, {s("d"), nil}, {nil, s("2")}}
	c := Chart{Type: "bar", Value: []ChartField{{Field: "x"}}}
	got := Shape(c, cols, raw)
	// Categories: biggest first.
	if want := [][]any{{"b", 5.0}, {"c", 3.0}, {nil, 2.0}, {"a", 1.0}, {"d", nil}}; !reflect.DeepEqual(got, want) {
		t.Errorf("default sort: %v", got)
	}
	c.Top, c.Others = 2, true
	if got, want := Shape(c, cols, raw), [][]any{{"b", 5.0}, {"c", 3.0}, {"Others", 3.0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("top 2 + others: %v", got)
	}
	c = Chart{Type: "line", Value: []ChartField{{Field: "x", Op: "cumulative"}}}
	months := [][]*string{{s("2026-02"), s("2")}, {s("2026-01"), s("1")}, {s("2026-03"), s("4")}}
	dcols := []Column{{Role: "dimension", Type: TypeString, Aggregate: "month"}, {Role: "value", Type: TypeNumber}}
	if got, want := Shape(c, dcols, months), [][]any{{"2026-01", 1.0}, {"2026-02", 3.0}, {"2026-03", 7.0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("cumulative: %v", got)
	}
	c.Value[0].Op = "subtract"
	if got, want := Shape(c, dcols, months), [][]any{{"2026-01", nil}, {"2026-02", 1.0}, {"2026-03", 2.0}}; !reflect.DeepEqual(got, want) {
		t.Errorf("subtract: %v", got)
	}
}

func TestTypes(t *testing.T) {
	for db, want := range map[string]string{"INT": TypeNumber, "numeric(10,2)": TypeNumber, "timestamptz": TypeDateTime, "DATE": TypeDateTime,
		"VARCHAR": TypeString, "interval": TypeString, "bool": TypeString, "YEAR": TypeNumber, "": ""} {
		if got := FieldType(db); got != want {
			t.Errorf("%q: %q, want %q", db, got, want)
		}
	}
	if GuessType([]string{"1", "2.5"}) != TypeNumber || GuessType([]string{"2024-03-01", "2024-03-02 10:00"}) != TypeDateTime || GuessType([]string{"1", "x"}) != TypeString {
		t.Error("guess")
	}
	for in, want := range map[string]string{"2024-03-01 10:30:00+00": "2024-03-01 10:30:00", "2024-03-01T10:30:00.5Z": "2024-03-01 10:30:00.5", "2026-09-15 10:00:00-07": "2026-09-15 17:00:00", "2024-03-01": "2024-03-01", "x": "x"} {
		if got := NormalizeDate(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}

func TestLibrary(t *testing.T) {
	l := Library{Dir: t.TempDir()}
	p, err := l.Create(New("Sales"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := l.Update(p, func(w *Workspace) error {
		src, err := w.PutSource(ordersSource())
		if err != nil {
			return err
		}
		_, err = w.PutChart(Chart{Meta: Meta{Name: "By month"}, DataSource: src.ID, Type: "bar"})
		return err
	})
	if err != nil || len(w.DataSources) != 1 || len(w.Charts) != 1 || w.Charts[0].Created == "" {
		t.Fatalf("update: %v %+v", err, w)
	}
	// Names are unique per kind: a chart may be named after its data source.
	if _, err := l.Update(p, func(w *Workspace) error {
		_, err := w.PutChart(Chart{Meta: Meta{Name: "orders"}, DataSource: "s1", Type: "bar"})
		return err
	}); err != nil {
		t.Errorf("chart named like its source: %v", err)
	}
	if _, err := l.Update(p, func(w *Workspace) error {
		_, err := w.PutChart(Chart{Meta: Meta{Name: "BY MONTH"}, DataSource: "s1", Type: "bar"})
		return err
	}); err == nil || !strings.Contains(err.Error(), "already has a chart") {
		t.Errorf("duplicate chart name: %v", err)
	}
	if err := os.MkdirAll(l.DataDir(w.ID), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(l.ArchivePath(w.ID, "s1"), []byte("rows"), 0o600)
	dup, err := l.Duplicate(p)
	if err != nil {
		t.Fatal(err)
	}
	dw, _ := Read(dup)
	if dw.Name != "Sales copy" || dw.ID == w.ID {
		t.Errorf("duplicate: %+v", dw)
	}
	if data, _ := os.ReadFile(l.ArchivePath(dw.ID, "s1")); string(data) != "rows" {
		t.Error("duplicate didn't copy the archive")
	}
	np, err := l.Rename(p, "Revenue")
	if err != nil || filepath.Base(np) != "Revenue.chloebi" {
		t.Fatalf("rename: %v %s", err, np)
	}
	if got, err := l.Find("revenue"); err != nil || got != np {
		t.Errorf("find: %v %s", err, got)
	}
	if err := l.Delete(np); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(l.DataDir(w.ID)); !os.IsNotExist(err) {
		t.Error("delete left the archive folder")
	}
	if len(l.List()) != 1 {
		t.Errorf("list: %+v", l.List())
	}
}

func TestHumanize(t *testing.T) {
	for in, want := range map[string]string{"total": "Total", "placed_at": "Placed At", "customer_id": "Customer ID", "orderTotal": "Order Total",
		"HTTPStatus": "HTTP Status", "unit-price": "Unit Price", "MRR": "MRR", "o.total": "O Total", "x": "X", "": ""} {
		if got := Humanize(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
	if l := (ChartField{Field: "total", Aggregate: "sum", Alias: "revenue"}).Label(); l != "revenue" {
		t.Errorf("alias: %q", l)
	}
}

func TestTableSort(t *testing.T) {
	s := func(v string) *string { return &v }
	cols := []Column{{Role: "column", Field: "game", Type: TypeString}, {Role: "column", Field: "rooms", Label: "Count of Rooms", Aggregate: "count", Type: TypeNumber},
		{Role: "column", Field: "events", Label: "Average of Events", Aggregate: "avg", Type: TypeNumber}}
	raw := [][]*string{{s("Alpha"), s("3"), s("9")}, {s("Bravo"), s("10"), s("1")}, {s("Charlie"), s("5"), s("4")}}
	c := Chart{Type: "table", Value: []ChartField{{Field: "game"}, {Field: "rooms", Aggregate: "count"}, {Field: "events", Aggregate: "avg"}}}
	names := func(rows [][]any) (out []any) {
		for _, r := range rows {
			out = append(out, r[0])
		}
		return out
	}
	c.Sort = &ChartSort{By: "value", Desc: true}
	if got := names(Shape(c, cols, raw)); !reflect.DeepEqual(got, []any{"Bravo", "Charlie", "Alpha"}) {
		t.Errorf("first aggregate: %v", got)
	}
	c.Sort.Field = "events"
	if got := names(Shape(c, cols, raw)); !reflect.DeepEqual(got, []any{"Alpha", "Charlie", "Bravo"}) {
		t.Errorf("named column: %v", got)
	}
	c.Sort, c.Top = &ChartSort{By: "dimension", Desc: true}, 2
	if got := names(Shape(c, cols, raw)); !reflect.DeepEqual(got, []any{"Charlie", "Bravo"}) {
		t.Errorf("dimension + top: %v", got)
	}
	// A value's name or label works as by.
	c.Sort, c.Top = &ChartSort{By: "Average of Events", Desc: true}, 0
	if got := names(Shape(c, cols, raw)); !reflect.DeepEqual(got, []any{"Alpha", "Charlie", "Bravo"}) {
		t.Errorf("by label: %v", got)
	}
	c.Sort = &ChartSort{By: "value", Field: "nope"}
	if _, err := c.Check(DataSource{Fields: []Field{{Name: "game"}, {Name: "rooms", Type: TypeNumber}, {Name: "events", Type: TypeNumber}}}, "mysql"); err == nil {
		t.Error("unknown sort field accepted")
	}
}

func TestFillPeriods(t *testing.T) {
	s := func(v string) *string { return &v }
	cols := []Column{{Role: "dimension", Aggregate: "day", Type: TypeDateTime}, {Role: "series", Type: TypeString}, {Role: "value", Aggregate: "count", Type: TypeNumber}}
	raw := [][]*string{{s("2026-09-28"), s("won"), s("2")}, {s("2026-10-01"), s("won"), s("1")}, {s("2026-10-01"), s("lost"), s("4")}}
	c := Chart{Type: "stacked_bar", Value: []ChartField{{Field: "id", Aggregate: "count"}}}
	got := Shape(c, cols, raw)
	var days []string
	for _, r := range got {
		if r[1] == "won" {
			days = append(days, r[0].(string))
		}
	}
	if want := []string{"2026-09-28", "2026-09-29", "2026-09-30", "2026-10-01"}; !reflect.DeepEqual(days, want) {
		t.Fatalf("days: %v", days)
	}
	if len(got) != 8 || got[1][2] != 0.0 { // every day × series
		t.Errorf("filled rows: %v", got)
	}
	// Weeks across a new year, and averages left empty.
	cols[0].Aggregate = "week"
	c = Chart{Type: "line", Value: []ChartField{{Field: "x", Aggregate: "avg"}}}
	got = Shape(c, []Column{cols[0], cols[2]}, [][]*string{{s("2025-12-14"), s("1")}, {s("2026-01-04"), s("2")}})
	var labels []any
	for _, r := range got {
		labels = append(labels, r[0])
	}
	if want := []any{"2025-12-14", "2025-12-21", "2025-12-28", "2026-01-04"}; !reflect.DeepEqual(labels, want) || got[1][1] != nil {
		t.Errorf("weeks: %v", got)
	}
	// Top N or a value sort leaves the periods alone.
	c.Top = 1
	if got = Shape(c, []Column{cols[0], cols[2]}, [][]*string{{s("2025-12-14"), s("1")}, {s("2026-01-04"), s("2")}}); len(got) != 1 {
		t.Errorf("top: %v", got)
	}
}

func TestInLast(t *testing.T) {
	sat := time.Date(2026, 10, 17, 15, 0, 0, 0, time.UTC) // a Saturday
	for _, tc := range []struct {
		n    int
		unit string
		want string
	}{{30, "day", "2026-09-18"}, {1, "day", "2026-10-17"}, {1, "week", "2026-10-11"}, {2, "week", "2026-10-04"}, {3, "month", "2026-08-01"}, {1, "year", "2026-01-01"}} {
		if got := Since(sat, tc.n, tc.unit).Format("2006-01-02"); got != tc.want {
			t.Errorf("%d %s: %s, want %s", tc.n, tc.unit, got, tc.want)
		}
	}
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return sat }
	src := ordersSource()
	src.Filters = []Filter{{Field: "placed_at", Op: "in_last", Values: []string{"30", "day"}}}
	q, err := PreviewQuery(pg, `"t"`, src, 5)
	if err != nil || !strings.Contains(q.SQL, `"placed_at" >= $1`) || q.Args[0] != "2026-09-18" {
		t.Errorf("%v %s %v", err, q.SQL, q.Args)
	}
	for _, bad := range [][]string{{"0", "day"}, {"x", "day"}, {"3", "fortnight"}} {
		src.Filters[0].Values = bad
		if _, err := PreviewQuery(pg, `"t"`, src, 5); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestFillSeries(t *testing.T) {
	s := func(v string) *string { return &v }
	cols := []Column{{Role: "dimension", Aggregate: "day", Type: TypeDateTime}, {Role: "series", Type: TypeString}, {Role: "value", Type: TypeNumber}}
	raw := [][]*string{{s("2026-09-15"), s("anonymous"), s("2")}, {s("2026-09-17"), s("anonymous"), s("1")}, {s("2026-09-17"), s("signed in"), s("3")}}
	c := Chart{Type: "stacked_bar", Value: []ChartField{{Field: "id", Aggregate: "count"}}}
	got := Shape(c, cols, raw)
	pairs := map[string]any{}
	for _, r := range got {
		pairs[r[0].(string)+"/"+r[1].(string)] = r[2]
	}
	want := map[string]any{"2026-09-15/anonymous": 2.0, "2026-09-15/signed in": 0.0, "2026-09-16/anonymous": 0.0, "2026-09-16/signed in": 0.0,
		"2026-09-17/anonymous": 1.0, "2026-09-17/signed in": 3.0}
	if !reflect.DeepEqual(pairs, want) || len(got) != 6 {
		t.Errorf("every day × series: %v", got)
	}
	// Categories too, also with Top N.
	cols[0] = Column{Role: "dimension", Type: TypeString}
	c.Top = 5
	got = Shape(c, cols, [][]*string{{s("x"), s("a"), s("1")}, {s("y"), s("b"), s("1")}})
	if len(got) != 4 {
		t.Errorf("categories: %v", got)
	}
}

func TestWindowAndNames(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC) }
	s := func(v string) *string { return &v }
	src := ordersSource()
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "placed_at", Aggregate: "day"}}, Value: []ChartField{{Field: "id", Aggregate: "count"}},
		Filters: []Filter{{Field: "placed_at", Op: "in_last", Values: []string{"7", "day"}}}}
	cols := []Column{{Role: "dimension", Aggregate: "day", Type: TypeDateTime}, {Role: "value", Type: TypeNumber}}
	rows := ShapeRange(c, cols, [][]*string{{s("2026-10-11"), s("3")}}, FilterWindow(src, c))
	if len(rows) != 7 || rows[0][0] != "2026-10-09" || rows[6][0] != "2026-10-15" || rows[6][1] != 0.0 {
		t.Errorf("window fill: %v", rows)
	}
	// Even with no rows at all the window is drawn.
	if rows = ShapeRange(c, cols, nil, FilterWindow(src, c)); len(rows) != 7 {
		t.Errorf("empty window: %v", rows)
	}
	// Weekdays and months are named once in order.
	c = Chart{Type: "bar", Dimension: []ChartField{{Field: "placed_at", Aggregate: "weekday_x"}}, Value: []ChartField{{Field: "id", Aggregate: "count"}}}
	cols[0].Aggregate = "weekday_x"
	rows = Shape(c, cols, [][]*string{{s("3"), s("5")}, {s("0"), s("1")}, {s("6"), s("2")}})
	if got := []any{rows[0][0], rows[1][0], rows[2][0]}; !reflect.DeepEqual(got, []any{"Sun", "Wed", "Sat"}) {
		t.Errorf("weekdays: %v", got)
	}
	// Tables can bucket a date column.
	tbl := Chart{Type: "table", Value: []ChartField{{Field: "placed_at", Aggregate: "weekday_x"}, {Field: "id", Aggregate: "count"}}}
	src.Fields = append(src.Fields, Field{Name: "id", Type: TypeNumber})
	q, tc, err := ChartQuery(SQLite, `"data"`, src, tbl, 10)
	if err != nil || !strings.Contains(q.SQL, `strftime('%w', "placed_at") AS "c0"`) || !strings.Contains(q.SQL, "GROUP BY 1") || tc[0].Aggregate != "weekday_x" {
		t.Errorf("table bucket: %v %s", err, q.SQL)
	}
	if rows = Shape(tbl, tc, [][]*string{{s("1"), s("4")}}); rows[0][0] != "Mon" {
		t.Errorf("table weekday name: %v", rows)
	}
	tbl.Value[0].Aggregate = "sum"
	if _, _, err := ChartQuery(SQLite, `"data"`, src, tbl, 10); err == nil || !strings.Contains(err.Error(), "placed_at (datetime) on the value shelf can't use aggregate \"sum\"") {
		t.Errorf("message: %v", err)
	}
}

func TestTimezone(t *testing.T) {
	for _, bad := range []string{"Mars/Base", "x'; DROP", "America/Los Angeles"} {
		if CheckTimezone(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	src := ordersSource()
	src.Timezone = "America/Los_Angeles"
	src.Fields = []Field{{Name: "placed_at", Type: TypeDateTime, DBType: "TIMESTAMPTZ"}, {Name: "made", Type: TypeDateTime, DBType: "DATETIME"},
		{Name: "day", Type: TypeDateTime, DBType: "DATE"}, {Name: "total", Type: TypeNumber}}
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "placed_at", Aggregate: "day"}}, Value: []ChartField{{Field: "total", Aggregate: "sum"}},
		Filters: []Filter{{Field: "made", Op: ">=", Values: []string{"2026-01-01"}}, {Field: "day", Op: "=", Values: []string{"2026-01-02"}}}}
	q, _, err := ChartQuery(pg, `"t"`, src, c, 10)
	if err != nil || !strings.Contains(q.SQL, `to_char(CAST(("placed_at" AT TIME ZONE 'America/Los_Angeles') AS timestamp), 'YYYY-MM-DD')`) ||
		!strings.Contains(q.SQL, `(CAST("made" AS timestamp) AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles') >= $1`) || !strings.Contains(q.SQL, `"day" = $2`) {
		t.Errorf("postgres: %v\n%s", err, q.SQL)
	}
	src.Fields[0].DBType = "TIMESTAMP"
	if q, _, _ := ChartQuery(mysql, "`t`", src, c, 10); !strings.Contains(q.SQL, "CONVERT_TZ(`placed_at`, @@session.time_zone, 'America/Los_Angeles')") || !strings.Contains(q.SQL, "CONVERT_TZ(`made`, '+00:00', 'America/Los_Angeles') >= ?") {
		t.Errorf("mysql: %s", q.SQL)
	}
	if q, _, _ := ChartQuery(SQLite, `"data"`, src, c, 10); !strings.Contains(q.SQL, `strftime('%Y-%m-%d', chloe_tz("placed_at", 'America/Los_Angeles'))`) {
		t.Errorf("sqlite: %s", q.SQL)
	}
	if got, _ := InZone("2026-09-16 05:30:00", "America/Los_Angeles"); got != "2026-09-15 22:30:00" {
		t.Errorf("in zone: %s", got)
	}
	if got, _ := InZone("2026-09-16 05:30:00+02", "America/Los_Angeles"); got != "2026-09-15 20:30:00" {
		t.Errorf("zoned value: %s", got)
	}
}

func TestBetweenWindow(t *testing.T) {
	s := func(v string) *string { return &v }
	src := ordersSource()
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "placed_at", Aggregate: "day"}}, Value: []ChartField{{Field: "id", Aggregate: "count"}},
		Filters: []Filter{{Field: "placed_at", Op: "between", Values: []string{"2026-10-10", "2026-10-20"}}}}
	cols := []Column{{Role: "dimension", Aggregate: "day", Type: TypeDateTime}, {Role: "value", Type: TypeNumber}}
	rows := ShapeRange(c, cols, [][]*string{{s("2026-10-12"), s("3")}, {s("2026-10-14"), s("1")}}, FilterWindow(src, c))
	if len(rows) != 11 || rows[0][0] != "2026-10-10" || rows[10][0] != "2026-10-20" {
		t.Errorf("between: %v", rows)
	}
	// An open end stops at the data; < excludes its day.
	c.Filters = []Filter{{Field: "placed_at", Op: "<", Values: []string{"2026-10-17"}}}
	rows = ShapeRange(c, cols, [][]*string{{s("2026-10-12"), s("3")}}, FilterWindow(src, c))
	if len(rows) != 5 || rows[4][0] != "2026-10-16" {
		t.Errorf("before: %v", rows)
	}
}
