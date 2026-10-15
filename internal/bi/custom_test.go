package bi

import (
	"reflect"
	"strings"
	"testing"
)

func TestExpressions(t *testing.T) {
	src := ordersSource()
	field := func(n string) (Field, bool) { return fieldIn(src.Fields, n) }
	var args []any
	c := &exprCompiler{d: pg, fields: field, params: Values{"rate": "1.5"}, bind: func(v any) string { args = append(args, v); return pg.Placeholder(len(args)) }}
	for expr, want := range map[string]string{
		"[total] * [$rate]":                            `(CAST("total" AS numeric) * CAST($1 AS numeric))`,
		"total / 0":                                    `(CAST("total" AS numeric) / NULLIF(CAST($1 AS numeric), 0))`,
		"IF(status = 'paid', total, NULL)":             `(CASE WHEN ("status" = $1) THEN "total" ELSE NULL END)`,
		"customer & ' – ' & status":                    `(CAST((CAST("customer" AS TEXT) || CAST($1 AS TEXT)) AS TEXT) || CAST("status" AS TEXT))`,
		"YEAR(placed_at) >= 2026 AND NOT (total < -1)": `((CAST(EXTRACT(YEAR FROM CAST("placed_at" AS timestamp)) AS integer) >= $1) AND (NOT ("total" < (-$2))))`,
		"round(total, 1)":                              `ROUND(CAST("total" AS numeric), CAST($1 AS integer))`,
	} {
		args = nil
		n, err := parseExpr(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		got, _, err := c.compile(n)
		if err != nil || got != want {
			t.Errorf("%s:\n got %s %v\nwant %s", expr, got, err, want)
		}
	}
	for expr, want := range map[string]string{
		"":                    "empty",
		"total +":             "ends too soon",
		"(total":              "missing )",
		"SQRT(total)":         "unknown function SQRT",
		"ROUND()":             "takes 1 to 2",
		"total; DROP TABLE x": `unexpected ';'`,
		"'open":               "unclosed text",
		"nope * 2":            `no field "nope"`,
		"[$missing]":          "no parameter $missing",
	} {
		n, err := parseExpr(expr)
		if err == nil {
			_, _, err = c.compile(n)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: %v", expr, err)
		}
	}
}

func TestCustomFields(t *testing.T) {
	src := ordersSource()
	src.CustomFields = []CustomField{
		{Name: "net", Kind: CustomCalculated, Expression: "total * 0.9"},
		{Name: "twice", Kind: CustomCalculated, Expression: "net * 2"}, // uses the one before
		{Name: "who", Kind: CustomConcat, Fields: []string{"customer", "status"}, Separator: "/"},
		{Name: "flag", Kind: CustomMapped, Field: "status", Rules: []MapRule{{From: "Paid", To: "yes"}}, Unmapped: "null"},
	}
	if err := src.Validate(); err != nil {
		t.Fatal(err)
	}
	vis := src.Visible()
	if f, ok := src.Field("twice"); !ok || f.Type != TypeNumber || len(vis) != 8 {
		t.Fatalf("fields: %+v", vis)
	}
	// Each field is a layer; ? dialects take the outer layer's values first.
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "flag"}}, Value: []ChartField{{Field: "twice", Aggregate: "sum"}},
		Filters: []Filter{{Field: "who", Op: "contains", Values: []string{"a"}}}}
	q, _, err := ChartQuery(mysql, Table("`orders`"), src, c, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q.Args, []any{"paid", "yes", "/", 2.0, 0.9, "%a%"}) {
		t.Errorf("mysql args: %v\n%s", q.Args, q.SQL)
	}
	q, _, _ = ChartQuery(pg, Table(`"orders"`), src, c, 10)
	if !reflect.DeepEqual(q.Args, []any{0.9, 2.0, "/", "paid", "yes", "%a%"}) || !strings.Contains(q.SQL, "ILIKE $6") {
		t.Errorf("pg args: %v\n%s", q.Args, q.SQL)
	}
	for _, bad := range [][]CustomField{
		{{Name: "total", Kind: CustomCalculated, Expression: "1"}},
		{{Name: "x", Kind: CustomConcat, Fields: []string{"status"}}},
		{{Name: "x", Kind: CustomMapped, Field: "nope", Rules: []MapRule{{From: "a", To: "b"}}}},
		{{Name: "x", Kind: CustomTypeChanged, Field: "total", To: "money"}},
		{{Name: "x", Kind: "magic"}},
		{{Name: "x", Kind: CustomCalculated, Expression: "later * 2"}, {Name: "later", Kind: CustomCalculated, Expression: "1"}},
	} {
		s := ordersSource()
		s.CustomFields = bad
		if s.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// A custom order puts its values first, the rest after in label order.
	if got := []string{"c", "b", "z", "a"}; true {
		order := []string{"z", "b"}
		sortWith := func(xs []string) []string {
			out := append([]string(nil), xs...)
			for i := 1; i < len(out); i++ {
				for j := i; j > 0 && compareOrdered(order, out[j], out[j-1]) < 0; j-- {
					out[j], out[j-1] = out[j-1], out[j]
				}
			}
			return out
		}
		if r := sortWith(got); !reflect.DeepEqual(r, []string{"z", "b", "a", "c"}) {
			t.Errorf("order: %v", r)
		}
	}
}
