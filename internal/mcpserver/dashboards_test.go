package mcpserver_test

import (
	"strings"
	"testing"

	"chloe/internal/bi"
	"chloe/internal/store"
)

func TestDashboardTools(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	f.enable(t, func(c *store.MCPCapabilities) { c.Reports = true })
	ws := f.call(t, "create_workspace", map[string]any{"name": "Ops"}, "")
	p := ws["path"].(string)
	f.call(t, "create_data_source", map[string]any{"workspace": "Ops", "name": "People", "connection": "shop", "table": "customers"}, "")
	f.call(t, "create_chart", map[string]any{"workspace": "Ops", "name": "Count", "data_source": "People", "type": "value", "value": []any{map[string]any{"field": "id", "aggregate": "count"}}}, "")
	f.call(t, "create_chart", map[string]any{"workspace": "Ops", "name": "By name", "data_source": "People", "type": "bar",
		"dimension": []any{map[string]any{"field": "name"}}, "value": []any{map[string]any{"field": "id", "aggregate": "count"}}}, "")

	f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "D", "pages": []any{map[string]any{"rows": []any{
		map[string]any{"tiles": []any{map[string]any{"chart": "nope"}}}}}}}, `no chart "nope"`)
	d := f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "Weekly", "pages": []any{map[string]any{"name": "Overview", "rows": []any{
		map[string]any{"height": "S", "tiles": []any{map[string]any{"text": "# Weekly people"}}},
		map[string]any{"tiles": []any{map[string]any{"chart": "Count", "span": 4}, map[string]any{"chart": "by name"}}},
	}}}}, "")
	items := d["pages"].([]any)[0].(map[string]any)["items"].([]any)
	if d["editable"] != true || len(items) != 3 || d["pages"].([]any)[0].(map[string]any)["size"] != "report" {
		t.Fatalf("create: %v", d)
	}
	if last := items[2].(map[string]any); last["x"] != 4.0 || last["w"] != 8.0 || last["y"] != 2.0 { // the text row fits its text
		t.Errorf("layout: %v", last)
	}

	r := f.call(t, "refresh_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly"}, "")
	page := r["pages"].([]any)[0].(map[string]any)
	charts := page["charts"].([]any)
	if len(charts) != 2 || charts[0].(map[string]any)["rows"].([]any)[0].([]any)[0] != 3.0 || page["text"].([]any)[0] != "# Weekly people" {
		t.Fatalf("refresh: %v", r)
	}

	// Notes: added at the top of a report page, then replaced in place.
	n := f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "Three customers; **Cy** has no name."}, "")
	if n["y"] != 0.0 || n["h"] != 2.0 || n["updated"] == "" || n["page"] != 1.0 || n["items"] != nil {
		t.Fatalf("notes: %v", n)
	}
	its := itemsOf(t, f, "Weekly")
	if its[0].CreatedBy != "mcp:test" || its[1].Y != 2 {
		t.Fatalf("notes on top: %+v", its)
	}
	// Moved to the bottom on request; replaced in place otherwise.
	if n = f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "Below.", "place": "bottom"}, ""); n["y"] != 7.0 {
		t.Fatalf("bottom: %v", n)
	}
	if its = itemsOf(t, f, "Weekly"); its[0].Y != 0 || its[0].Kind != "text" || its[0].CreatedBy != "" {
		t.Fatalf("gap closed: %+v", its)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "x", "place": "middle"}, "top or bottom")
	n = f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "page": "Overview", "markdown": "Updated.", "place": "top"}, "")
	if its = itemsOf(t, f, "Weekly"); len(its) != 4 || its[0].Text != "Updated." || n["y"] != 0.0 {
		t.Fatalf("replace notes: %+v", its)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "page": "3", "markdown": "x"}, "has 1 page")
	r = f.call(t, "refresh_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly"}, "")
	page = r["pages"].([]any)[0].(map[string]any)
	if nt := page["notes"].([]any)[0].(map[string]any); nt["by"] != "test" || nt["markdown"] != "Updated." || len(page["text"].([]any)) != 1 {
		t.Errorf("text and notes apart: %v", page)
	}
	// Bottom notes stay at the bottom through an edit, below everything else.
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "At the end.", "place": "bottom"}, "")
	noOverlap(t, itemsOf(t, f, "Weekly"))
	f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "pages": []any{map[string]any{"rows": []any{
		map[string]any{"tiles": []any{map[string]any{"text": "# Weekly people"}}},
		map[string]any{"tiles": []any{map[string]any{"chart": "Count", "span": 4}, map[string]any{"chart": "by name"}}}}}}}, "")
	its = itemsOf(t, f, "Weekly")
	noOverlap(t, its)
	if last := its[len(its)-1]; last.CreatedBy != "mcp:test" || last.Y != 7 {
		t.Fatalf("bottom notes after edit: %+v", its)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "Updated.", "place": "top"}, "")
	noOverlap(t, itemsOf(t, f, "Weekly"))

	// Re-laying out the page keeps the notes.
	ed := f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "pages": []any{map[string]any{"rows": []any{
		map[string]any{"tiles": []any{map[string]any{"chart": "Count"}}}}}}}, "")
	kept := ed["pages"].([]any)[0].(map[string]any)["items"].([]any)
	if len(kept) != 2 || kept[0].(map[string]any)["text"] != "Updated." || kept[1].(map[string]any)["y"] != 2.0 {
		t.Errorf("notes after edit_dashboard: %v", kept)
	}

	// The user's dashboard: notes yes, layout no; a full slide has no room.
	lib := f.m.Store.BI()
	w, _ := bi.Read(p)
	count := w.Charts[0].ID
	if _, err := lib.Update(p, func(w *bi.Workspace) error {
		_, err := w.PutDashboard(bi.Dashboard{Meta: bi.Meta{Name: "Mine"}, Pages: []bi.Page{
			{Size: bi.SizeSlide, Items: []bi.Item{{Kind: bi.ItemChart, Chart: count, W: 12, H: 8}}},
			{Size: bi.SizeSlide, Items: []bi.Item{{Kind: bi.ItemChart, Chart: count, W: 12, H: 12}}}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Mine", "rename": "x"}, "user's dashboards")
	f.call(t, "delete_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Mine"}, "user's dashboards")
	n = f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Mine", "markdown": "Looks fine."}, "")
	if n["y"] != 8.0 || n["h"] != 2.0 {
		t.Errorf("slide notes: %v", n)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Mine", "page": "2", "markdown": "x"}, "no room")

	f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "refresh": 5, "rename": "Weekly report"}, "")
	if gw := f.call(t, "get_workspace", map[string]any{"workspace": "Ops"}, ""); len(gw["dashboards"].([]any)) != 2 || gw["dashboard_count"] != 2.0 {
		t.Fatalf("get_workspace: %v", gw["dashboards"])
	}
	if o := f.call(t, "open_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly report"}, ""); o["pages"] != 1.0 || o["items"] != nil {
		t.Errorf("open: %v", o)
	}
	if last := f.events.opened[len(f.events.opened)-1]; !strings.HasPrefix(last, p+"#") {
		t.Errorf("open: %v", f.events.opened)
	}
	// A deleted chart leaves its dashboards.
	f.call(t, "delete_chart", map[string]any{"workspace": "Ops", "chart": "Count"}, "")
	if r := f.call(t, "refresh_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly report"}, ""); len(r["pages"].([]any)[0].(map[string]any)["charts"].([]any)) != 0 {
		t.Errorf("after deleting a chart: %v", r)
	}
	f.call(t, "delete_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly report"}, "")
}

// itemsOf reads a dashboard's first page from the workspace file.
func itemsOf(t *testing.T, f *fixture, dashboard string) []bi.Item {
	t.Helper()
	p, err := f.m.Store.BI().Find("Ops")
	if err != nil {
		t.Fatal(err)
	}
	w, _ := bi.Read(p)
	for _, d := range w.Dashboards {
		if d.Name == dashboard {
			return d.Pages[0].Items
		}
	}
	t.Fatalf("no dashboard %q", dashboard)
	return nil
}

// noOverlap fails when two tiles share a grid cell.
func noOverlap(t *testing.T, items []bi.Item) {
	t.Helper()
	for i, a := range items {
		for _, b := range items[i+1:] {
			if a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H {
				t.Errorf("tiles overlap: %+v and %+v", a, b)
			}
		}
	}
}
