package core_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"chloe/internal/bi"
	"chloe/internal/core"
	"chloe/internal/driver"
	"chloe/internal/model"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// The same charts give the same rows on every engine, live and archived: date
// buckets, aggregates and filters are built per dialect.
func TestBIChartsAcrossEngines(t *testing.T) {
	rows := [][3]string{{"North", "120", "2026-01-04 10:00:00"}, {"South", "80", "2026-01-20 00:00:00"}, {"North", "45.5", "2026-02-02 08:30:00"},
		{"East", "300", "2026-02-14 12:00:00"}, {"South", "60", "2026-03-01 00:00:00"}, {"North", "210", "2026-03-09 23:59:00"}, {"West", "15", "2026-04-02 00:00:00"}}
	sql := func(ts string) string {
		var parts []string
		for _, r := range rows {
			parts = append(parts, fmt.Sprintf("SELECT '%s' AS region, CAST(%s AS DECIMAL(10,2)) AS total, CAST('%s' AS %s) AS placed_at", r[0], r[1], r[2], ts))
		}
		return strings.Join(parts, " UNION ALL ")
	}
	charts := []struct {
		name string
		c    bi.Chart
		want [][]any
	}{
		{"month", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "month"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}},
			[][]any{{"2026-01", 200.0}, {"2026-02", 345.5}, {"2026-03", 270.0}, {"2026-04", 15.0}}},
		{"quarter and week", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "week"}}, Value: []bi.ChartField{{Field: "region", Aggregate: "count"}},
			Filters: []bi.Filter{{Field: "placed_at", Op: "<", Values: []string{"2026-02-01"}}}},
			[][]any{{"2026-01-04", 1.0}, {"2026-01-11", 0.0}, {"2026-01-18", 1.0}, {"2026-01-25", 0.0}}}, // weeks by their Sunday, filled out to the filter's end
		{"extracts", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "weekday_x"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "max"}}},
			[][]any{{"Sun", 120.0}, {"Mon", 210.0}, {"Tue", 80.0}, {"Thu", 15.0}, {"Sat", 300.0}}},
		{"series and filters", bi.Chart{Type: "stacked_bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "quarter"}}, Series: []bi.ChartField{{Field: "region"}},
			Value:   []bi.ChartField{{Field: "total", Aggregate: "sum"}},
			Filters: []bi.Filter{{Field: "region", Op: "in", Values: []string{"North", "South"}}, {Field: "region", Op: "contains", Values: []string{"OR"}}}},
			[][]any{{"2026-Q1", "North", 375.5}}},
		{"value", bi.Chart{Type: "value", Value: []bi.ChartField{{Field: "region", Aggregate: "count_distinct"}},
			Filters: []bi.Filter{{Field: "total", Op: "between", Values: []string{"50", "200"}}}},
			[][]any{{2.0}}},

		// Custom fields (defined on the source below).
		{"calculated", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "size"}}, Value: []bi.ChartField{{Field: "net", Aggregate: "sum"}}},
			[][]any{{"big", 567.0}, {"small", 180.45}}},
		{"mapped", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "zone"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}},
			[][]any{{"Top", 375.5}, {"Side", 315.0}, {"Bottom", 140.0}}},
		{"sorted and concatenated", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "ordered"}}, Value: []bi.ChartField{{Field: "label", Aggregate: "count_distinct"}}},
			[][]any{{"West", 1.0}, {"North", 3.0}, {"East", 1.0}, {"South", 2.0}}},
		{"type changed and division by zero", bi.Chart{Type: "value", Value: []bi.ChartField{{Field: "nothing", Aggregate: "max"}},
			Filters: []bi.Filter{{Field: "total_text", Op: "begins_with", Values: []string{"30"}}}},
			[][]any{{nil}}},
	}
	custom := []bi.CustomField{
		{Name: "net", Kind: bi.CustomCalculated, Expression: "ROUND([total] * 0.9, 2)"},
		{Name: "size", Kind: bi.CustomCalculated, Expression: "IF(total >= 100, 'big', 'small')"},
		{Name: "zone", Kind: bi.CustomMapped, Field: "region", Rules: []bi.MapRule{{From: "north", To: "Top"}, {From: "SOUTH", To: "Bottom"}}, Unmapped: "other", Other: "Side"},
		{Name: "label", Kind: bi.CustomConcat, Fields: []string{"region", "total"}, Separator: " / "},
		{Name: "ordered", Kind: bi.CustomSorted, Field: "region", Order: []string{"West", "North"}},
		{Name: "total_text", Kind: bi.CustomTypeChanged, Field: "total", To: bi.TypeString},
		{Name: "nothing", Kind: bi.CustomCalculated, Expression: "total / (total - total)"},
	}
	for _, e := range engines() {
		t.Run(string(e.conn.Type), func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			ts := "DATETIME"
			if e.conn.Type == model.PostgreSQL {
				ts = "timestamp"
			}
			lib := m.Store.BI()
			p, err := lib.Create(bi.New("it"))
			if err != nil {
				t.Fatal(err)
			}
			// A time zone moves values across days: Jan 20 00:00 UTC is Monday evening in Los Angeles.
			// Live charts read a cached copy of the source (SQLite) or the database
			// itself; both must agree.
			reads := map[string]core.BIRead{"cached": {}, "uncached": {Uncached: true}}
			for _, mode := range []string{bi.ModeLive, bi.ModeArchive} {
				src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Zoned " + mode}, ConnID: id, Database: e.database, Schema: e.schema, SQL: sql(ts), Mode: mode, Timezone: "America/Los_Angeles"})
				if err != nil {
					t.Fatal(err)
				}
				w, _ := bi.Read(p)
				c := bi.Chart{DataSource: src.ID, Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "weekday_x"}}, Value: []bi.ChartField{{Field: "region", Aggregate: "count"}},
					Filters: []bi.Filter{{Field: "placed_at", Op: "<", Values: []string{"2026-01-31"}}}}
				for how, opt := range reads {
					data, err := m.BIChartData(ctx, w, c, nil, opt)
					if want := [][]any{{"Sun", 1.0}, {"Mon", 1.0}}; err != nil || !reflect.DeepEqual(data.Rows, want) {
						t.Errorf("%s %s zoned: %v %v\n%s", mode, how, err, data.Rows, data.SQL)
					}
				}
				// Filters are wall-clock time in the zone: Monday Jan 19 there holds
				// Jan 20 00:00 UTC, and nothing of Jan 4 10:00 UTC (02:00 that day).
				days := bi.Chart{DataSource: src.ID, Type: "value", Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}}
				for _, tc := range []struct {
					f    bi.Filter
					want float64
				}{
					{bi.Filter{Field: "placed_at", Op: "=", Values: []string{"2026-01-19"}}, 80},
					{bi.Filter{Field: "placed_at", Op: "between", Values: []string{"2026-01-04", "2026-01-19"}}, 200},
					{bi.Filter{Field: "placed_at", Op: ">=", Values: []string{"2026-01-04 03:00:00"}}, 710.5},
					{bi.Filter{Field: "placed_at", Op: "in", Values: []string{"2026-01-04 02:00:00", "2026-01-19 16:00:00"}}, 200},
				} {
					days.Filters = []bi.Filter{tc.f}
					data, err := m.BIChartData(ctx, w, days, nil, core.BIRead{Uncached: true})
					if err != nil || len(data.Rows) != 1 || data.Rows[0][0] != tc.want {
						t.Errorf("%s zoned filter %s %v: %v %v\n%s", mode, tc.f.Op, tc.f.Values, err, data.Rows, data.SQL)
					}
				}
			}
			for _, mode := range []string{bi.ModeLive, bi.ModeArchive} {
				src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Orders " + mode}, ConnID: id, Database: e.database, Schema: e.schema, SQL: sql(ts), Mode: mode, CustomFields: custom})
				if err != nil {
					t.Fatal(err)
				}
				w, _ := bi.Read(p)
				for _, tc := range charts {
					c := tc.c
					c.DataSource = src.ID
					for how, opt := range reads {
						data, err := m.BIChartData(ctx, w, c, nil, opt)
						if err != nil {
							t.Errorf("%s %s %s: %v", mode, how, tc.name, err)
							continue
						}
						if !reflect.DeepEqual(data.Rows, tc.want) {
							t.Errorf("%s %s %s:\n got %v\nwant %v\n%s", mode, how, tc.name, data.Rows, tc.want, data.SQL)
						}
					}
				}
			}
		})
	}
}

