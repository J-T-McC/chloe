package filelib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type doc struct{ Name string }

func testLib(t *testing.T) Lib[doc] {
	return Lib[doc]{Dir: t.TempDir(), Ext: ".doc", Kind: "doc",
		Read: func(p string) (doc, error) {
			var d doc
			b, err := os.ReadFile(p)
			if err == nil {
				err = json.Unmarshal(b, &d)
			}
			return d, err
		},
		Write: func(p string, d doc) error {
			b, _ := json.Marshal(d)
			return os.WriteFile(p, b, 0o600)
		},
		Name: func(d *doc) string { return d.Name }, SetName: func(d *doc, n string) { d.Name = n }}
}

func TestCreateRenameDelete(t *testing.T) {
	l := testLib(t)
	a, err := l.Create(doc{Name: "Sales / 2026"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(a) != "Sales - 2026.doc" {
		t.Fatalf("path %s", a)
	}
	b, _ := l.Create(doc{Name: "Sales / 2026"})
	if filepath.Base(b) != "Sales - 2026 2.doc" {
		t.Fatalf("second path %s", b)
	}
	if _, err := l.Create(doc{Name: " "}); err == nil {
		t.Fatal("created a doc without a name")
	}
	n, err := l.Rename(b, "Costs")
	if err != nil || filepath.Base(n) != "Costs.doc" {
		t.Fatalf("rename: %s %v", n, err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatal("old file kept")
	}
	if d, _ := l.Read(n); d.Name != "Costs" {
		t.Fatalf("renamed doc %+v", d)
	}
	if len(l.Files()) != 2 {
		t.Fatalf("files %v", l.Files())
	}
	outside := filepath.Join(t.TempDir(), "x.doc")
	if l.Contains(outside) || l.Delete(outside) == nil {
		t.Fatal("touched a file outside the folder")
	}
	if err := l.Delete(n); err != nil {
		t.Fatal(err)
	}
}

func TestImportNamesFromFile(t *testing.T) {
	l := testLib(t)
	src := filepath.Join(t.TempDir(), "From Disk.doc")
	l.Write(src, doc{})
	p, err := l.Import(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d, _ := l.Read(p); d.Name != "From Disk" {
		t.Fatalf("imported %+v", d)
	}
}
