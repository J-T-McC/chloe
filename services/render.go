package services

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/bi"
	"chloe/internal/safefile"
)

// Dashboards are drawn by Chloe's window, so rendering one (for export, or for
// an agent to look at) asks the window to draw its pages off screen and send
// them back as PNGs.

const EventBIRender = "bi:render"

// RenderRequest asks the window to draw pages (0-based) of a dashboard at a
// pixel ratio.
type RenderRequest struct {
	ID        string  `json:"id"`
	Path      string  `json:"path"`
	Dashboard string  `json:"dashboard"`
	Pages     []int   `json:"pages"`
	Scale     float64 `json:"scale"`
	// Params are parameter values to draw with (not saved).
	Params map[string]string `json:"params,omitempty"`
	// Since asks for data read at or after this time (Unix ms): an agent's
	// export shows the data as it is now, each source read once for it.
	Since int64 `json:"since,omitempty"`
	// Format is "html" for an HTML report of the pages; otherwise PNGs.
	Format string `json:"format,omitempty"`
}

func init() { application.RegisterEvent[RenderRequest](EventBIRender) }

// RenderTimeout bounds how long a render waits for the window.
var RenderTimeout = 60 * time.Second

type renderResult struct {
	pages [][]byte
	err   error
}

type pendingRender struct {
	ch   chan renderResult
	html bool
}

var renders = struct {
	sync.Mutex
	pending map[string]pendingRender
}{pending: map[string]pendingRender{}}

// Render has the window draw dashboard pages and returns them as PNGs.
func (MCPEvents) Render(ctx context.Context, path, dashboard string, pages []int, scale float64, params map[string]string) ([][]byte, error) {
	return render(ctx, RenderRequest{Path: path, Dashboard: dashboard, Pages: pages, Scale: scale, Params: params})
}

// RenderHTML has the window build an HTML report of dashboard pages.
func (MCPEvents) RenderHTML(ctx context.Context, path, dashboard string, pages []int, params map[string]string) ([]byte, error) {
	out, err := render(ctx, RenderRequest{Path: path, Dashboard: dashboard, Pages: pages, Params: params, Format: "html"})
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errors.New("the window sent no report")
	}
	return out[0], nil
}

func render(ctx context.Context, req RenderRequest) ([][]byte, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("Chloe's window isn't available to draw the dashboard")
	}
	req.ID, req.Since = uuid.NewString(), time.Now().UnixMilli()
	ch := make(chan renderResult, 1)
	renders.Lock()
	renders.pending[req.ID] = pendingRender{ch: ch, html: req.Format == "html"}
	renders.Unlock()
	defer func() {
		renders.Lock()
		delete(renders.pending, req.ID)
		renders.Unlock()
	}()
	app.Event.Emit(EventBIRender, req)
	ctx, cancel := context.WithTimeout(ctx, RenderTimeout)
	defer cancel()
	select {
	case r := <-ch:
		return r.pages, r.err
	case <-ctx.Done():
		return nil, errors.New("Chloe's window didn't draw the dashboard in time; is Chloe open?")
	}
}

// Rendered is the window's answer to a render request: PNG data URLs (or
// base64), one per page, or the HTML report; or the error it hit.
func (s *BIService) Rendered(id string, pages []string, errText string) {
	renders.Lock()
	pr, ok := renders.pending[id]
	renders.Unlock()
	if !ok {
		return
	}
	ch := pr.ch
	if errText != "" {
		ch <- renderResult{err: errors.New(errText)}
		return
	}
	if pr.html {
		out := make([][]byte, len(pages))
		for i, p := range pages {
			out[i] = []byte(p)
		}
		ch <- renderResult{pages: out}
		return
	}
	out := make([][]byte, len(pages))
	for i, p := range pages {
		data, err := decodePNG(p)
		if err != nil {
			ch <- renderResult{err: fmt.Errorf("page %d: %w", i+1, err)}
			return
		}
		out[i] = data
	}
	ch <- renderResult{pages: out}
}

func decodePNG(s string) ([]byte, error) {
	if i := strings.Index(s, ","); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+1:]
	}
	return base64.StdEncoding.DecodeString(s)
}

// ChooseExportFile asks where to save an export before anything is drawn, so a
// cancelled dialog costs nothing. Returns the path with its extension, or ""
// when cancelled.
func (s *BIService) ChooseExportFile(name, format string) (string, error) {
	ext, err := exportExt(format)
	if err != nil {
		return "", err
	}
	// The browser build has no native dialog; e2e saves into a folder instead.
	if dir := os.Getenv("CHLOE_EXPORT_DIR"); dir != "" {
		return filepath.Join(dir, safeName(name)+ext), os.MkdirAll(dir, 0o755)
	}
	p, err := application.Get().Dialog.SaveFile().SetFilename(safeName(name) + ext).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || p == "" {
		return "", err
	}
	if filepath.Ext(p) == "" {
		p += ext
	}
	return p, nil
}

// WriteExport writes rendered pages to path: one PNG, or a PDF of every page.
func (s *BIService) WriteExport(path, format string, pages []string) error {
	data, _, err := exportData(format, pages)
	if err != nil {
		return err
	}
	return safefile.Write(path, data)
}

func exportExt(format string) (string, error) {
	switch format {
	case "png", "pdf", "html":
		return "." + format, nil
	}
	return "", fmt.Errorf("export is png, pdf or html, not %q", format)
}

func exportData(format string, pages []string) ([]byte, string, error) {
	if format == "html" {
		if len(pages) != 1 {
			return nil, "", errors.New("an HTML report is one file")
		}
		return []byte(pages[0]), ".html", nil
	}
	pngs := make([][]byte, len(pages))
	for i, p := range pages {
		var err error
		if pngs[i], err = decodePNG(p); err != nil {
			return nil, "", err
		}
	}
	switch format {
	case "png":
		if len(pngs) != 1 {
			return nil, "", errors.New("a PNG holds one page")
		}
		return pngs[0], ".png", nil
	case "pdf":
		data, err := bi.PDF(pngs, bi.PageWidth)
		return data, ".pdf", err
	}
	return nil, "", fmt.Errorf("export is png, pdf or html, not %q", format)
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, s)
}
