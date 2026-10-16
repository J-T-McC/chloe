package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/bi"
	"chloe/internal/core"
)

type TileArg struct {
	Chart string `json:"chart,omitempty" jsonschema:"a chart name or id in the workspace"`
	Text  string `json:"text,omitempty" jsonschema:"Markdown (headings, lists, bold, links, tables) instead of a chart"`
	Span  int    `json:"span,omitempty" jsonschema:"columns of 12 the tile spans; tiles without one share what the row has left"`
}

type RowArg struct {
	Height string    `json:"height,omitempty" jsonschema:"for rows with a chart: S (6 grid rows), M (10, default), L (16) or XL (22, which fills a 16:9 slide on its own). A 16:9 slide holds 24 grid rows in all (each 18px with 12px gaps). Rows of only text are always as tall as their text"`
	Tiles  []TileArg `json:"tiles"`
}

type PageArg struct {
	Name string   `json:"name,omitempty"`
	Size string   `json:"size,omitempty" jsonschema:"report (scrolls; the default) or 16:9 (a 24-row slide)"`
	Rows []RowArg `json:"rows"`
}

type CreateDashboardArgs struct {
	WorkspaceArgs
	Name    string    `json:"name"`
	Pages   []PageArg `json:"pages" jsonschema:"each page is rows of tiles across 12 columns; Chloe places them"`
	Refresh int       `json:"refresh,omitempty" jsonschema:"reload the charts every n minutes while the dashboard is open in Chloe (0 = off)"`
	// Interaction is what a click on a chart's point does for the user.
	Interaction string `json:"interaction,omitempty" jsonschema:"what clicking a chart's point does to the page's other charts on the same data source: filter (default), highlight or none. Control charts on the page filter the charts with their field"`
}

type EditDashboardArgs struct {
	WorkspaceArgs
	Dashboard   string    `json:"dashboard" jsonschema:"a dashboard name or id in the workspace"`
	Rename      string    `json:"rename,omitempty"`
	Pages       []PageArg `json:"pages,omitempty" jsonschema:"replaces every page"`
	Refresh     *int      `json:"refresh,omitempty"`
	Interaction *string   `json:"interaction,omitempty" jsonschema:"filter, highlight or none"`
}

type DashboardArgs struct {
	WorkspaceArgs
	Dashboard string `json:"dashboard" jsonschema:"a dashboard name or id in the workspace"`
}

type RefreshArgs struct {
	DashboardArgs
	Page       string            `json:"page,omitempty" jsonschema:"a page number (1 = first) or name; default every page"`
	Parameters map[string]string `json:"parameters,omitempty" jsonschema:"parameter values for this read only, by name (see get_workspace); the saved values don't change"`
}

type NotesArgs struct {
	DashboardArgs
	Page     string `json:"page,omitempty" jsonschema:"a page number (1 = first) or name; default 1"`
	Markdown string `json:"markdown" jsonschema:"your notes: what changed, what stands out. Replaces your earlier notes on that page"`
	Place    string `json:"place,omitempty" jsonschema:"top (default for new notes) or bottom of the page; omit to keep your notes where they are"`
}

type NotesOut struct {
	Dashboard string `json:"dashboard"`
	Page      int    `json:"page"`
	Y         int    `json:"y" jsonschema:"the notes tile's row"`
	H         int    `json:"h" jsonschema:"its height in rows"`
	Updated   string `json:"updated"`
}

type DashboardOpened struct {
	Dashboard string `json:"dashboard"`
	ID        string `json:"id"`
	Pages     int    `json:"pages"`
}

type ExportArgs struct {
	DashboardArgs
	Page       string            `json:"page,omitempty" jsonschema:"a page number (1 = first) or name; default every page"`
	Format     string            `json:"format,omitempty" jsonschema:"png (the default: one image per page, also returned to you so you can see the page) or pdf (one file of the pages)"`
	Parameters map[string]string `json:"parameters,omitempty" jsonschema:"parameter values to draw the pages with, by name; the saved values don't change"`
}

