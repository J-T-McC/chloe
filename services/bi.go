package services

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/bi"
	"chloe/internal/core"
)

// EventBIChanged is emitted with a workspace's path whenever it changes (in the
// designer or over MCP), so open lists and tabs reload.
const EventBIChanged = "bi:changed"

func init() { application.RegisterEvent[string](EventBIChanged) }

func biChanged(p string) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventBIChanged, p)
	}
}

// BIService backs BI workspaces: the library, data sources and charts.
type BIService struct{ M *core.Manager }

// Catalog is what the designers offer: chart types and their shelves,
// aggregates, date buckets and filter operators.
type Catalog struct {
	Types           map[string]bi.ChartType `json:"types"`
	ValueAggregates map[string][]string     `json:"valueAggregates"`
	DateBuckets     []string                `json:"dateBuckets"`
	FilterOps       map[string]int          `json:"filterOps"`
}

func (s *BIService) Catalog() Catalog {
	return Catalog{Types: bi.ChartTypes, ValueAggregates: bi.ValueAggregates, DateBuckets: bi.DateBuckets, FilterOps: bi.FilterOps}
}

func (s *BIService) lib() bi.Library { return s.M.Store.BI() }

// List describes every workspace in Chloe's library.
func (s *BIService) List() []bi.Entry { return s.lib().List() }

// Create adds an empty workspace to the library and returns its path.
func (s *BIService) Create(name string) (string, error) {
	p, err := s.lib().Create(bi.New(strings.TrimSpace(name)))
	if err == nil {
		biChanged(p)
	}
	return p, err
}

// Open reads a library workspace.
func (s *BIService) Open(path string) (bi.Workspace, error) {
	if !s.lib().Contains(path) {
		return bi.Workspace{}, errors.New("not a workspace in Chloe's library")
	}
	return bi.Read(path)
}

func (s *BIService) update(path string, fn func(*bi.Workspace) error) (bi.Workspace, error) {
	w, err := s.lib().Update(path, fn)
	if err == nil {
		biChanged(path)
	}
	return w, err
}

func (s *BIService) Rename(path, name string) (string, error) {
	p, err := s.lib().Rename(path, name)
	if err == nil {
		biChanged(path)
		biChanged(p)
	}
	return p, err
}

func (s *BIService) Duplicate(path string) (string, error) {
	p, err := s.lib().Duplicate(path)
	if err == nil {
		biChanged(p)
	}
	return p, err
}

func (s *BIService) Delete(path string) error {
	err := s.lib().Delete(path)
	if err == nil {
		biChanged(path)
	}
	return err
}

func (s *BIService) Reveal(path string) error {
	return application.Get().Env.OpenFileManager(path, true)
}

// Import asks for a workspace file kept elsewhere and copies it into the library.
// A cancelled dialog returns "".
func (s *BIService) Import() (string, error) {
	p, err := application.Get().Dialog.OpenFile().SetTitle("Open External Workspace").AddFilter("Chloe BI Workspace", "*"+bi.Ext).PromptForSingleSelection()
	if err != nil || p == "" {
		return "", err
	}
	np, err := s.lib().Import(p)
	if err == nil {
		biChanged(np)
	}
	return np, err
}

// SaveExternal writes a copy of a workspace (definitions only) to a file the user
// picks. Returns the path written, or "" when cancelled.
func (s *BIService) SaveExternal(path string) (string, error) {
	w, err := s.Open(path)
	if err != nil {
		return "", err
	}
	p, err := application.Get().Dialog.SaveFile().SetFilename(w.Name+bi.Ext).CanCreateDirectories(true).AddFilter("Chloe BI Workspace", "*"+bi.Ext).PromptForSingleSelection()
	if err != nil || p == "" {
		return "", err
	}
	if filepath.Ext(p) == "" {
		p += bi.Ext
	}
	return p, bi.Write(p, w)
}

// SetTheme sets the charts' theme of a workspace.
func (s *BIService) SetTheme(path, theme string) error {
	_, err := s.update(path, func(w *bi.Workspace) error { w.Theme = theme; return nil })
	return err
}

// Fields reads a draft data source's columns.
func (s *BIService) Fields(ctx context.Context, src bi.DataSource) ([]bi.Field, error) {
	return s.M.BIFields(ctx, src)
}

