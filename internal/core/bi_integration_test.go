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
			[][]any{{"2026-W01", 1.0}, {"2026-W02", 0.0}, {"2026-W03", 1.0}}}, // the empty week filled
		{"extracts", bi.Chart{Type: "bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "weekday_x"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "max"}}},
			[][]any{{"0", 120.0}, {"1", 210.0}, {"2", 80.0}, {"4", 15.0}, {"6", 300.0}}},
		{"series and filters", bi.Chart{Type: "stacked_bar", Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "quarter"}}, Series: []bi.ChartField{{Field: "region"}},
			Value:   []bi.ChartField{{Field: "total", Aggregate: "sum"}},
			Filters: []bi.Filter{{Field: "region", Op: "in", Values: []string{"North", "South"}}, {Field: "region", Op: "contains", Values: []string{"OR"}}}},
			[][]any{{"2026-Q1", "North", 375.5}}},
		{"value", bi.Chart{Type: "value", Value: []bi.ChartField{{Field: "region", Aggregate: "count_distinct"}},
			Filters: []bi.Filter{{Field: "total", Op: "between", Values: []string{"50", "200"}}}},
			[][]any{{2.0}}},
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
			for _, mode := range []string{bi.ModeLive, bi.ModeArchive} {
				src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Orders " + mode}, ConnID: id, Database: e.database, Schema: e.schema, SQL: sql(ts), Mode: mode})
				if err != nil {
					t.Fatal(err)
				}
				w, _ := bi.Read(p)
				for _, tc := range charts {
					c := tc.c
					c.DataSource = src.ID
					data, err := m.BIChartData(ctx, w, c)
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