type ExportOut struct {
	Files  []string `json:"files" jsonschema:"where Chloe saved the export"`
	Pages  []int    `json:"pages"`
	Format string   `json:"format"`
}

type DashboardOut struct {
	bi.Dashboard
	Editable bool `json:"editable" jsonschema:"created over MCP, so edit_dashboard may change it"`
}

type ChartResult struct {
	Chart     string       `json:"chart"`
	Title     string       `json:"title,omitempty" jsonschema:"the title shown, parameters filled in"`
	ID        string       `json:"id"`
	Type      string       `json:"type"`
	Columns   []bi.Column  `json:"columns,omitempty"`
	Rows      [][]any      `json:"rows,omitempty"`
	Complete  bool         `json:"complete"`
	Truncated bool         `json:"truncated,omitempty"`
	Mode      string       `json:"mode,omitempty"`
	AsOf      string       `json:"as_of,omitempty"`
	Previous  *bi.Previous `json:"previous,omitempty"`
	Error     string       `json:"error,omitempty"`
}

type NoteOut struct {
	By       string `json:"by" jsonschema:"the endpoint that wrote them"`
	Updated  string `json:"updated,omitempty"`
	Markdown string `json:"markdown"`
	Y        int    `json:"y" jsonschema:"the grid row the notes start at"`
	H        int    `json:"h" jsonschema:"their height in grid rows"`
	Place    string `json:"place" jsonschema:"top or bottom of the page"`
	Needed   int    `json:"needed" jsonschema:"the rows the text needs; set_notes again resizes the notes to it"`
	Fits     bool   `json:"fits" jsonschema:"false when the text needs more rows than the notes have (it scrolls)"`
}

type TextFit struct {
	Y      int  `json:"y"`
	H      int  `json:"h" jsonschema:"the tile's height in grid rows"`
	Needed int  `json:"needed" jsonschema:"the rows its text needs; less than h means empty space, more means it scrolls"`
	Fits   bool `json:"fits"`
}

type PageResult struct {
	Page    int           `json:"page"`
	Name    string        `json:"name,omitempty"`
	Charts  []ChartResult `json:"charts"`
	Text    []string      `json:"text,omitempty" jsonschema:"the page's own text tiles (part of its layout)"`
	TextFit []TextFit     `json:"text_fit,omitempty" jsonschema:"each text tile's height against what its text needs, in the order of text; edit_dashboard re-fits text rows"`
	Notes   []NoteOut     `json:"notes,omitempty" jsonschema:"notes agents wrote with set_notes"`
}

type RefreshOut struct {
	Dashboard string       `json:"dashboard"`
	AsOf      string       `json:"as_of"`
	Pages     []PageResult `json:"pages"`
}

