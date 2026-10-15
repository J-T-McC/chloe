package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/bi"
)

type TileArg struct {
	Chart string `json:"chart,omitempty" jsonschema:"a chart name or id in the workspace"`
	Text  string `json:"text,omitempty" jsonschema:"Markdown (headings, lists, bold, links, tables) instead of a chart"`
	Span  int    `json:"span,omitempty" jsonschema:"columns of 12 the tile spans; tiles without one share what the row has left"`
}

type RowArg struct {
	Height string    `json:"height,omitempty" jsonschema:"for rows with a chart: S (3 grid rows), M (5, default), L (8) or XL (11, which fills a 16:9 slide on its own). A 16:9 slide holds 12 grid rows in all. Rows of only text are always as tall as their text"`
	Tiles  []TileArg `json:"tiles"`
}

type PageArg struct {
	Name string   `json:"name,omitempty"`
	Size string   `json:"size,omitempty" jsonschema:"report (scrolls; the default) or 16:9 (a 12-row slide)"`
	Rows []RowArg `json:"rows"`
}

type CreateDashboardArgs struct {
	WorkspaceArgs
	Name    string    `json:"name"`
	Pages   []PageArg `json:"pages" jsonschema:"each page is rows of tiles across 12 columns; Chloe places them"`
	Refresh int       `json:"refresh,omitempty" jsonschema:"reload the charts every n minutes while the dashboard is open in Chloe (0 = off)"`
}

type EditDashboardArgs struct {
	WorkspaceArgs
	Dashboard string    `json:"dashboard" jsonschema:"a dashboard name or id in the workspace"`
	Rename    string    `json:"rename,omitempty"`
	Pages     []PageArg `json:"pages,omitempty" jsonschema:"replaces every page"`
	Refresh   *int      `json:"refresh,omitempty"`
}

type DashboardArgs struct {
	WorkspaceArgs
	Dashboard string `json:"dashboard" jsonschema:"a dashboard name or id in the workspace"`
}

