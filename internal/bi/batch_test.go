package bi

import (
	"fmt"
	"strings"
	"testing"
)

func TestBatchQuery(t *testing.T) {
	src := ordersSource()
	src.Table, src.SQL = "", "SELECT * FROM orders"
	src.Filters = []Filter{{Field: "status", Op: "!=", Values: []string{"new"}}}
	src.CustomFields = []CustomField{{Name: "net", Kind: CustomCalculated, Expression: "total * 0.9"}}
	shared := []Filter{{Field: "customer", Op: "in", Values: []string{"a", "b"}}}
	charts := []Chart{
		{Type: "bar", Dimension: []ChartField{{Field: "placed_at", Aggregate: "month"}}, Series: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "net", Aggregate: "sum"}}, Filters: shared},
		{Type: "value", Value: []ChartField{{Field: "total", Aggregate: "max"}}, Filters: shared},
	}
	from := Source{SQL: "(SELECT * FROM orders WHERE x = $1)", Args: []any{"p"}}
	q, parts, err := BatchQuery(pg, from, src, charts, []int{100, 1})
	if err != nil {
		t.Fatal(err)
	}
	// The filters run once, in the shared read; the source's argument comes first.
	if !strings.HasPrefix(q.SQL, `WITH "bi_source" AS (SELECT * FROM (SELECT s.*, (CAST("total" AS numeric) * CAST($2 AS numeric)) AS "net" FROM (SELECT * FROM orders WHERE x = $1) AS s) AS s WHERE "status" <> $3 AND "customer" IN ($4, $5))`) ||
		strings.Count(q.SQL, "$3") != 1 || fmt.Sprint(q.Args) != "[p 0.9 new a b]" {
		t.Errorf("postgres shared read:\n%s\n%v", q.SQL, q.Args)
	}
	// A branch that never runs types every column; each chart has its columns, in its order.
	if !strings.Contains(q.SQL, `SELECT 0 AS p, b0."c0" AS v0, b0."c1" AS v1, b0."c2" AS v2, b1."c0" AS v3 FROM (`) || !strings.Contains(q.SQL, `WHERE false UNION ALL SELECT 0, b0."c0", b0."c1", b0."c2", NULL FROM (`) ||
		!strings.Contains(q.SQL, `UNION ALL SELECT 1, NULL, NULL, NULL, b1."c0" FROM (`) || !strings.HasSuffix(q.SQL, " ORDER BY 1, 2, 3") {
		t.Errorf("postgres branches:\n%s", q.SQL)
	}
	if fmt.Sprint(parts) != "[{0 3} {3 1}]" {
		t.Errorf("parts: %v", parts)
	}

	q, _, err = BatchQuery(mysql, Source{SQL: "(SELECT * FROM orders WHERE x = ?)", Args: []any{"p"}}, src, charts, []int{100, 1})
	if err != nil || strings.Contains(q.SQL, "WHERE false") || strings.Count(q.SQL, "SELECT /*+ NO_MERGE(s) */") != 2 ||
		!strings.Contains(q.SQL, "SELECT 0 AS p, b0.`c0` AS v0, b0.`c1` AS v1, b0.`c2` AS v2, NULL AS v3 FROM (") || fmt.Sprint(q.Args) != "[0.9 p new a b]" {
		t.Errorf("mysql: %v\n%s\n%v", err, q.SQL, q.Args)
	}

	charts[1].Filters = nil
	if _, _, err := BatchQuery(pg, from, src, charts, []int{100, 1}); err == nil {
		t.Error("charts with different filters batched")
	}
	rows := [][]*string{{s("0"), s("2026-01"), s("paid"), s("9"), nil}, {s("1"), nil, nil, nil, s("12")}}
	if got, err := SplitBatch(rows, 1, BatchPart{Offset: 3, Width: 1}); err != nil || len(got) != 1 || *got[0][0] != "12" {
		t.Errorf("split: %v %v", got, err)
	}
}

func s(v string) *string { return &v }