// Joined tables give the same rows on every engine.
func TestBIJoinsAcrossEngines(t *testing.T) {
	var results []string
	for _, e := range engines() {
		m, id := setup(t, e)
		ctx := context.Background()
		p, err := m.Store.BI().Create(bi.New("joins " + string(e.conn.Type)))
		if err != nil {
			t.Fatal(err)
		}
		src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Orders"}, ConnID: id, Database: e.database, Schema: e.schema, Table: "orders", Mode: bi.ModeLive,
			Joins: []bi.Join{{Table: "customers", Type: "left", On: []bi.JoinOn{{Left: "customer_id", Right: "id"}}}}})
		if err != nil {
			t.Fatalf("%s: %v", e.conn.Type, err)
		}
		w, _ := bi.Read(p)
		data, err := m.BIChartData(ctx, w, bi.Chart{DataSource: src.ID, Type: "bar", Dimension: []bi.ChartField{{Field: "customers.full_name"}},
			Value: []bi.ChartField{{Field: "id", Aggregate: "count"}}, Sort: &bi.ChartSort{By: "dimension"}}, nil, core.BIRead{})
		if err != nil || len(data.Rows) == 0 {
			t.Fatalf("%s: %v %v\n%s", e.conn.Type, data.Rows, err, data.SQL)
		}
		results = append(results, fmt.Sprint(data.Rows))
	}
	if len(results) == 2 && results[0] != results[1] {
		t.Errorf("engines differ:\n%s\n%s", results[0], results[1])
	}
}

