package bi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"

	"chloe/internal/filelib"
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

func (l Library) lib() filelib.Lib[Workspace] {
	return filelib.Lib[Workspace]{Dir: l.Dir, Ext: Ext, Kind: "workspace", Read: Read, Write: Write,
		Name: func(w *Workspace) string { return w.Name }, SetName: func(w *Workspace, n string) { w.Name = n }}
}

// List reads every workspace in the library, by name.
func (l Library) List() []Entry {
	out := []Entry{}
	for _, f := range l.lib().Files() {
		e := Entry{Path: f.Path, Name: f.Base, Modified: f.Modified}
		if w, err := Read(f.Path); err != nil {
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
func (l Library) Contains(p string) bool { return l.lib().Contains(p) }

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
func (l Library) Create(w Workspace) (string, error) { return l.lib().Create(w) }

// Update reads a library workspace, lets fn change it, and writes it back,
// holding the workspace's lock throughout.
func (l Library) Update(p string, fn func(*Workspace) error) (Workspace, error) {
	return l.lib().Update(p, fn)
}

// Rename renames a workspace, file included, and returns its new path.
func (l Library) Rename(p, name string) (string, error) { return l.lib().Rename(p, name) }

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
	return l.lib().Import(src, func(w *Workspace) error {
		// A shared file wasn't checked by this Chloe; its sources must pass what editing checks.
		for _, d := range w.DataSources {
			if err := d.Validate(); err != nil {
				return fmt.Errorf("data source %q: %w", d.Name, err)
			}
		}
		// Archived data stays behind; the copy's archive sources read again on refresh.
		w.ID = uuid.NewString()
		for i := range w.DataSources {
			w.DataSources[i].RefreshedAt, w.DataSources[i].ArchiveRows = "", 0
		}
		return nil
	})
}

// DataDir is the folder of a workspace's archived data.
func (l Library) DataDir(id string) string { return filepath.Join(l.Dir, "data", id) }

// ArchivePath is where a data source's archive snapshot is kept.
func (l Library) ArchivePath(workspaceID, sourceID string) string {
	return filepath.Join(l.DataDir(workspaceID), sourceID+".archive")
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
