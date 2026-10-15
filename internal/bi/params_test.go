package bi

import (
	"reflect"
	"strings"
	"testing"
)

func paramWorkspace(t *testing.T) Workspace {
	t.Helper()
	w := New("w")
	for _, p := range []Parameter{
		{Meta: Meta{Name: "min_total"}, Type: TypeNumber, Value: "50"},
		{Meta: Meta{Name: "region"}, Type: TypeString, Allowed: "list", List: []ParamOption{{Value: "North"}, {Value: "South", Label: "The south"}}, Value: "North"},
		{Meta: Meta{Name: "pct"}, Type: TypeNumber, Allowed: "range", Min: "0", Max: "100", Step: "5", Value: "20"},
	} {
		if _, err := w.PutParameter(p); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func TestParameterChecks(t *testing.T) {
	w := paramWorkspace(t)
	for p, want := range map[*Parameter]string{
		{Meta: Meta{Name: "1x"}, Type: TypeString}:                                                    "parameter name",
		{Meta: Meta{Name: "n"}, Type: TypeNumber, Value: "lots"}:                                      "is a number",
		{Meta: Meta{Name: "d"}, Type: TypeDateTime, Value: "soon"}:                                    "is a date",
		{Meta: Meta{Name: "l"}, Type: TypeString, Allowed: "list"}:                                    "at least one value",
		{Meta: Meta{Name: "r"}, Type: TypeNumber, Allowed: "range", Min: "5", Max: "1"}:               "minimum and a maximum",
		{Meta: Meta{Name: "r2"}, Type: TypeNumber, Allowed: "range", Min: "0", Max: "9", Value: "12"}: "between 0 and 9",
		{Meta: Meta{Name: "REGION"}, Type: TypeString}:                                                "already has a parameter",
	} {
		_, err := w.PutParameter(*p)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v", *p, err)
		}
	}
	if _, err := w.Values(map[string]string{"region": "East"}); err == nil || !strings.Contains(err.Error(), `"North", "South"`) {
		t.Errorf("list override: %v", err)
	}
	if _, err := w.Values(map[string]string{"nope": "1"}); err == nil || !strings.Contains(err.Error(), "min_total") {
		t.Errorf("unknown override: %v", err)
	}
	v, _ := w.Values(map[string]string{"$pct": "45"})
	if v.Expand("Top [$pct]% in [$Region], [$other]") != "Top 45% in North, [$other]" {
		t.Errorf("expand: %s", v.Expand("Top [$pct]% in [$Region], [$other]"))
	}
	if !reflect.DeepEqual(Refs("a [$x] b [$y] [$x] [$ z]"), []string{"x", "y"}) {
		t.Error("refs")
	}
}

func TestParametersInQueries(t *testing.T) {
	w := paramWorkspace(t)
	src := ordersSource()
	src.Table = ""
	src.SQL = "SELECT * FROM orders WHERE total >= [$min_total] -- not [$here]\n AND note <> 'x' /* [$nor] */"
	src.Filters = []Filter{{Field: "customer", Op: "=", Values: []string{"[$region]"}}}
	c := Chart{Type: "bar", Dimension: []ChartField{{Field: "status"}}, Value: []ChartField{{Field: "total", Aggregate: "sum"}},
		Filters: []Filter{{Field: "status", Op: "!=", Values: []string{"new"}}}}
	if err := src.Validate(); err != nil {
		t.Fatal(err) // a filter value may be a parameter
	}
	rs, rc, err := w.Resolve(src, c, map[string]string{"min_total": "75"})
	if err != nil {
		t.Fatal(err)
	}
	if src.Filters[0].Values[0] != "[$region]" {
		t.Fatal("resolving changed the stored filter")
	}
	from, err := From(pg, rs)
	if err != nil {
		t.Fatal(err)
	}
	q, _, err := ChartQuery(pg, from, rs, rc, 10)
	if err != nil {
		t.Fatal(err)
	}
	// The source's parameter is $1; the chart's own values follow.
	if !strings.Contains(q.SQL, "WHERE total >= $1 -- not [$here]") || !strings.Contains(q.SQL, `"customer" = $2 AND "status" <> $3`) {
		t.Errorf("sql: %s", q.SQL)
	}
	if !reflect.DeepEqual(q.Args, []any{"75", "North", "new"}) {
		t.Errorf("args: %v", q.Args)
	}
	// SQLite binds number parameters as numbers.
	from, _ = From(SQLite, rs)
	if !reflect.DeepEqual(from.Args, []any{75.0}) || !strings.Contains(from.SQL, "total >= ?") {
		t.Errorf("sqlite: %s %v", from.SQL, from.Args)
	}
	// A reference in quotes can only be a mistake; an unknown name is reported.
	rs.SQL = "SELECT * FROM orders WHERE region = '[$region]'"
	if _, err := From(pg, rs); err == nil || !strings.Contains(err.Error(), "without quotes") {
		t.Errorf("quoted: %v", err)
	}
	rs.SQL = "SELECT [$nope] AS x"
	if _, err := From(pg, rs); err == nil || !strings.Contains(err.Error(), "no parameter $nope") {
		t.Errorf("unknown: %v", err)
	}
	// Without resolving, a query that uses parameters can't run.
	src.SQL = "SELECT [$min_total] AS x"
	if _, err := From(pg, src); err == nil {
		t.Error("unresolved parameter ran")
	}
}