// A chart read that's given up on is stopped on the server, not left running
// there, and at most BIMaxSessions BI reads use the server at once.
func TestBIReadsOnTheServer(t *testing.T) {
	for _, e := range engines() {
		t.Run(string(e.conn.Type), func(t *testing.T) {
			m, id := setup(t, e)
			sleep, running := "SELECT SLEEP(30) AS s", "SELECT COUNT(*) FROM information_schema.processlist WHERE info LIKE CONCAT('%SLE', 'EP(30)%')"
			if e.conn.Type == model.PostgreSQL {
				sleep, running = "SELECT pg_sleep(30)::text AS s", "SELECT COUNT(*) FROM pg_stat_activity WHERE query LIKE '%pg_' || 'sleep(30)%' AND state = 'active'"
			}
			w := bi.New("slow")
			w.DataSources = []bi.DataSource{{Meta: bi.Meta{ID: "slow", Name: "Slow"}, ConnID: id, Database: e.database, Schema: e.schema, SQL: sleep,
				Mode: bi.ModeLive, Fields: []bi.Field{{Name: "s", Type: bi.TypeString}}}}
			chart := bi.Chart{DataSource: "slow", Type: "value", Value: []bi.ChartField{{Field: "s", Aggregate: "count"}}}
			count := func() int {
				t.Helper()
				var n int
				err := m.WithSession(context.Background(), id, e.database, e.schema, func(ds driver.Session, _ driver.Dialect) error {
					rows, err := ds.Query(context.Background(), running)
					if err != nil {
						return err
					}
					defer rows.Close()
					cells := make([]*string, 1)
					if rows.Next(cells) {
						n, _ = strconv.Atoi(*cells[0])
					}
					return rows.Err()
				})
				if err != nil {
					t.Fatal(err)
				}
				return n
			}
			// Six charts at once, given up on after a second: no more than BIMaxSessions
			// reach the server, and none is still running afterwards.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			var wg sync.WaitGroup
			peak := 0
			for range 6 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _ = m.BIChartData(ctx, w, chart, nil, core.BIRead{Uncached: true})
				}()
			}
			time.Sleep(600 * time.Millisecond)
			peak = count()
			wg.Wait()
			if peak == 0 || peak > core.BIMaxSessions {
				t.Errorf("%d reads on the server at once, want 1..%d", peak, core.BIMaxSessions)
			}
			deadline := time.Now().Add(time.Second)
			for count() > 0 {
				if time.Now().After(deadline) {
					t.Fatal("abandoned reads still running on the server")
				}
				time.Sleep(100 * time.Millisecond)
			}
		})
	}
}