func (s *Server) addDashboardTools(srv *mcp.Server, ep string) {
	f := false
	t := true
	add(s, srv, ep, &mcp.Tool{Name: "create_dashboard", Description: "Creates a dashboard: pages of the workspace's charts and Markdown text, for the user to open as a report. " +
		"Describe each page as rows of tiles across 12 columns (e.g. a text row with a title and summary, then a row of three charts); Chloe lays them out.",
		Annotations: &mcp.ToolAnnotations{Title: "Create dashboard", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in CreateDashboardArgs) (DashboardOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DashboardOut{}, err
			}
			pages, err := c.layoutPages(&w, in.Pages)
			if err != nil {
				return DashboardOut{}, err
			}
			d := bi.Dashboard{Meta: bi.Meta{Name: in.Name, CreatedBy: "mcp:" + c.ep.Name}, Pages: pages, Refresh: in.Refresh, Interaction: in.Interaction}
			saved, err := c.putDashboard(p, d)
			return DashboardOut{Dashboard: saved, Editable: true}, err
		})

	add(s, srv, ep, &mcp.Tool{Name: "edit_dashboard", Description: "Changes a dashboard created over MCP: replaces its pages, renames it, or sets its refresh. " +
		"Notes written with set_notes stay on their page (matched by name, else by number), at its top or bottom. " +
		"The user's dashboards can't be changed, but set_notes can add your notes to them.",
		Annotations: &mcp.ToolAnnotations{Title: "Edit dashboard", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in EditDashboardArgs) (DashboardOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DashboardOut{}, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return DashboardOut{}, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			if !agentMade(d.CreatedBy) {
				return DashboardOut{}, errors.New("this is one of the user's dashboards; agents can't change its layout (set_notes adds notes to it), so create a dashboard of your own")
			}
			edited := *d
			if r := strings.TrimSpace(in.Rename); r != "" {
				edited.Name = r
			}
			if in.Interaction != nil {
				edited.Interaction = *in.Interaction
			}
			if in.Refresh != nil {
				edited.Refresh = *in.Refresh
			}
			if in.Pages != nil {
				if edited.Pages, err = c.layoutPages(&w, in.Pages); err != nil {
					return DashboardOut{}, err
				}
				// Notes written with set_notes stay on their page (the one with the
				// same name, else the same number), at its top or bottom as they were.
				for i := range edited.Pages {
					old := -1
					for j, op := range d.Pages {
						if op.Name != "" && strings.EqualFold(op.Name, edited.Pages[i].Name) {
							old = j
						}
					}
					if old < 0 && i < len(d.Pages) && !slices.ContainsFunc(edited.Pages, func(p bi.Page) bool {
						return d.Pages[i].Name != "" && strings.EqualFold(p.Name, d.Pages[i].Name)
					}) {
						old = i
					}
					if old < 0 {
						continue
					}
					for _, it := range d.Pages[old].Items {
						if it.Kind == bi.ItemText && it.CreatedBy != "" {
							place := "top"
							if it.Y > 0 && it.Y+it.H >= d.Pages[old].Bottom() {
								place = "bottom"
							}
							if err := setNotes(&edited.Pages[i], it.CreatedBy, it.Text, place); err != nil {
								var nr noRoom
								if errors.As(err, &nr) {
									err = fmt.Errorf("its notes by %s need %d grid rows at the %s and the slide would have %d free; leave room for them (smaller rows), or make the page a report", strings.TrimPrefix(it.CreatedBy, "mcp:"), nr.need, place, max(0, nr.free))
								}
								return DashboardOut{}, fmt.Errorf("page %d: %w", i+1, err)
							}
							for j := range edited.Pages[i].Items {
								if edited.Pages[i].Items[j].CreatedBy == it.CreatedBy {
									edited.Pages[i].Items[j].Updated = it.Updated
								}
							}
						}
					}
				}
			}
			// Text rows fit their text, also on pages laid out before.
			for i := range edited.Pages {
				edited.Pages[i].FitText()
			}
			saved, err := c.putDashboard(p, edited)
			return DashboardOut{Dashboard: saved, Editable: true}, err
		})

	add(s, srv, ep, &mcp.Tool{Name: "set_notes", Description: "Writes your Markdown notes on a dashboard page, shown in Chloe as \"Notes from " + "<endpoint>\" with the time. " +
		"Calling it again replaces your notes on that page. Works on the user's dashboards too; it never changes their charts or text.",
		Annotations: &mcp.ToolAnnotations{Title: "Set dashboard notes", DestructiveHint: &f, IdempotentHint: true, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in NotesArgs) (NotesOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return NotesOut{}, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return NotesOut{}, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			if strings.TrimSpace(in.Markdown) == "" {
				return NotesOut{}, errors.New("markdown is empty")
			}
			if in.Place != "" && in.Place != "top" && in.Place != "bottom" {
				return NotesOut{}, fmt.Errorf("place is top or bottom, not %q", in.Place)
			}
			pi, err := findPage(*d, in.Page)
			if err != nil {
				return NotesOut{}, err
			}
			id := d.ID
			var saved bi.Dashboard
			_, err = c.s.m.Store.BI().Update(p, func(w *bi.Workspace) error {
				d := w.Dashboard(id)
				if d == nil {
					return errors.New("the dashboard was deleted")
				}
				if err := setNotes(&d.Pages[pi], "mcp:"+c.ep.Name, in.Markdown, in.Place); err != nil {
					var nr noRoom
					if agentMade(d.CreatedBy) && errors.As(err, &nr) {
						return nr.mine()
					}
					return err
				}
				if agentMade(d.CreatedBy) {
					d.Pages[pi].FitText()
				}
				var err error
				saved, err = w.PutDashboard(*d)
				return err
			})
			if err != nil {
				return NotesOut{}, err
			}
			c.act.SQL = p + " › " + saved.Name
			c.biChanged(p)
			out := NotesOut{Dashboard: saved.Name, Page: pi + 1}
			for _, it := range saved.Pages[pi].Items {
				if it.Kind == bi.ItemText && it.CreatedBy == "mcp:"+c.ep.Name {
					out.Y, out.H, out.Updated = it.Y, it.H, it.Updated
				}
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("refresh_dashboard", "Refresh dashboard",
		"Reads every chart on a dashboard (or one page) as the user sees it now, with the page's own text and, separately, the notes agents wrote. Values are unformatted numbers."),
		func(ctx context.Context, c *call, in RefreshArgs) (RefreshOut, error) {
			_, w, err := c.workspace(in.Workspace)
			if err != nil {
				return RefreshOut{}, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return RefreshOut{}, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			// Parameter values are checked once, not per chart.
			values, err := w.Values(in.Parameters)
			if err != nil {
				return RefreshOut{}, err
			}
			out := RefreshOut{Dashboard: d.Name, AsOf: time.Now().UTC().Format(time.RFC3339), Pages: []PageResult{}}
			only := -1
			if in.Page != "" {
				if only, err = findPage(*d, in.Page); err != nil {
					return RefreshOut{}, err
				}
			}
			rows := 0
			started := time.Now()
			// A chart on several pages is read once, so every copy shows the same rows.
			read := map[string]ChartResult{}
			for i, pg := range d.Pages {
				if only >= 0 && i != only {
					continue
				}
				pr := PageResult{Page: i + 1, Name: pg.Name, Charts: []ChartResult{}}
				for _, it := range pg.Items {
					if it.Kind == bi.ItemText {
						if it.CreatedBy != "" {
							place := "top"
							if it.Y > 0 && it.Y+it.H >= pg.Bottom() {
								place = "bottom"
							}
							need := bi.TextRowsAt(it.Text, it.W, true)
							pr.Notes = append(pr.Notes, NoteOut{By: strings.TrimPrefix(it.CreatedBy, "mcp:"), Updated: it.Updated, Markdown: it.Text,
								Y: it.Y, H: it.H, Place: place, Needed: need, Fits: need <= it.H})
						} else {
							need := bi.TextRowsAt(it.Text, it.W, false)
							pr.Text = append(pr.Text, values.Expand(it.Text))
							pr.TextFit = append(pr.TextFit, TextFit{Y: it.Y, H: it.H, Needed: need, Fits: need <= it.H})
						}
						continue
					}
					ch := w.Chart(it.Chart)
					if ch == nil {
						continue
					}
					if r, ok := read[ch.ID]; ok {
						pr.Charts = append(pr.Charts, r)
						continue
					}
					title := ch.Props.Title
					if title == "" {
						title = ch.Name
					}
					r := ChartResult{Chart: ch.Name, ID: ch.ID, Type: ch.Type, Title: values.Expand(title)}
					src := w.Source(ch.DataSource)
					switch {
					case src == nil:
						r.Error = "the chart's data source is no longer in the workspace"
					default:
						if err := c.canRead(ctx, *src); err != nil {
							r.Error = err.Error()
							break
						}
						// Each source is read once for the whole dashboard, now.
						data, err := c.s.m.BIChartData(ctx, w, *ch, in.Parameters, core.BIRead{Since: started})
						if err != nil {
							r.Error = err.Error()
							break
						}
						r.Columns, r.Rows, r.Complete, r.Truncated, r.Mode, r.AsOf, r.Previous = data.Columns, data.Rows, data.Complete, data.Truncated, data.Mode, data.RanAt, data.Previous
						rows += len(data.Rows)
					}
					read[ch.ID] = r
					pr.Charts = append(pr.Charts, r)
				}
				out.Pages = append(out.Pages, pr)
			}
			c.act.SQL = d.Name
			c.act.Rows = int64(rows)
			return out, nil
		})

	addRich(s, srv, ep, &mcp.Tool{Name: "export_dashboard", Description: "Draws a dashboard's pages as Chloe shows them and saves them in the user's Chloe Reports folder: PNG images " +
		"(returned to you too, so you can check how a page looks: text that's cut off, crowded charts, empty space) or a PDF. Needs Chloe's window open.",
		Annotations: &mcp.ToolAnnotations{Title: "Export dashboard", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in ExportArgs) (ExportOut, []mcp.Content, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return ExportOut{}, nil, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return ExportOut{}, nil, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			format := strings.ToLower(in.Format)
			if format == "" {
				format = "png"
			}
			if format != "png" && format != "pdf" {
				return ExportOut{}, nil, fmt.Errorf("format is png or pdf, not %q", in.Format)
			}
			var pages []int
			if in.Page != "" {
				pi, err := findPage(*d, in.Page)
				if err != nil {
					return ExportOut{}, nil, err
				}
				pages = []int{pi}
			} else {
				for i := range d.Pages {
					pages = append(pages, i)
				}
			}
			// Drawing shows the data, so every chart's source must be readable.
			for _, pi := range pages {
				for _, it := range d.Pages[pi].Items {
					if ch := w.Chart(it.Chart); it.Kind == bi.ItemChart && ch != nil {
						if src := w.Source(ch.DataSource); src != nil {
							if err := c.canRead(ctx, *src); err != nil {
								return ExportOut{}, nil, fmt.Errorf("chart %q: %w", ch.Name, err)
							}
						}
					}
				}
			}
			if c.s.events == nil {
				return ExportOut{}, nil, errors.New("Chloe's window isn't available")
			}
			// Agents get pages at their layout size; PDFs are drawn at twice that.
			scale := 1.0
			if format == "pdf" {
				scale = 2
			}
			if _, err := w.Values(in.Parameters); err != nil {
				return ExportOut{}, nil, err
			}
			pngs, err := c.s.events.Render(ctx, p, d.ID, pages, scale, in.Parameters)
			if err != nil {
				return ExportOut{}, nil, err
			}
			dir := filepath.Join(bi.ReportsDir(), fileSafe(w.Name))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return ExportOut{}, nil, err
			}
			stamp := time.Now().Format("2006-01-02 150405")
			out := ExportOut{Files: []string{}, Format: format}
			var content []mcp.Content
			for i, pi := range pages {
				out.Pages = append(out.Pages, pi+1)
				if format == "png" {
					f := filepath.Join(dir, fmt.Sprintf("%s p%d %s.png", fileSafe(d.Name), pi+1, stamp))
					if err := os.WriteFile(f, pngs[i], 0o644); err != nil {
						return ExportOut{}, nil, err
					}
					out.Files = append(out.Files, f)
					content = append(content, &mcp.ImageContent{Data: pngs[i], MIMEType: "image/png"})
				}
			}
			if format == "pdf" {
				data, err := bi.PDF(pngs, bi.PageWidth)
				if err != nil {
					return ExportOut{}, nil, err
				}
				f := filepath.Join(dir, fmt.Sprintf("%s %s.pdf", fileSafe(d.Name), stamp))
				if err := os.WriteFile(f, data, 0o644); err != nil {
					return ExportOut{}, nil, err
				}
				out.Files = append(out.Files, f)
			}
			c.act.SQL = strings.Join(out.Files, ", ")
			return out, content, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "open_dashboard", Description: "Opens a dashboard in Chloe for the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Open dashboard", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in DashboardArgs) (DashboardOpened, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DashboardOpened{}, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return DashboardOpened{}, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			if c.s.events == nil {
				return DashboardOpened{}, errors.New("Chloe's window isn't available")
			}
			c.s.events.OpenBI(p, d.ID)
			return DashboardOpened{Dashboard: d.Name, ID: d.ID, Pages: len(d.Pages)}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "delete_dashboard", Description: "Deletes a dashboard created over MCP (not its charts). The user's dashboards can't be deleted.",
		Annotations: &mcp.ToolAnnotations{Title: "Delete dashboard", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in DashboardArgs) (DeletedOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DeletedOut{}, err
			}
			d := findDashboard(&w, in.Dashboard)
			if d == nil {
				return DeletedOut{}, fmt.Errorf("no dashboard %q in the workspace (see get_workspace)", in.Dashboard)
			}
			if !agentMade(d.CreatedBy) {
				return DeletedOut{}, errors.New("this is one of the user's dashboards; agents can only delete dashboards they created")
			}
			id, name := d.ID, d.Name
			if _, err := c.s.m.Store.BI().Update(p, func(w *bi.Workspace) error { return w.Delete(id) }); err != nil {
				return DeletedOut{}, err
			}
			c.act.SQL = p + " › " + name
			c.biChanged(p)
			return DeletedOut{Deleted: name}, nil
		})
}

// rowHeights lists the heights of rows laid out by LayoutRows, top to bottom.
func rowHeights(items []bi.Item) []int {
	byY := map[int]int{}
	var ys []int
	for _, it := range items {
		if _, ok := byY[it.Y]; !ok {
			ys = append(ys, it.Y)
		}
		byY[it.Y] = max(byY[it.Y], it.H)
	}
	slices.Sort(ys)
	out := make([]int, len(ys))
	for i, y := range ys {
		out[i] = byY[y]
	}
	return out
}

// fileSafe makes a name usable as a file name.
func fileSafe(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) || r < 32 {
			return '-'
		}
		return r
	}, strings.TrimSpace(s))
	if s == "" {
		return "dashboard"
	}
	return s
}

func findDashboard(w *bi.Workspace, ref string) *bi.Dashboard {
	if d := w.Dashboard(ref); d != nil {
		return d
	}
	for i := range w.Dashboards {
		if strings.EqualFold(w.Dashboards[i].Name, ref) {
			return &w.Dashboards[i]
		}
	}
	return nil
}

// findPage finds a page by number (1 = first) or name; "" is the first page.
func findPage(d bi.Dashboard, ref string) (int, error) {
	if ref == "" {
		return 0, nil
	}
	if n, err := strconv.Atoi(ref); err == nil {
		if n < 1 || n > len(d.Pages) {
			return 0, fmt.Errorf("the dashboard has %d page(s)", len(d.Pages))
		}
		return n - 1, nil
	}
	for i, p := range d.Pages {
		if strings.EqualFold(p.Name, ref) {
			return i, nil
		}
	}
	return 0, fmt.Errorf("no page %q in the dashboard", ref)
}

// layoutPages turns the agent's rows into placed tiles. Charts must be ones the
// endpoint can read.
func (c *call) layoutPages(w *bi.Workspace, in []PageArg) ([]bi.Page, error) {
	if len(in) == 0 {
		return nil, errors.New("a dashboard needs at least one page")
	}
	chartID := func(ref string) (string, error) {
		ch := findChart(w, ref)
		if ch == nil {
			var names []string
			for _, ch := range w.Charts {
				names = append(names, ch.Name)
			}
			return "", fmt.Errorf("no chart %q in the workspace (charts: %s)", ref, strings.Join(names, ", "))
		}
		if src := w.Source(ch.DataSource); src != nil && !c.readable(*src) {
			return "", fmt.Errorf("chart %q reads a connection that isn't shared with this endpoint", ch.Name)
		}
		return ch.ID, nil
	}
	var pages []bi.Page
	for i, pa := range in {
		size := pa.Size
		if size == "" {
			size = bi.SizeReport
		}
		rows := make([][]bi.RowTile, len(pa.Rows))
		heights := make([]string, len(pa.Rows))
		for r, ra := range pa.Rows {
			heights[r] = ra.Height
			for _, ta := range ra.Tiles {
				rows[r] = append(rows[r], bi.RowTile{Chart: ta.Chart, Text: ta.Text, Span: ta.Span})
			}
			if len(ra.Tiles) == 0 {
				return nil, fmt.Errorf("page %d row %d has no tiles", i+1, r+1)
			}
		}
		items, bottom, err := bi.LayoutRows(rows, heights, 0, chartID)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i+1, err)
		}
		if size == bi.SizeSlide && bottom > bi.PageRows {
			var parts []string
			for r, h := range rowHeights(items) {
				parts = append(parts, fmt.Sprintf("row %d %d", r+1, h))
			}
			return nil, fmt.Errorf("page %d: its rows take %d grid rows (%s) but a 16:9 slide has %d; use smaller heights (S 6, M 10, L 16), fewer rows, or size report", i+1, bottom, strings.Join(parts, ", "), bi.PageRows)
		}
		pages = append(pages, bi.Page{ID: uuid.NewString(), Name: pa.Name, Size: size, Items: items})
	}
	return pages, nil
}

