// Package filelib is a folder of documents Chloe manages, one file each, named
// after the document: BI workspaces and ER models.
package filelib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Lib describes one kind of document: its folder, file extension, and how a
// document is read, written and named.
type Lib[T any] struct {
	Dir   string
	Ext   string
	Kind  string // "workspace", "model": in errors, and the file name of a document without one
	Read  func(path string) (T, error)
	Write func(path string, v T) error
	// Name and SetName get and set the document's own name.
	Name    func(*T) string
	SetName func(*T, string)
}

// File is one document file found in the folder.
type File struct {
	Path     string
	Base     string // the file name without the extension
	Modified string
}

// Files lists the document files in the folder, in directory order.
func (l Lib[T]) Files() []File {
	var out []File
	files, _ := os.ReadDir(l.Dir)
	for _, f := range files {
		if f.IsDir() || filepath.Ext(f.Name()) != l.Ext {
			continue
		}
		e := File{Path: filepath.Join(l.Dir, f.Name()), Base: strings.TrimSuffix(f.Name(), l.Ext)}
		if info, err := f.Info(); err == nil {
			e.Modified = info.ModTime().UTC().Format(time.RFC3339)
		}
		out = append(out, e)
	}
	return out
}

// Contains reports whether p is a document file in the folder.
func (l Lib[T]) Contains(p string) bool {
	p = filepath.Clean(p)
	return filepath.Dir(p) == filepath.Clean(l.Dir) && filepath.Ext(p) == l.Ext
}

func (l Lib[T]) notOurs() error { return fmt.Errorf("not a %s in Chloe's library", l.Kind) }

// Create saves v under a new file named after it and returns the path.
func (l Lib[T]) Create(v T) (string, error) {
	if strings.TrimSpace(l.Name(&v)) == "" {
		return "", fmt.Errorf("a %s needs a name", l.Kind)
	}
	if err := os.MkdirAll(l.Dir, 0o755); err != nil {
		return "", err
	}
	p := l.freePath(l.Name(&v))
	return p, l.Write(p, v)
}

// Update reads a document of the folder, lets fn change it, and writes it back,
// holding the file's lock throughout.
func (l Lib[T]) Update(p string, fn func(*T) error) (T, error) {
	if !l.Contains(p) {
		var zero T
		return zero, l.notOurs()
	}
	defer lock(p)()
	v, err := l.Read(p)
	if err != nil {
		return v, err
	}
	if err := fn(&v); err != nil {
		return v, err
	}
	return v, l.Write(p, v)
}

// Rename renames a document, file included, and returns its new path.
func (l Lib[T]) Rename(p, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("a %s needs a name", l.Kind)
	}
	v, err := l.Update(p, func(v *T) error { l.SetName(v, name); return nil })
	if err != nil {
		return "", err
	}
	if l.fileName(name) == l.fileName(strings.TrimSuffix(filepath.Base(p), l.Ext)) {
		return p, nil
	}
	np := l.freePath(name)
	if err := l.Write(np, v); err != nil {
		return "", err
	}
	return np, os.Remove(p)
}

// Delete removes a document file of the folder.
func (l Lib[T]) Delete(p string) error {
	if !l.Contains(p) {
		return l.notOurs()
	}
	return os.Remove(p)
}

// Import reads a file from anywhere, names it after the file when it has no
// name, lets prepare check or change it, and saves it into the folder.
func (l Lib[T]) Import(src string, prepare func(*T) error) (string, error) {
	v, err := l.Read(src)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(l.Name(&v)) == "" {
		l.SetName(&v, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)))
	}
	if prepare != nil {
		if err := prepare(&v); err != nil {
			return "", err
		}
	}
	return l.Create(v)
}

func (l Lib[T]) freePath(name string) string {
	base := l.fileName(name)
	p := filepath.Join(l.Dir, base+l.Ext)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p
		}
		p = filepath.Join(l.Dir, fmt.Sprintf("%s %d%s", base, i, l.Ext))
	}
}

var unsafeName = regexp.MustCompile(`[^\pL\pN\p{Pd} ._()]+`)

// fileName makes a document name safe as a file name.
func (l Lib[T]) fileName(name string) string {
	base := strings.Trim(unsafeName.ReplaceAllString(name, "-"), " .-")
	if base == "" {
		base = l.Kind
	}
	if len(base) > 80 {
		base = base[:80]
	}
	return base
}

// locks serialize read-modify-write cycles on one file: the designer and MCP
// agents change the same documents.
var locks sync.Map

func lock(p string) func() {
	m, _ := locks.LoadOrStore(filepath.Clean(p), &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