// Cached results are the database's own: text groups, filters and sorts by its
// collation (MySQL's is case-insensitive), not by SQLite's.
func TestBICacheKeepsDatabaseRules(t *testing.T) {
	for _, e := range engines() {
		t.Run(string(e.conn.Type), func(t *testing.T) {
			m, id := setup(t, e)
			w := bi.New("case")
			w.DataSources = []bi.DataSource{{Meta: bi.Meta{ID: "s", Name: "s"}, ConnID: id, Database: e.database, Schema: e.schema, Mode: bi.ModeLive,
				SQL:    "SELECT 'North' AS region, 1 AS n UNION ALL SELECT 'north', 2 UNION ALL SELECT 'NORTH', 4 UNION ALL SELECT 'South', 8",
				Fields: []bi.Field{{Name: "region", Type: bi.TypeString}, {Name: "n", Type: bi.TypeNumber}}}}
			value := func(f bi.Filter) bi.Chart {
				return bi.Chart{DataSource: "s", Type: "value", Value: []bi.ChartField{{Field: "n", Aggregate: "sum"}}, Filters: []bi.Filter{f}}
			}
			charts := []bi.Chart{
				{DataSource: "s", Type: "bar", Dimension: []bi.ChartField{{Field: "region"}}, Value: []bi.ChartField{{Field: "n", Aggregate: "sum"}}, Sort: &bi.ChartSort{By: "dimension"}},
				value(bi.Filter{Field: "region", Op: "=", Values: []string{"north"}}),
				value(bi.Filter{Field: "region", Op: "in", Values: []string{"NORTH"}}),
				{DataSource: "s", Type: "value", Value: []bi.ChartField{{Field: "region", Aggregate: "count_distinct"}}},
				{DataSource: "s", Type: "value", Value: []bi.ChartField{{Field: "region", Aggregate: "max"}}},
			}
			for i, c := range charts {
				db, err := m.BIChartData(context.Background(), w, c, nil, core.BIRead{Uncached: true})
				if err != nil {
					t.Fatal(err)
				}
				for range 2 {
					got, err := m.BIChartData(context.Background(), w, c, nil, core.BIRead{})
					if err != nil || !reflect.DeepEqual(got.Rows, db.Rows) {
						t.Errorf("chart %d: cached %v, database %v (%v)", i, got.Rows, db.Rows, err)
					}
				}
			}
		})
	}
}

