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
}

func init() { application.RegisterEvent[RenderRequest](EventBIRender) }

// RenderTimeout bounds how long a render waits for the window.
var RenderTimeout = 60 * time.Second

type renderResult struct {
	pages [][]byte
	err   error
}

var renders = struct {
	sync.Mutex
	pending map[string]chan renderResult
}{pending: map[string]chan renderResult{}}

// Render has the window draw dashboard pages and returns them as PNGs.
func (MCPEvents) Render(ctx context.Context, path, dashboard string, pages []int, scale float64) ([][]byte, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("Chloe's window isn't available to draw the dashboard")
	}
	req := RenderRequest{ID: uuid.NewString(), Path: path, Dashboard: dashboard, Pages: pages, Scale: scale}
	ch := make(chan renderResult, 1)
	renders.Lock()
	renders.pending[req.ID] = ch
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
// base64), one per page, or the error it hit.
func (s *BIService) Rendered(id string, pages []string, errText string) {
	renders.Lock()
	ch := renders.pending[id]
	renders.Unlock()
	if ch == nil {
		return
	}
	if errText != "" {
		ch <- renderResult{err: errors.New(errText)}
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

// SaveExport writes rendered pages to a file the user picks: one PNG, or a PDF
// of every page. Returns the path, or "" when cancelled.
func (s *BIService) SaveExport(name, format string, pages []string) (string, error) {
	data, ext, err := exportData(format, pages)
	if err != nil {
		return "", err
	}
	p, err := application.Get().Dialog.SaveFile().SetFilename(safeName(name) + ext).CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil || p == "" {
		return "", err
	}
	if filepath.Ext(p) == "" {
		p += ext
	}
	return p, os.WriteFile(p, data, 0o644)
}

func exportData(format string, pages []string) ([]byte, string, error) {
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
	return nil, "", fmt.Errorf("export is png or pdf, not %q", format)
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '-'
		}
		return r
	}, s)
}
