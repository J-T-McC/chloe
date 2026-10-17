package bi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"chloe/internal/safefile"
)

// Ext is the workspace file extension.
const Ext = ".chloebi"

// Library is the folder of BI workspaces Chloe manages. Each workspace's archived
// rows live in data/<workspace id>/ beside the files.
type Library struct{ Dir string }

// Entry describes one workspace in the library.
type Entry struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	DataSources int    `json:"dataSources"`
	Charts      int    `json:"charts"`
	Dashboards  int    `json:"dashboards"`
	CreatedBy   string `json:"createdBy,omitempty"`
	Modified    string `json:"modified"`
	Error       string `json:"error,omitempty"` // the file couldn't be read
}

// locks serialize read-modify-write cycles on one workspace file: the designer
// and MCP agents change objects of the same workspace.
var locks sync.Map

func lock(p string) func() {
	m, _ := locks.LoadOrStore(filepath.Clean(p), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// List reads every workspace in the library, by name.
func (l Library) List() []Entry {
	out := []Entry{}
	files, _ := os.ReadDir(l.Dir)
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != Ext {
			continue
		}
		p := filepath.Join(l.Dir, f.Name())
		e := Entry{Path: p, Name: strings.TrimSuffix(f.Name(), Ext)}
		if info, err := f.Info(); err == nil {
			e.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		if w, err := Read(p); err != nil {
			e.Error = err.Error()
		} else {
			e.Name, e.DataSources, e.Charts, e.Dashboards, e.CreatedBy = w.Name, len(w.DataSources), len(w.Charts), len(w.Dashboards), w.CreatedBy
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Contains reports whether p is a workspace file in the library.
func (l Library) Contains(p string) bool {
	p = filepath.Clean(p)
	return filepath.Dir(p) == filepath.Clean(l.Dir) && filepath.Ext(p) == Ext
}

// Find returns the library workspace with this name (case-insensitive) or path.
func (l Library) Find(nameOrPath string) (string, error) {
	if l.Contains(nameOrPath) {
		if _, err := os.Stat(nameOrPath); err == nil {
			return filepath.Clean(nameOrPath), nil
		}
	}
	var match []string
	for _, e := range l.List() {
		if strings.EqualFold(e.Name, nameOrPath) {
			match = append(match, e.Path)
		}
	}
	switch len(match) {
	case 0:
		return "", fmt.Errorf("no workspace %q", nameOrPath)
	case 1:
		return match[0], nil
	}
	return "", fmt.Errorf("several workspaces are named %q; pass the path", nameOrPath)
}

// Create saves a new workspace under a file named after it and returns the path.
func (l Library) Create(w Workspace) (string, error) {
	if strings.TrimSpace(w.Name) == "" {
		return "", errors.New("a workspace needs a name")
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", err
	}
	p := l.freePath(w.Name)
	return p, Write(p, w)
}

// Update reads a library workspace, lets fn change it, and writes it back,
// holding the workspace's lock throughout.
func (l Library) Update(p string, fn func(*Workspace) error) (Workspace, error) {
	if !l.Contains(p) {
		return Workspace{}, errors.New("not a workspace in Chloe's library")
	}
	defer lock(p)()
	w, err := Read(p)
	if err != nil {
		return w, err
	}
	if err := fn(&w); err != nil {
		return w, err
	}
	return w, Write(p, w)
}

// Rename renames a workspace, file included, and returns its new path.
func (l Library) Rename(p, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("a workspace needs a name")
	}
	w, err := l.Update(p, func(w *Workspace) error { w.Name = name; return nil })
	if err != nil {
		return "", err
	}
	if fileName(name) == fileName(strings.TrimSuffix(filepath.Base(p), Ext)) {
		return p, nil
	}
	np := l.freePath(name)
	if err := Write(np, w); err != nil {
		return "", err
	}
	return np, os.Remove(p)
}

// Duplicate copies a workspace, archived data included, under a new name.
func (l Library) Duplicate(p string) (string, error) {
	if !l.Contains(p) {
		return "", errors.New("not a workspace in Chloe's library")
	}
	w, err := Read(p)
	if err != nil {
		return "", err
	}
	old := w.ID
	w.ID, w.Name, w.CreatedBy = uuid.NewString(), w.Name+" copy", ""
	if err := copyDir(l.DataDir(old), l.DataDir(w.ID)); err != nil {
		return "", err
	}
	return l.Create(w)
}

// Delete removes a workspace and its archived data.
func (l Library) Delete(p string) error {
	if !l.Contains(p) {
		return errors.New("not a workspace in Chloe's library")
	}
	if w, err := Read(p); err == nil && w.ID != "" {
		_ = os.RemoveAll(l.DataDir(w.ID))
	}
	return os.Remove(p)
}

// Import copies a workspace file into the library, as a new workspace.
func (l Library) Import(src string) (string, error) {
	w, err := Read(src)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(w.Name) == "" {
		w.Name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	// A shared file wasn't checked by this Chloe; its sources must pass what editing checks.
	for _, d := range w.DataSources {
		if err := d.Validate(); err != nil {
			return "", fmt.Errorf("data source %q: %w", d.Name, err)
		}
	}
	// Archived data stays behind; the copy's archive sources read again on refresh.
	w.ID = uuid.NewString()
	for i := range w.DataSources {
		w.DataSources[i].RefreshedAt, w.DataSources[i].ArchiveRows = "", 0
	}
	return l.Create(w)
}

// DataDir is the folder of a workspace's archived data.
func (l Library) DataDir(id string) string { return filepath.Join(l.Dir, "data", id) }

// ArchivePath is where a data source's archive snapshot is kept.
func (l Library) ArchivePath(workspaceID, sourceID string) string {
	return filepath.Join(l.DataDir(workspaceID), sourceID+".archive")
}

func (l Library) freePath(name string) string {
	base := fileName(name)
	p := filepath.Join(l.Dir, base+Ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(l.Dir, fmt.Sprintf("%s %d%s", base, i, Ext))
	}
}

var unsafeName = regexp.MustCompile(`[^\pL\pN\p{Pd} ._()]+`)

func fileName(name string) string {
	base := strings.Trim(unsafeName.ReplaceAllString(name, "-"), " .-")
	if base == "" {
		base = "workspace"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return base
}

// Read loads a workspace file.
func Read(p string) (Workspace, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Workspace{}, err
	}
	return Parse(data)
}

// Write saves a workspace file.
func Write(p string, w Workspace) error {
	w.Format, w.Version = Format, FormatVersion
	if w.DataSources == nil {
		w.DataSources = []DataSource{}
	}
	if w.Charts == nil {
		w.Charts = []Chart{}
	}
	if w.Dashboards == nil {
		w.Dashboards = []Dashboard{}
	}
	data, err := w.Marshal()
	if err != nil {
		return err
	}
	// Data source SQL and connection ids stay private to the user (files
	// written by earlier releases were 0644).
	return safefile.Write(p, data)
}

func copyDir(from, to string) error {
	files, err := os.ReadDir(from)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o700); err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(from, f.Name()))
		if err != nil {
			return err
		}
		if err := safefile.Write(filepath.Join(to, f.Name()), data); err != nil {
			return err
		}
	}
	return nil
}
