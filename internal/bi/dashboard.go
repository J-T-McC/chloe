package bi

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Dashboard is pages of charts and text laid out on a 12-column grid.
type Dashboard struct {
	Meta
	Pages []Page `json:"pages"`
	// Refresh reloads every chart on the open page this often (minutes; 0 = off).
	Refresh int `json:"refresh,omitempty"`
}

// Page sizes: a fixed 16:9 slide of PageRows rows, or a report that grows downward.
const (
	SizeSlide  = "16:9"
	SizeReport = "report"
	// PageCols is the grid's width in columns; PageRows a slide's height in rows.
	PageCols = 12
	PageRows = 12
)

type Page struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Size is 16:9 (a slide) or report (scrolls).
	Size  string `json:"size"`
	Items []Item `json:"items"`
	// Note is the page's note (Navicat's Page Note).
	Note string `json:"note,omitempty"`
}

// Item kinds.
const (
	ItemChart = "chart"
	ItemText  = "text"
)

// Item is a tile on a page: a chart of the workspace, or Markdown text.
type Item struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Chart is the chart's ID (kind chart).
	Chart string `json:"chart,omitempty"`
	// Text is Markdown (kind text).
	Text string `json:"text,omitempty"`
	// X, Y, W, H place the tile in grid columns and rows.
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
	// CreatedBy is "mcp:<endpoint>" for an agent's notes.
	CreatedBy string `json:"createdBy,omitempty"`
	// Updated is when an agent last wrote its notes.
	Updated string `json:"updated,omitempty"`
}

func (w *Workspace) Dashboard(id string) *Dashboard {
	if i := slices.IndexFunc(w.Dashboards, func(d Dashboard) bool { return d.ID == id }); i >= 0 {
		return &w.Dashboards[i]
	}
	return nil
}

// DashboardsUsing lists the dashboards that show a chart.
func (w *Workspace) DashboardsUsing(chartID string) []Dashboard {
	var out []Dashboard
	for _, d := range w.Dashboards {
		if slices.ContainsFunc(d.Pages, func(p Page) bool {
			return slices.ContainsFunc(p.Items, func(it Item) bool { return it.Kind == ItemChart && it.Chart == chartID })
		}) {
			out = append(out, d)
		}
	}
	return out
}

// PutDashboard adds a dashboard (no ID yet) or replaces the one with its ID.
func (w *Workspace) PutDashboard(d Dashboard) (Dashboard, error) {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return d, errors.New("a dashboard needs a name")
	}
	for _, o := range w.Dashboards {
		if o.ID != d.ID && strings.EqualFold(o.Name, d.Name) {
			return d, fmt.Errorf("the workspace already has a dashboard named %q", d.Name)
		}
	}
	if err := d.normalize(w); err != nil {
		return d, err
	}
	d.Modified = stamp()
	if old := w.Dashboard(d.ID); d.ID != "" && old != nil {
		d.Created, d.CreatedBy = old.Created, old.CreatedBy
		*old = d
		return d, nil
	}
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	d.Created = d.Modified
	w.Dashboards = append(w.Dashboards, d)
	return d, nil
}

// normalize gives pages and items IDs and checks them: known kinds, charts of
// this workspace, tiles inside the grid.
func (d *Dashboard) normalize(w *Workspace) error {
	if len(d.Pages) == 0 {
		d.Pages = []Page{{Size: SizeSlide}}
	}
	if d.Refresh < 0 {
		return errors.New("refresh can't be negative")
	}
	for i := range d.Pages {
		p := &d.Pages[i]
		if p.ID == "" {
			p.ID = uuid.NewString()
		}
		if p.Size == "" {
			p.Size = SizeSlide
		}
		if p.Size != SizeSlide && p.Size != SizeReport {
			return fmt.Errorf("page size is %s or %s, not %q", SizeSlide, SizeReport, p.Size)
		}
		if p.Items == nil {
			p.Items = []Item{}
		}
		seen := map[string]bool{}
		for j := range p.Items {
			it := &p.Items[j]
			if it.ID == "" || seen[it.ID] {
				it.ID = uuid.NewString()
			}
			seen[it.ID] = true
			switch it.Kind {
			case ItemChart:
				if w.Chart(it.Chart) == nil {
					return fmt.Errorf("page %d: no chart %q in the workspace", i+1, it.Chart)
				}
			case ItemText:
			default:
				return fmt.Errorf("page %d: a tile is a chart or text, not %q", i+1, it.Kind)
			}
			if it.W < 1 || it.H < 1 || it.X < 0 || it.Y < 0 || it.X+it.W > PageCols {
				return fmt.Errorf("page %d: tile at %d,%d (%dx%d) is outside the %d-column grid", i+1, it.X, it.Y, it.W, it.H, PageCols)
			}
			if p.Size == SizeSlide && it.Y+it.H > PageRows {
				return fmt.Errorf("page %d: tile at row %d with height %d runs past the slide's %d rows (use a report page for longer pages)", i+1, it.Y, it.H, PageRows)
			}
		}
	}
	return nil
}

