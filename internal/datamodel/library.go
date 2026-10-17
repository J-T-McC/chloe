package datamodel

import (
	"errors"
	"os"
	"sort"
	"strings"

	"chloe/internal/design"
	"chloe/internal/filelib"
	"chloe/internal/safefile"
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

func (l Library) lib() filelib.Lib[Model] {
	return filelib.Lib[Model]{Dir: l.Dir, Ext: Ext, Kind: "model", Read: Read, Write: Write,
		Name: func(m *Model) string { return m.Name }, SetName: func(m *Model, n string) { m.Name = n }}
}

// List reads every model in the library, by name.
func (l Library) List() []Entry {
	out := []Entry{}
	for _, f := range l.lib().Files() {
		e := Entry{Path: f.Path, Name: f.Base, Modified: f.Modified}
		if m, err := Read(f.Path); err != nil {
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
func (l Library) Contains(p string) bool { return l.lib().Contains(p) }

// Create saves m under a new file named after it and returns the path.
func (l Library) Create(m Model) (string, error) { return l.lib().Create(m) }

// Update reads a library model, lets fn change it, and writes it back, holding
// the model's lock throughout.
func (l Library) Update(p string, fn func(*Model) error) (Model, error) { return l.lib().Update(p, fn) }

// Rename renames a library model, file included, and returns its new path.
func (l Library) Rename(p, name string) (string, error) { return l.lib().Rename(p, name) }

// Duplicate copies a library model under a new name and returns the copy's path.
func (l Library) Duplicate(p string) (string, error) {
	if !l.Contains(p) {
		return "", errors.New("not a model in Chloe's library")
	}
	m, err := Read(p)
	if err != nil {
		return "", err
	}
	m.Name, m.CreatedBy = m.Name+" copy", ""
	return l.Create(m)
}

// Delete removes a library model.
func (l Library) Delete(p string) error { return l.lib().Delete(p) }

// Import copies a model file into the library.
func (l Library) Import(src string) (string, error) { return l.lib().Import(src, nil) }

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
	// Table designs and connection ids stay private to the user (files
	// written by earlier releases were 0644).
	return safefile.Write(p, data)
}