func (c *call) putDashboard(p string, d bi.Dashboard) (bi.Dashboard, error) {
	var saved bi.Dashboard
	_, err := c.s.m.Store.BI().Update(p, func(w *bi.Workspace) error {
		var err error
		saved, err = w.PutDashboard(d)
		return err
	})
	if err != nil {
		return saved, err
	}
	c.act.SQL = p + " › " + saved.Name
	c.biChanged(p)
	return saved, nil
}

// setNotes writes an endpoint's notes tile on a page: replacing its text in
// place, or adding it at the top of a report page (pushing the rest down) or at
// the bottom (place), or in the first free rows of a slide.
func setNotes(pg *bi.Page, by, md, place string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	h := bi.TextRowsAt(md, bi.PageCols, true)
	for i := range pg.Items {
		it := &pg.Items[i]
		if it.Kind != bi.ItemText || it.CreatedBy != by {
			continue
		}
		if moved := place == "top" && it.Y != 0 || place == "bottom" && it.Y+it.H != pg.Bottom(); moved {
			// Lift it out, close the gap, and add it again where asked.
			y, gone := it.Y, it.H
			pg.Items = append(pg.Items[:i], pg.Items[i+1:]...)
			for j := range pg.Items {
				if pg.Items[j].Y >= y+gone {
					pg.Items[j].Y -= gone
				}
			}
			return setNotes(pg, by, md, place)
		}
		grow := 0
		if h > it.H {
			grow = h - it.H
			// On a slide notes grow only into free rows at the bottom; they're never cut off.
			if pg.Size == bi.SizeSlide && (it.Y+it.H != pg.Bottom() || pg.Bottom()+grow > bi.PageRows) {
				return noRoom{need: h, free: bi.PageRows - pg.Bottom() + it.H}
			}
		}
		it.Text, it.Updated = md, now
		if grow > 0 {
			bottom := it.Y + it.H
			it.H = h
			for j := range pg.Items {
				if j != i && pg.Items[j].Y >= bottom {
					pg.Items[j].Y += grow
				}
			}
		}
		return nil
	}
	note := bi.Item{ID: uuid.NewString(), Kind: bi.ItemText, Text: md, X: 0, W: bi.PageCols, H: h, CreatedBy: by, Updated: now}
	// A slide has only the rows below its tiles to give.
	if pg.Size == bi.SizeSlide && pg.Bottom()+h > bi.PageRows {
		return noRoom{need: h, free: bi.PageRows - pg.Bottom()}
	}
	if place == "bottom" {
		note.Y = pg.Bottom()
		pg.Items = append(pg.Items, note)
		return nil
	}
	for j := range pg.Items {
		pg.Items[j].Y += h
	}
	pg.Items = append([]bi.Item{note}, pg.Items...)
	return nil
}

// noRoom is notes that don't fit the free rows of a 16:9 slide.
type noRoom struct{ need, free int }

func (e noRoom) Error() string {
	return fmt.Sprintf("these notes need %d grid rows but the 16:9 slide has %d free; shorten them, put them on a report page, or ask the user to make room", e.need, max(0, e.free))
}

// mine says how an agent can make room on its own dashboard.
func (e noRoom) mine() error {
	return fmt.Errorf("these notes need %d grid rows but the 16:9 slide has %d free; it's your dashboard, so shorten the notes, make room with edit_dashboard (smaller rows), or switch the page to report", e.need, max(0, e.free))
}