// A zoned date filter keeps the column bare, so the database uses its index.
func TestBIZonedFilterUsesIndex(t *testing.T) {
	for _, e := range engines() {
		t.Run(string(e.conn.Type), func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			d, err := m.Dialect(id)
			if err != nil {
				t.Fatal(err)
			}
			driverName, dsn := "mysql", "root:chloe@tcp(127.0.0.1:33306)/shop"
			table, cols := "bi_tz_idx", "(id INT PRIMARY KEY, at %s, v INT)"
			ts := []string{"DATETIME", "TIMESTAMP"}
			if e.conn.Type == model.PostgreSQL {
				driverName, dsn, table = "pgx", "postgres://postgres:chloe@127.0.0.1:55432/postgres", "shop.bi_tz_idx"
				ts = []string{"timestamp", "timestamptz"}
			}
			db, err := sql.Open(driverName, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			conn, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			exec := func(q string) {
				if _, err := conn.ExecContext(ctx, q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			t.Cleanup(func() { db.Exec("DROP TABLE IF EXISTS " + table) })
			for _, typ := range ts {
				exec("DROP TABLE IF EXISTS " + table)
				exec("CREATE TABLE " + table + " " + fmt.Sprintf(cols, typ))
				exec("CREATE INDEX bi_tz_idx_at ON " + table + " (at)")
				if e.conn.Type == model.PostgreSQL {
					exec("INSERT INTO " + table + " SELECT i, TIMESTAMP '2020-01-01' + i * INTERVAL '7 hours', i FROM generate_series(0, 1999) i")
					exec("ANALYZE " + table)
					exec("SET enable_seqscan = off") // a seq scan only when there's no way to use the index
				} else {
					exec("SET SESSION cte_max_recursion_depth = 5000")
					exec("INSERT INTO " + table + " SELECT n, TIMESTAMP '2020-01-01 00:00:00' + INTERVAL n * 7 HOUR, n FROM (WITH RECURSIVE s(n) AS (SELECT 0 UNION ALL SELECT n + 1 FROM s WHERE n < 1999) SELECT n FROM s) x")
					exec("ANALYZE TABLE " + table)
				}
				src := bi.DataSource{Table: "bi_tz_idx", Database: e.database, Schema: e.schema, Timezone: "America/Los_Angeles",
					Fields: []bi.Field{{Name: "at", Type: bi.TypeDateTime, DBType: strings.ToUpper(typ)}, {Name: "v", Type: bi.TypeNumber}}}
				from, err := bi.From(d, src)
				if err != nil {
					t.Fatal(err)
				}
				c := bi.Chart{Type: "value", Value: []bi.ChartField{{Field: "v", Aggregate: "sum"}},
					Filters: []bi.Filter{{Field: "at", Op: "between", Values: []string{"2020-03-01", "2020-03-02"}}}}
				q, _, err := bi.ChartQuery(d, from, src, c, 10)
				if err != nil {
					t.Fatal(err)
				}
				explain := "EXPLAIN FORMAT=TREE "
				if e.conn.Type == model.PostgreSQL {
					explain = "EXPLAIN "
				}
				rows, err := conn.QueryContext(ctx, explain+q.SQL, q.Args...)
				if err != nil {
					t.Fatalf("%s: %v", q.SQL, err)
				}
				var plan strings.Builder
				for rows.Next() {
					var line string
					rows.Scan(&line)
					plan.WriteString(line + "\n")
				}
				rows.Close()
				if !strings.Contains(plan.String(), "bi_tz_idx_at") {
					t.Errorf("%s: the index isn't used:\n%s\n%s", typ, q.SQL, plan.String())
				}
				// And the filter still means wall-clock days in the zone.
				var sum sql.NullInt64
				if err := conn.QueryRowContext(ctx, q.SQL, q.Args...).Scan(&sum); err != nil {
					t.Fatal(err)
				}
				var want int64
				for i := int64(0); i < 2000; i++ {
					at := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i*7) * time.Hour)
					if l := at.In(mustZone(t, "America/Los_Angeles")); !l.Before(time.Date(2020, 3, 1, 0, 0, 0, 0, l.Location())) && l.Before(time.Date(2020, 3, 3, 0, 0, 0, 0, l.Location())) {
						want += i
					}
				}
				if sum.Int64 != want {
					t.Errorf("%s: sum %d, want %d", typ, sum.Int64, want)
				}
			}
		})
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}
