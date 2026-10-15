package mcpserver_test

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/bi"
	"chloe/internal/model"
	"chloe/internal/store"
)

func TestReports(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	if _, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_workspaces", Arguments: map[string]any{}}); err == nil {
		t.Fatal("list_workspaces without the capability")
	}
	f.enable(t, func(c *store.MCPCapabilities) { c.Reports = true })
	f.exec(t, "main", "", `UPDATE customers SET vip = 1 WHERE id = 1;
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER, total REAL, placed_at TEXT);
INSERT INTO orders VALUES (1, 1, 10, '2026-01-05'), (2, 1, 15, '2026-02-01'), (3, 2, 5, '2026-02-20'), (4, 3, 30, '2026-03-02');`)

	ws := f.call(t, "create_workspace", map[string]any{"name": "Sales"}, "")
	if ws["created_by"] != "mcp:test" || len(f.events.bi) != 1 {
		t.Fatalf("workspace: %v %v", ws, f.events.bi)
	}
	f.call(t, "create_data_source", map[string]any{"workspace": "Sales", "name": "x", "connection": "shop", "sql": "DELETE FROM orders"}, "reads data")
	f.call(t, "create_data_source", map[string]any{"workspace": "Sales", "name": "x", "connection": "shop", "sql": "SELECT 1; SELECT 2"}, "exactly one statement")
	f.call(t, "create_data_source", map[string]any{"workspace": "nope", "name": "x", "connection": "shop", "table": "orders"}, "no workspace")
	src := f.call(t, "create_data_source", map[string]any{"workspace": "Sales", "name": "Orders", "connection": "shop",
		"sql": "SELECT o.*, c.email FROM orders o JOIN customers c ON c.id = o.customer_id", "filters": []any{map[string]any{"field": "total", "op": ">", "value": 6}}}, "")
	if src["mode"] != "live" || src["createdBy"] != "mcp:test" || len(src["fields"].([]any)) != 5 {
		t.Fatalf("source: %v", src)
	}

	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "c", "data_source": "Orders", "type": "pyramid"}, "unknown chart type")
	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "c", "data_source": "Orders", "type": "bar",
		"dimension": []any{map[string]any{"field": "nope"}}}, `no field "nope"`)
	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "c", "data_source": "Orders", "type": "bar",
		"dimension": []any{map[string]any{"field": "email"}}}, "needs value")
	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "c", "data_source": "Orders", "type": "bar",
		"dimension": []any{map[string]any{"field": "email"}}, "value": []any{map[string]any{"field": "total"}}, "properties": map[string]any{"titel": "x"}}, "unknown field")
	ch := f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "Revenue by month", "data_source": "Orders", "type": "line",
		"dimension": []any{map[string]any{"field": "placed_at", "bucket": "month"}}, "value": []any{map[string]any{"field": "total", "operation": "cumulative"}},
		"properties": map[string]any{"title": "Revenue", "number": map[string]any{"prefix": "$"}}}, "")
	if ch["editable"] != true || ch["value"].([]any)[0].(map[string]any)["aggregate"] != "sum" {
		t.Fatalf("chart: %v", ch)
	}
	data := f.call(t, "get_chart_data", map[string]any{"workspace": "Sales", "chart": "Revenue by month"}, "")
	rows := data["rows"].([]any)
	if want := []any{[]any{"2026-01", 10.0}, []any{"2026-02", 25.0}, []any{"2026-03", 55.0}}; !reflect.DeepEqual(rows, want) || data["complete"] != true || data["mode"] != "live" {
		t.Fatalf("data: %v", data)
	}

	// Edit the agent's chart; the user's charts stay read-only.
	ed := f.call(t, "edit_chart", map[string]any{"workspace": "Sales", "chart": "Revenue by month", "type": "pie", "rename": "Share",
		"dimension": []any{map[string]any{"field": "email"}}, "value": []any{map[string]any{"field": "id", "aggregate": "count"}}}, "")
	if ed["name"] != "Share" || ed["type"] != "pie" {
		t.Fatalf("edit: %v", ed)
	}
	if got := f.call(t, "get_chart_data", map[string]any{"workspace": "Sales", "chart": "Share"}, "")["rows"].([]any); len(got) != 2 {
		t.Fatalf("pie: %v", got)
	}
	if _, ok := data["sql"]; ok {
		t.Error("sql returned without include_sql")
	}
	if d := f.call(t, "get_chart_data", map[string]any{"workspace": "Sales", "chart": "Share", "include_sql": true}, ""); !strings.Contains(fmt.Sprint(d["sql"]), "SELECT") {
		t.Errorf("include_sql: %v", d["sql"])
	}

	// A chart read from get_workspace goes back into edit_chart unchanged.
	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "Scorecard", "data_source": "Orders", "type": "table",
		"value": []any{map[string]any{"field": "email"}, map[string]any{"field": "id", "aggregate": "count"}, map[string]any{"field": "total", "aggregate": "sum"}},
		"sort": map[string]any{"by": "value", "field": "total", "desc": true}, "filters": []any{map[string]any{"field": "placed_at", "op": "in_last", "value": []any{100, "year"}}}}, "")
	var got map[string]any
	for _, ch := range f.call(t, "get_workspace", map[string]any{"workspace": "Sales"}, "")["charts"].([]any) {
		if ch.(map[string]any)["name"] == "Scorecard" {
			got = ch.(map[string]any)
		}
	}
	args := map[string]any{"workspace": "Sales", "chart": "Scorecard"}
	for _, k := range []string{"type", "value", "filters", "sort", "props"} {
		args[k] = got[k]
	}
	f.call(t, "edit_chart", args, "")
	tbl := f.call(t, "get_chart_data", map[string]any{"workspace": "Sales", "chart": "Scorecard"}, "")["rows"].([]any)
	if want := []any{[]any{"cy@example.com", 1.0, 30.0}, []any{"ada@example.com", 2.0, 25.0}}; !reflect.DeepEqual(tbl, want) {
		t.Fatalf("table sorted by total: %v", tbl)
	}
	f.call(t, "edit_chart", map[string]any{"workspace": "Sales", "chart": "Scorecard", "sort": map[string]any{"by": "value", "field": "nope"}}, "isn't one of the chart's values")

	p := ws["path"].(string)
	lib := f.m.Store.BI()
	w, _ := bi.Read(p)
	if _, err := lib.Update(p, func(w *bi.Workspace) error {
		_, err := w.PutChart(bi.Chart{Meta: bi.Meta{Name: "Mine"}, DataSource: w.DataSources[0].ID, Type: "value", Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "edit_chart", map[string]any{"workspace": "Sales", "chart": "Mine", "type": "bar"}, "user's charts")
	if got := f.call(t, "get_chart_data", map[string]any{"workspace": p, "chart": "Mine"}, "")["rows"].([]any); !reflect.DeepEqual(got, []any{[]any{55.0}}) {
		t.Fatalf("user chart data: %v", got)
	}
	gw := f.call(t, "get_workspace", map[string]any{"workspace": "Sales"}, "")
	if len(gw["charts"].([]any)) != 3 || gw["data_sources"].([]any)[0].(map[string]any)["connection"] != "shop" {
		t.Fatalf("get_workspace: %v", gw)
	}

	// A source on a connection the endpoint can't see can't be read, even archived.
	other, err := f.m.Store.Save(model.Connection{Name: "hidden", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(t.TempDir(), "h.db"), Create: true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Update(p, func(w *bi.Workspace) error {
		s, err := w.PutSource(bi.DataSource{Meta: bi.Meta{Name: "Hidden"}, ConnID: other.ID, Database: "main", Table: "t", Mode: bi.ModeArchive, Fields: []bi.Field{{Name: "a", Type: bi.TypeNumber}}})
		if err != nil {
			return err
		}
		_, err = w.PutChart(bi.Chart{Meta: bi.Meta{Name: "Secret"}, DataSource: s.ID, Type: "value", Value: []bi.ChartField{{Field: "a", Aggregate: "sum"}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "get_chart_data", map[string]any{"workspace": "Sales", "chart": "Secret"}, "is shared with this endpoint")
	f.call(t, "create_chart", map[string]any{"workspace": "Sales", "name": "Peek", "data_source": "Hidden", "type": "value", "value": []any{map[string]any{"field": "a"}}}, "isn't shared")
	gw = f.call(t, "get_workspace", map[string]any{"workspace": "Sales"}, "")
	for _, raw := range append(gw["data_sources"].([]any), gw["charts"].([]any)...) {
		o := raw.(map[string]any)
		if o["name"] == "Hidden" && (o["table"] != nil || o["fields"] != nil || o["connection"] == "hidden") || o["name"] == "Secret" && o["value"] != nil {
			t.Errorf("hidden source shown: %v", o)
		}
	}

	f.call(t, "open_chart", map[string]any{"workspace": "Sales", "chart": "Share"}, "")
	if last := f.events.opened[len(f.events.opened)-1]; !strings.HasPrefix(last, p+"#") || w.ID == "" {
		t.Fatalf("open: %v", f.events.opened)
	}
	if ls := f.call(t, "list_workspaces", map[string]any{}, "")["workspaces"].([]any); len(ls) != 1 || ls[0].(map[string]any)["chart_count"] != 4.0 {
		t.Fatalf("list: %v", ls)
	}
}
