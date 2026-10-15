package core_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"chloe/internal/bi"
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
			for _, mode := range []string{bi.ModeLive, bi.ModeArchive} {
				src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Zoned " + mode}, ConnID: id, Database: e.database, Schema: e.schema, SQL: sql(ts), Mode: mode, Timezone: "America/Los_Angeles"})
				if err != nil {
					t.Fatal(err)
				}
				w, _ := bi.Read(p)
				c := bi.Chart{DataSource: src.ID, Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "weekday_x"}}, Value: []bi.ChartField{{Field: "region", Aggregate: "count"}},
					Filters: []bi.Filter{{Field: "placed_at", Op: "<", Values: []string{"2026-01-31"}}}}
				data, err := m.BIChartData(ctx, w, c, nil)
				if want := [][]any{{"Sun", 1.0}, {"Mon", 1.0}}; err != nil || !reflect.DeepEqual(data.Rows, want) {
					t.Errorf("%s zoned: %v %v\n%s", mode, err, data.Rows, data.SQL)
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
					data, err := m.BIChartData(ctx, w, c, nil)
					if err != nil {
						t.Errorf("%s %s: %v", mode, tc.name, err)
						continue
					}
					if !reflect.DeepEqual(data.Rows, tc.want) {
						t.Errorf("%s %s:\n got %v\nwant %v\n%s", mode, tc.name, data.Rows, tc.want, data.SQL)
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
			Value: []bi.ChartField{{Field: "id", Aggregate: "count"}}, Sort: &bi.ChartSort{By: "dimension"}}, nil)
		if err != nil || len(data.Rows) == 0 {
			t.Fatalf("%s: %v %v\n%s", e.conn.Type, data.Rows, err, data.SQL)
		}
		results = append(results, fmt.Sprint(data.Rows))
	}
	if len(results) == 2 && results[0] != results[1] {
		t.Errorf("engines differ:\n%s\n%s", results[0], results[1])
	}
}