// Row heights agents ask for, in grid rows.
var RowHeights = map[string]int{"S": 3, "M": 5, "L": 8, "XL": 11}

// RowTile is a tile in a row an agent describes: a chart or Markdown text,
// spanning some of the 12 columns.
type RowTile struct {
	Chart string
	Text  string
	Span  int
}

// LayoutRows turns rows of tiles into placed items, starting at row y. Spans
// left at 0 share what the row has left equally. On a report page (grow) a row
// with text is made tall enough for it.
func LayoutRows(rows [][]RowTile, heights []string, y int, grow bool, chartID func(string) (string, error)) ([]Item, int, error) {
	var items []Item
	for r, row := range rows {
		h := RowHeights["M"]
		if r < len(heights) && heights[r] != "" {
			v, ok := RowHeights[strings.ToUpper(heights[r])]
			if !ok {
				return nil, y, fmt.Errorf("row %d: height is S, M, L or XL, not %q", r+1, heights[r])
			}
			h = v
		}
		used, open := 0, 0
		for _, t := range row {
			if grow && t.Text != "" {
				w := t.Span
				if w <= 0 {
					w = PageCols / len(row)
				}
				h = max(h, TextRowsAt(t.Text, w))
			}
			used += max(t.Span, 0)
			if t.Span <= 0 {
				open++
			}
		}
		if used > PageCols {
			return nil, y, fmt.Errorf("row %d spans %d columns; a row has %d", r+1, used, PageCols)
		}
		x := 0
		for i, t := range row {
			w := t.Span
			if w <= 0 {
				w = (PageCols - used) / open
				if i == len(row)-1 || x+w > PageCols {
					w = PageCols - x
				}
			}
			if w < 1 {
				return nil, y, fmt.Errorf("row %d has no room left for tile %d", r+1, i+1)
			}
			it := Item{ID: uuid.NewString(), X: x, Y: y, W: w, H: h}
			switch {
			case t.Chart != "" && t.Text != "":
				return nil, y, fmt.Errorf("row %d tile %d: pass chart or text, not both", r+1, i+1)
			case t.Chart != "":
				id, err := chartID(t.Chart)
				if err != nil {
					return nil, y, fmt.Errorf("row %d tile %d: %w", r+1, i+1, err)
				}
				it.Kind, it.Chart = ItemChart, id
			default:
				it.Kind, it.Text = ItemText, t.Text
			}
			items = append(items, it)
			x += w
		}
		y += h
	}
	return items, y, nil
}

// TextRows estimates the grid rows Markdown needs across a whole page.
func TextRows(md string) int { return TextRowsAt(md, PageCols) }

// TextRowsAt estimates the grid rows Markdown needs in a tile w columns wide
// (pages are laid out 1280px wide, rows 48px apart plus 12px gaps).
func TextRowsAt(md string, w int) int {
	chars := max(12, (w*105-24)/8) // characters per line at 13.5px
	px := 30                      // padding
	for _, l := range strings.Split(strings.TrimSpace(md), "\n") {
		t := strings.TrimSpace(l)
		lineH, n := 21, len(t)
		switch {
		case strings.HasPrefix(t, "# "):
			lineH, n = 36, n*2
		case strings.HasPrefix(t, "## "):
			lineH, n = 28, n*3/2
		case strings.HasPrefix(t, "### "):
			lineH = 24
		}
		px += lineH * (1 + n/chars)
	}
	return max(2, min(30, (px+12+59)/60))
}

// Bottom is the first free row below a page's tiles.
func (p Page) Bottom() int {
	b := 0
	for _, it := range p.Items {
		b = max(b, it.Y+it.H)
	}
	return b
}