type RefreshArgs struct {
	DashboardArgs
	Page string `json:"page,omitempty" jsonschema:"a page number (1 = first) or name; default every page"`
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

type DashboardOut struct {
	bi.Dashboard
	Editable bool `json:"editable" jsonschema:"created over MCP, so edit_dashboard may change it"`
}

type ChartResult struct {
	Chart     string      `json:"chart"`
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Columns   []bi.Column `json:"columns,omitempty"`
	Rows      [][]any     `json:"rows,omitempty"`
	Complete  bool        `json:"complete"`
	Truncated bool        `json:"truncated,omitempty"`
	Mode      string      `json:"mode,omitempty"`
	AsOf      string      `json:"as_of,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type NoteOut struct {
	By       string `json:"by" jsonschema:"the endpoint that wrote them"`
	Updated  string `json:"updated,omitempty"`
	Markdown string `json:"markdown"`
}

type PageResult struct {
	Page   int           `json:"page"`
	Name   string        `json:"name,omitempty"`
	Charts []ChartResult `json:"charts"`
	Text   []string      `json:"text,omitempty" jsonschema:"the page's own text tiles (part of its layout)"`
	Notes  []NoteOut     `json:"notes,omitempty" jsonschema:"notes agents wrote with set_notes"`
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
			d := bi.Dashboard{Meta: bi.Meta{Name: in.Name, CreatedBy: "mcp:" + c.ep.Name}, Pages: pages, Refresh: in.Refresh}
			saved, err := c.putDashboard(p, d)
			return DashboardOut{Dashboard: saved, Editable: true}, err
		})

	add(s, srv, ep, &mcp.Tool{Name: "edit_dashboard", Description: "Changes a dashboard created over MCP: replaces its pages, renames it, or sets its refresh. " +
		"Notes written with set_notes are kept on the page with the same number. " +
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
			if in.Refresh != nil {
				edited.Refresh = *in.Refresh
			}
			if in.Pages != nil {
				if edited.Pages, err = c.layoutPages(&w, in.Pages); err != nil {
					return DashboardOut{}, err
				}
				// Notes written with set_notes stay on the page with the same number,
				// at its top or bottom as they were.
				for i := range edited.Pages {
					if i >= len(d.Pages) {
						break
					}
					for _, it := range d.Pages[i].Items {
						if it.Kind == bi.ItemText && it.CreatedBy != "" {
							place := "top"
							if it.Y > 0 && it.Y+it.H >= d.Pages[i].Bottom() {
								place = "bottom"
							}
							if err := setNotes(&edited.Pages[i], it.CreatedBy, it.Text, place); err != nil {
								return DashboardOut{}, fmt.Errorf("page %d: keeping the notes by %s: %w", i+1, strings.TrimPrefix(it.CreatedBy, "mcp:"), err)
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
					return err
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
			out := RefreshOut{Dashboard: d.Name, AsOf: time.Now().UTC().Format(time.RFC3339), Pages: []PageResult{}}
			only := -1
			if in.Page != "" {
				if only, err = findPage(*d, in.Page); err != nil {
					return RefreshOut{}, err
				}
			}
			rows := 0
			for i, pg := range d.Pages {
				if only >= 0 && i != only {
					continue
				}
				pr := PageResult{Page: i + 1, Name: pg.Name, Charts: []ChartResult{}}
				for _, it := range pg.Items {
					if it.Kind == bi.ItemText {
						if it.CreatedBy != "" {
							pr.Notes = append(pr.Notes, NoteOut{By: strings.TrimPrefix(it.CreatedBy, "mcp:"), Updated: it.Updated, Markdown: it.Text})
						} else {
							pr.Text = append(pr.Text, it.Text)
						}
						continue
					}
					ch := w.Chart(it.Chart)
					if ch == nil {
						continue
					}
					r := ChartResult{Chart: ch.Name, ID: ch.ID, Type: ch.Type}
					src := w.Source(ch.DataSource)
					switch {
					case src == nil:
						r.Error = "the chart's data source is no longer in the workspace"
					default:
						if err := c.canRead(ctx, *src); err != nil {
							r.Error = err.Error()
							break
						}
						data, err := c.s.m.BIChartData(ctx, w, *ch)
						if err != nil {
							r.Error = err.Error()
							break
						}
						r.Columns, r.Rows, r.Complete, r.Truncated, r.Mode, r.AsOf = data.Columns, data.Rows, data.Complete, data.Truncated, data.Mode, data.RanAt
						rows += len(data.Rows)
					}
					pr.Charts = append(pr.Charts, r)
				}
				out.Pages = append(out.Pages, pr)
			}
			c.act.SQL = d.Name
			c.act.Rows = int64(rows)
			return out, nil
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
			return "", fmt.Errorf("no chart %q in the workspace", ref)
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
		items, _, err := bi.LayoutRows(rows, heights, 0, chartID)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", i+1, err)
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
		if moved := place == "top" && it.Y != 0 || place == "bottom" && it.Y+it.H != pg.Bottom(); moved && pg.Size == bi.SizeReport {
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
		if pg.Size == bi.SizeReport && h > it.H {
			grow = h - it.H
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
	if pg.Size == bi.SizeReport && place == "bottom" {
		note.Y = pg.Bottom()
		pg.Items = append(pg.Items, note)
		return nil
	}
	if pg.Size == bi.SizeReport {
		for j := range pg.Items {
			pg.Items[j].Y += h
		}
		pg.Items = append([]bi.Item{note}, pg.Items...)
		return nil
	}
	note.Y = pg.Bottom()
	note.H = min(h, bi.PageRows-note.Y)
	if note.H < 2 {
		return errors.New("this page is a full 16:9 slide with no room for notes; add them to a report page, or ask the user to make room")
	}
	pg.Items = append(pg.Items, note)
	return nil
}
