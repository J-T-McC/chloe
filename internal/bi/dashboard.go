package bi

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
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
	// Interaction is what clicking a point does to the page's other charts on the
	// same data source: filter ("" too), highlight or none.
	Interaction string `json:"interaction,omitempty"`
}

// Page sizes: a fixed 16:9 slide of PageRows rows, or a report that grows downward.
const (
	SizeSlide  = "16:9"
	SizeReport = "report"
	// PageCols is the grid's width in columns; PageRows a slide's height in rows.
	PageCols = 12
	PageRows = 24
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
	ItemShape = "shape"
)

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3}([0-9a-fA-F]{3})?$`)

// Shapes a shape tile draws (Navicat's line, arrow, rectangle, ellipse, triangle
// and icons).
var Shapes = []string{"line", "arrow", "rectangle", "ellipse", "triangle", "right_triangle", "right_arrow", "database", "cloud", "server", "desktop", "mobile", "person"}

// Item is a tile on a page: a chart of the workspace, Markdown text or a shape.
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
	// Targets are the charts a Control chart filters; empty means every chart on
	// the page that has its field.
	Targets []string `json:"targets,omitempty"`
	// Shape is what a shape tile draws (see Shapes), in Color (default the
	// theme's ink), filled or outlined LineWidth px wide, dashed or not.
	Shape     string `json:"shape,omitempty"`
	Color     string `json:"color,omitempty"`
	Filled    bool   `json:"filled,omitempty"`
	LineWidth int    `json:"lineWidth,omitempty"`
	Dashed    bool   `json:"dashed,omitempty"`
	// Locked tiles can't be moved or resized.
	Locked bool `json:"locked,omitempty"`
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
	if !slices.Contains([]string{"", "filter", "highlight", "none"}, d.Interaction) {
		return d, fmt.Errorf("interaction is filter, highlight or none, not %q", d.Interaction)
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
			case ItemShape:
				if !slices.Contains(Shapes, it.Shape) {
					return fmt.Errorf("page %d: a shape is one of %s, not %q", i+1, strings.Join(Shapes, ", "), it.Shape)
				}
				if it.Color != "" && !colorRe.MatchString(it.Color) {
					return fmt.Errorf("page %d: a shape's color is a hex color like #2a78d6, not %q", i+1, it.Color)
				}
			default:
				return fmt.Errorf("page %d: a tile is a chart, text or shape, not %q", i+1, it.Kind)
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
// (A grid row is 18px with 12px gaps, so tiles grow in 30px steps.)
var RowHeights = map[string]int{"S": 6, "M": 10, "L": 16, "XL": 22}

// RowTile is a tile in a row an agent describes: a chart or Markdown text,
// spanning some of the 12 columns.
type RowTile struct {
	Chart string
	Text  string
	Span  int
}

// LayoutRows turns rows of tiles into placed items, starting at row y. Spans
// left at 0 share what the row has left equally. A row of only text is exactly
// as tall as its text (its height label is ignored); a row with a chart takes
// its label's height, growing if its text needs more.
func LayoutRows(rows [][]RowTile, heights []string, y int, chartID func(string) (string, error)) ([]Item, int, error) {
	var items []Item
	for r, row := range rows {
		h := RowHeights["M"]
		explicit := r < len(heights) && heights[r] != ""
		textOnly := !slices.ContainsFunc(row, func(t RowTile) bool { return t.Chart != "" })
		if explicit {
			v, ok := RowHeights[strings.ToUpper(heights[r])]
			if !ok {
				return nil, y, fmt.Errorf("row %d: height is S, M, L or XL, not %q", r+1, heights[r])
			}
			h = v
		}
		if textOnly {
			h = 2
		}
		used, open := 0, 0
		for _, t := range row {
			if t.Text != "" {
				w := t.Span
				if w <= 0 {
					w = PageCols / len(row)
				}
				h = max(h, TextRowsAt(t.Text, w, false))
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

// TextRows is the grid rows Markdown needs across a whole page.
func TextRows(md string) int { return TextRowsAt(md, PageCols, false) }

// Text tile metrics, measured in Chloe at the 1280px page width: rows are 18px
// with 12px gaps, tiles have 18px of padding and border, and body text is 13.5px
// on 20.25px lines at about 7.2px a character. A notes tile's header is a caption
// beside its first line, taking about 34 characters of it.
const (
	rowPx      = 18
	gapPx      = 12
	tilePadPx  = 18
	notesHead  = 34 // characters
	lineHPx    = 20.25
	charPx     = 7.2
	blockGapPx = 4
)

// TextRowsAt is the grid rows Markdown needs in a tile w columns wide; notes
// tiles (an agent's) have a header line.
func TextRowsAt(md string, w int, notes bool) int {
	colPx := (float64(1280-2*gapPx) - float64((PageCols-1)*gapPx)) / PageCols
	tilePx := float64(w)*colPx + float64(w-1)*gapPx
	perLine := max(8, int((tilePx-34)/charPx))
	lines := func(s string, scale float64) float64 {
		n := int(float64(len([]rune(s))) * scale)
		return float64(max(1, (n+perLine-1)/perLine))
	}
	px := float64(tilePadPx)
	first := notes // the header shares the first line
	blocks := 0
	inPara := false
	trailing := 0.0 // a heading's bottom margin, dropped when nothing follows it
	for _, l := range strings.Split(strings.TrimSpace(md), "\n") {
		t := strings.TrimSpace(l)
		if first && t != "" {
			t = strings.Repeat(" ", notesHead) + t
			first = false
		}
		switch {
		case t == "":
			inPara = false
			continue
		case strings.HasPrefix(t, "# "):
			px += 26.4*lines(t, 1.65) + 6 // 22px at 1.2
			inPara, trailing = false, 6
			blocks++
			continue
		case strings.HasPrefix(t, "## "):
			px += 23.4*lines(t, 1.35) + 6 // 18px at 1.3
			inPara, trailing = false, 6
			blocks++
			continue
		case strings.HasPrefix(t, "### "), strings.HasPrefix(t, "#### "):
			px += 22.5*lines(t, 1.1) + 8
			inPara, trailing = false, 8
			blocks++
			continue
		case strings.HasPrefix(t, "- "), strings.HasPrefix(t, "* "), len(t) > 2 && t[0] >= '0' && t[0] <= '9' && strings.Contains(t[:min(4, len(t))], ". "):
			px += lineHPx * lines(t, 1)
			if !inPara {
				blocks++
			}
			inPara, trailing = true, 0
			continue
		case strings.HasPrefix(t, "|"):
			px += 27 // a table row with its padding
		default:
			px += lineHPx * lines(t, 1) // lines run on in a paragraph (breaks are on)
			if !inPara {
				blocks++
			}
			inPara, trailing = true, 0
			continue
		}
		blocks++
		trailing = 0
	}
	px += float64(max(0, blocks-1)*blockGapPx) - trailing
	// A tile h rows tall is h*18 + (h-1)*12 pixels (a slide's rows are 17.5px); text
	// that fits only to the pixel would still show a scrollbar, so keep 2px spare.
	h := int(math.Ceil((px + 2 + gapPx) / (rowPx + gapPx)))
	return max(2, min(60, h))
}

// Bottom is the first free row below a page's tiles.
func (p Page) Bottom() int {
	b := 0
	for _, it := range p.Items {
		b = max(b, it.Y+it.H)
	}
	return b
}

// FitText sizes a page's text rows to their text, moving the rows below: a row
// of only text becomes exactly as tall as its text; a row with a chart grows
// when its text needs more. It works on pages laid out in rows (tiles of a row
// share their top, rows don't interleave), as agents' are, and leaves other
// pages alone. A slide never grows past its 12 rows. It reports whether
// anything moved.
func (p *Page) FitText() bool {
	if len(p.Items) == 0 {
		return false
	}
	byY := map[int][]int{}
	var ys []int
	for i, it := range p.Items {
		if _, ok := byY[it.Y]; !ok {
			ys = append(ys, it.Y)
		}
		byY[it.Y] = append(byY[it.Y], i)
	}
	slices.Sort(ys)
	// Rows must not interleave: each row ends before the next begins.
	for k, y := range ys {
		end := 0
		for _, i := range byY[y] {
			end = max(end, y+p.Items[i].H)
		}
		if k+1 < len(ys) && end > ys[k+1] {
			return false
		}
	}
	items := slices.Clone(p.Items)
	shift := 0
	for _, y := range ys {
		row := byY[y]
		cur, need, textOnly := 0, 0, true
		for _, i := range row {
			it := items[i]
			cur = max(cur, it.H)
			if it.Kind == ItemText {
				need = max(need, TextRowsAt(it.Text, it.W, it.CreatedBy != ""))
			} else {
				textOnly = false
			}
		}
		h := cur
		if textOnly || need > cur {
			h = need
		}
		for _, i := range row {
			items[i].Y = y + shift
			if items[i].H == cur || textOnly {
				items[i].H = h
			}
		}
		shift += h - cur
	}
	if p.Size == SizeSlide {
		for _, it := range items {
			if it.Y+it.H > PageRows {
				return false
			}
		}
	}
	if slices.EqualFunc(items, p.Items, func(a, b Item) bool { return reflect.DeepEqual(a, b) }) {
		return false
	}
	p.Items = items
	return true
}

// PageWidth is the width pages are laid out at, in CSS pixels.
const PageWidth = 1280

// ReportsDir is where exports are written for agents: CHLOE_REPORTS_DIR, or
// "Chloe Reports" in the user's Documents.
func ReportsDir() string {
	if d := os.Getenv("CHLOE_REPORTS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "Chloe Reports"
	}
	return filepath.Join(home, "Documents", "Chloe Reports")
}
