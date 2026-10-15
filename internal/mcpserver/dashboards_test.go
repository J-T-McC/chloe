package mcpserver_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

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
	if last := items[2].(map[string]any); last["x"] != 4.0 || last["w"] != 8.0 || last["y"] != 2.0 { // the text row fits its text (a lone # heading: 2 rows)
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
	if n = f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Weekly", "markdown": "Below.", "place": "bottom"}, ""); n["y"] != 12.0 {
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
	if tf := page["text_fit"].([]any)[0].(map[string]any); tf["needed"] != 2.0 || tf["h"] != 2.0 || tf["fits"] != true {
		t.Errorf("text fit: %v", tf)
	}
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
	if last := its[len(its)-1]; last.CreatedBy != "mcp:test" || last.Y != 12 {
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
			{Size: bi.SizeSlide, Items: []bi.Item{{Kind: bi.ItemChart, Chart: count, W: 12, H: 16}}},
			{Size: bi.SizeSlide, Items: []bi.Item{{Kind: bi.ItemChart, Chart: count, W: 12, H: 24}}}}})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Mine", "rename": "x"}, "user's dashboards")
	f.call(t, "delete_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Mine"}, "user's dashboards")
	n = f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Mine", "markdown": "Looks fine."}, "")
	if n["y"] != 0.0 || n["h"] != 2.0 { // new notes go on top, on slides too
		t.Errorf("slide notes: %v", n)
	}
	if n := f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Mine", "markdown": "Looks fine.", "place": "bottom"}, ""); n["y"] != 16.0 {
		t.Errorf("slide notes moved to the bottom: %v", n)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Mine", "page": "2", "markdown": "x"}, "has 0 free")

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
	// An agent's page laid out by an older Chloe (a one-line heading 3 rows tall)
	// is fitted the next time the agent touches it.
	f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "Old", "pages": []any{map[string]any{"size": "16:9", "rows": []any{
		map[string]any{"tiles": []any{map[string]any{"text": "# Rooms by game"}}},
		map[string]any{"height": "M", "tiles": []any{map[string]any{"chart": "Count"}}},
		map[string]any{"tiles": []any{map[string]any{"text": "One more line."}}}}}}}, "")
	if _, err := lib.Update(p, func(w *bi.Workspace) error {
		for i := range w.Dashboards {
			if w.Dashboards[i].Name == "Old" {
				it := w.Dashboards[i].Pages[0].Items
				it[0].H, it[1].Y, it[2].Y, it[2].H = 6, 6, 16, 8 // fills the slide
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Old", "markdown": "x"}, "it's your dashboard, so shorten the notes, make room with edit_dashboard")
	f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Old", "rename": "Old layout"}, "")
	if its := itemsOf(t, f, "Old layout"); its[0].H != 2 || its[1].Y != 2 || its[2].Y != 12 || its[2].H != 2 {
		t.Errorf("refit: %+v", its)
	}
	if n := f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Old layout", "markdown": "Fits now.", "place": "bottom"}, ""); n["y"] != 14.0 {
		t.Errorf("notes in the freed rows: %v", n)
	}

	// Exports: PNGs come back to the agent and are saved; a PDF is saved.
	reports := t.TempDir()
	t.Setenv("CHLOE_REPORTS_DIR", reports)
	res, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "export_dashboard", Arguments: map[string]any{"workspace": "Ops", "dashboard": "Old layout"}})
	if err != nil || res.IsError {
		t.Fatalf("export: %v %+v", err, res)
	}
	var images int
	for _, ct := range res.Content {
		if im, ok := ct.(*mcp.ImageContent); ok && im.MIMEType == "image/png" && bytes.HasPrefix(im.Data, []byte("\x89PNG")) {
			images++
		}
	}
	files, _ := filepath.Glob(filepath.Join(reports, "Ops", "Old layout p1 *.png"))
	if images != 1 || len(files) != 1 || f.events.rendered[len(f.events.rendered)-1] != "[0]@1" {
		t.Fatalf("png export: %d images, files %v, %v", images, files, f.events.rendered)
	}
	pdf := f.call(t, "export_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly report", "format": "pdf"}, "")
	if data, _ := os.ReadFile(pdf["files"].([]any)[0].(string)); !bytes.HasPrefix(data, []byte("%PDF")) || f.events.rendered[len(f.events.rendered)-1] != "[0]@2" {
		t.Fatalf("pdf export: %v", pdf)
	}
	f.call(t, "export_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Weekly report", "format": "gif"}, "png or pdf")

	// Slides: rows that don't fit are named; notes are never cut off; notes follow
	// their page by name when pages move.
	f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "Tall", "pages": []any{map[string]any{"size": "16:9", "rows": []any{
		map[string]any{"height": "XL", "tiles": []any{map[string]any{"chart": "Count"}}},
		map[string]any{"height": "S", "tiles": []any{map[string]any{"chart": "Count"}}}}}}}, "its rows take 28 grid rows (row 1 22, row 2 6)")
	f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "X", "pages": []any{map[string]any{"rows": []any{
		map[string]any{"tiles": []any{map[string]any{"chart": "Nope"}}}}}}}, "(charts: ")
	f.call(t, "create_dashboard", map[string]any{"workspace": "Ops", "name": "Moves", "pages": []any{
		map[string]any{"name": "Overview", "rows": []any{map[string]any{"tiles": []any{map[string]any{"chart": "Count"}}}}},
		map[string]any{"name": "Slide", "size": "16:9", "rows": []any{map[string]any{"height": "L", "tiles": []any{map[string]any{"chart": "Count"}}}}}}}, "")
	long := "Notes:\n- one\n- two\n- three\n- four\n- five\n- six"
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Moves", "page": "Slide", "markdown": long + "\n- seven\n- eight\n- nine\n- ten"}, "need 9 grid rows but the 16:9 slide has 8 free")
	f.call(t, "set_notes", map[string]any{"workspace": "Ops", "dashboard": "Moves", "page": "Overview", "markdown": "On top.", "place": "top"}, "")
	sw := f.call(t, "edit_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Moves", "pages": []any{
		map[string]any{"name": "Slide", "size": "16:9", "rows": []any{map[string]any{"height": "L", "tiles": []any{map[string]any{"chart": "Count"}}}}},
		map[string]any{"name": "Overview", "rows": []any{map[string]any{"tiles": []any{map[string]any{"chart": "Count"}}}}}}}, "")
	pgs := sw["pages"].([]any)
	if a, b := pgs[0].(map[string]any)["items"].([]any), pgs[1].(map[string]any)["items"].([]any); len(a) != 1 || len(b) != 2 || b[0].(map[string]any)["text"] != "On top." || b[0].(map[string]any)["y"] != 0.0 {
		t.Fatalf("notes follow their page: %v", pgs)
	}
	// A chart on two pages is read once; text tiles report their fit.
	dup := f.call(t, "refresh_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Moves"}, "")["pages"].([]any)
	if a, b := dup[0].(map[string]any)["charts"].([]any)[0].(map[string]any), dup[1].(map[string]any)["charts"].([]any)[0].(map[string]any); a["as_of"] != b["as_of"] {
		t.Errorf("shared chart read twice: %v %v", a["as_of"], b["as_of"])
	}
	rf := f.call(t, "refresh_dashboard", map[string]any{"workspace": "Ops", "dashboard": "Moves", "page": "Overview"}, "")
	if nt := rf["pages"].([]any)[0].(map[string]any)["notes"].([]any)[0].(map[string]any); nt["place"] != "top" || nt["fits"] != true || nt["h"] != 2.0 || nt["needed"] != 2.0 {
		t.Errorf("notes in refresh: %v", nt)
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
