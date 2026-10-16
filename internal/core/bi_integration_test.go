package core_test

import (
	"context"
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
