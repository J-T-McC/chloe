package datamodel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"chloe/internal/design"
)

// Ext is the model file extension.
const Ext = ".chloemodel"

// Link remembers the database a model was built from or last synchronized to.
type Link struct {
	ConnID   string `json:"connId"`
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
}

// Library is the folder of models Chloe manages (Navicat keeps its models in its
// profile folder the same way). Files elsewhere can still be opened, and imported.
type Library struct{ Dir string }

// Entry describes one model in the library.
type Entry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Dialect   string `json:"dialect,omitempty"`
	Tables    int    `json:"tables"`
	Modified  string `json:"modified"`
	CreatedBy string `json:"createdBy,omitempty"`
	Link      *Link  `json:"link,omitempty"`
	Error     string `json:"error,omitempty"` // the file couldn't be read
}

// List reads every model in the library, by name.
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
		if m, err := Read(p); err != nil {
			e.Error = err.Error()
		} else {
			e.Name, e.Dialect, e.Tables, e.CreatedBy, e.Link = m.Name, m.Dialect, len(m.Tables), m.CreatedBy, m.Link
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Contains reports whether p is a model file in the library.
func (l Library) Contains(p string) bool {
	p = filepath.Clean(p)
	return filepath.Dir(p) == filepath.Clean(l.Dir) && filepath.Ext(p) == Ext
}

// Create saves m under a new file named after it and returns the path.
func (l Library) Create(m Model) (string, error) {
	if strings.TrimSpace(m.Name) == "" {
		return "", errors.New("a model needs a name")
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", err
	}
	p := l.freePath(m.Name)
	return p, Write(p, m)
}

// Rename renames a library model, file included, and returns its new path.
func (l Library) Rename(p, name string) (string, error) {
	name = strings.TrimSpace(name)
	if !l.Contains(p) {
		return "", errors.New("not a model in Chloe's library")
	}
	if name == "" {
		return "", errors.New("a model needs a name")
	}
	m, err := Read(p)
	if err != nil {
		return "", err
	}
	m.Name = name
	np := p
	if fileName(name) != fileName(strings.TrimSuffix(filepath.Base(p), Ext)) {
		np = l.freePath(name)
	}
	if err := Write(np, m); err != nil {
		return "", err
	}
	if np != p {
		if err := os.Remove(p); err != nil {
			return np, err
		}
	}
	return np, nil
}

// Delete removes a library model.
func (l Library) Delete(p string) error {
	if !l.Contains(p) {
		return errors.New("not a model in Chloe's library")
	}
	return os.Remove(p)
}

// Import copies a model file into the library.
func (l Library) Import(src string) (string, error) {
	m, err := Read(src)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(m.Name) == "" {
		m.Name = strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	}
	return l.Create(m)
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

// fileName makes a model name safe as a file name.
func fileName(name string) string {
	base := strings.Trim(unsafeName.ReplaceAllString(name, "-"), " .-")
	if base == "" {
		base = "model"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return base
}

// Read loads a model file.
func Read(p string) (Model, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Model{}, err
	}
	return Parse(data)
}

// Write saves a model file.
func Write(p string, m Model) error {
	m.Format, m.Version = Format, max(m.Version, 1)
	if m.Positions == nil {
		m.Positions = map[string]Point{}
	}
	if m.Tables == nil {
		m.Tables = []design.Table{}
	}
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}