// Preview reads the first rows of a draft data source.
func (s *BIService) Preview(ctx context.Context, path string, src bi.DataSource, limit int) (core.BIPreview, error) {
	w, err := s.Open(path)
	if err != nil {
		return core.BIPreview{}, err
	}
	return s.M.BIPreview(ctx, w, src, min(max(limit, 1), 1000))
}

// SaveSource stores a data source (new when it has no ID) after reading its
// fields; an archive source is read into a snapshot when it needs one.
func (s *BIService) SaveSource(ctx context.Context, path string, src bi.DataSource) (bi.DataSource, error) {
	saved, err := s.M.BISaveSource(ctx, path, src)
	if err == nil {
		biChanged(path)
	}
	return saved, err
}

// RefreshSource reads an archive data source into a new snapshot.
func (s *BIService) RefreshSource(ctx context.Context, path, id string) (bi.DataSource, error) {
	src, err := s.M.BIRefreshSource(ctx, path, id)
	if err == nil {
		biChanged(path)
	}
	return src, err
}

// SaveChart stores a chart (new when it has no ID).
func (s *BIService) SaveChart(path string, c bi.Chart) (bi.Chart, error) {
	var saved bi.Chart
	_, err := s.update(path, func(w *bi.Workspace) error {
		var err error
		saved, err = w.PutChart(c)
		return err
	})
	return saved, err
}

// SaveDashboard stores a dashboard (new when it has no ID).
func (s *BIService) SaveDashboard(path string, d bi.Dashboard) (bi.Dashboard, error) {
	var saved bi.Dashboard
	_, err := s.update(path, func(w *bi.Workspace) error {
		var err error
		saved, err = w.PutDashboard(d)
		return err
	})
	return saved, err
}

// DeleteObject removes a data source, chart or dashboard from a workspace.
func (s *BIService) DeleteObject(path, id string) error {
	var archived string
	w, err := s.update(path, func(w *bi.Workspace) error {
		if src := w.Source(id); src != nil && src.Mode == bi.ModeArchive {
			archived = id
		}
		return w.Delete(id)
	})
	if err == nil && archived != "" {
		_ = os.Remove(s.lib().ArchivePath(w.ID, archived))
	}
	return err
}

// ChartData reads what a (draft) chart of a workspace draws.
func (s *BIService) ChartData(ctx context.Context, path string, c bi.Chart) (bi.Data, error) {
	w, err := s.Open(path)
	if err != nil {
		return bi.Data{}, err
	}
	return s.M.BIChartData(ctx, w, c)
}

// NewChart is a data source and an empty chart made from a table or query
// (Create Chart in the data grid).
type NewChart struct {
	Path     string `json:"path"`
	SourceID string `json:"sourceId"`
	ChartID  string `json:"chartId"`
}

// CreateChart adds a data source and an empty chart on it to a workspace, or to
// a new workspace named newWorkspace when path is "".
func (s *BIService) CreateChart(ctx context.Context, path, newWorkspace string, src bi.DataSource) (NewChart, error) {
	if path == "" {
		var err error
		if path, err = s.Create(newWorkspace); err != nil {
			return NewChart{}, err
		}
	}
	w, err := s.Open(path)
	if err != nil {
		return NewChart{}, err
	}
	src.Name = freeName(sourceNames(&w), src.Name)
	saved, err := s.SaveSource(ctx, path, src)
	if err != nil {
		return NewChart{Path: path}, err
	}
	w, _ = s.Open(path)
	c, err := s.SaveChart(path, bi.Chart{Meta: bi.Meta{Name: freeName(chartNames(&w), "Chart of "+saved.Name)}, DataSource: saved.ID, Type: "bar"})
	return NewChart{Path: path, SourceID: saved.ID, ChartID: c.ID}, err
}

// freeName numbers name until no object of the same kind has it.
func freeName(names []string, name string) string {
	taken := func(n string) bool {
		for _, x := range names {
			if strings.EqualFold(x, n) {
				return true
			}
		}
		return false
	}
	out := name
	for i := 2; taken(out); i++ {
		out = name + " " + strconv.Itoa(i)
	}
	return out
}

func sourceNames(w *bi.Workspace) (out []string) {
	for _, d := range w.DataSources {
		out = append(out, d.Name)
	}
	return out
}

func chartNames(w *bi.Workspace) (out []string) {
	for _, c := range w.Charts {
		out = append(out, c.Name)
	}
	return out
}
