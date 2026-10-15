package datamodel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibrary(t *testing.T) {
	lib := Library{Dir: filepath.Join(t.TempDir(), "models")}
	if got := lib.List(); len(got) != 0 {
		t.Fatalf("empty library: %v", got)
	}
	a, err := lib.Create(New("Shop / v2", "mysql"))
	if err != nil || filepath.Base(a) != "Shop - v2.chloemodel" {
		t.Fatalf("create: %s %v", a, err)
	}
	b, _ := lib.Create(New("Shop / v2", "mysql"))
	if filepath.Base(b) != "Shop - v2 2.chloemodel" {
		t.Fatalf("unique: %s", b)
	}
	if _, err := lib.Create(New(" ", "mysql")); err == nil {
		t.Fatal("created without a name")
	}

	r, err := lib.Rename(b, "Archive (old)")
	if err != nil || filepath.Base(r) != "Archive (old).chloemodel" {
		t.Fatalf("rename: %s %v", r, err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Fatal("the old file is still there")
	}
	// Renaming to the same file name keeps the file.
	if same, err := lib.Rename(a, "Shop - v2"); err != nil || same != a {
		t.Fatalf("same name: %s %v", same, err)
	}

	es := lib.List()
	if len(es) != 2 || es[0].Name != "Archive (old)" || es[1].Name != "Shop - v2" || es[0].Dialect != "mysql" || es[0].Modified == "" {
		t.Fatalf("list: %+v", es)
	}

	outside := filepath.Join(t.TempDir(), "x.chloemodel")
	m := New("", "postgres")
	if err := Write(outside, m); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{outside, filepath.Join(lib.Dir, "..", "x.chloemodel"), filepath.Join(lib.Dir, "notes.txt")} {
		if lib.Contains(p) {
			t.Errorf("contains %s", p)
		}
	}
	if err := lib.Delete(outside); err == nil {
		t.Fatal("deleted a file outside the library")
	}
	if _, err := lib.Rename(outside, "y"); err == nil {
		t.Fatal("renamed a file outside the library")
	}
	imp, err := lib.Import(outside)
	if err != nil || filepath.Base(imp) != "x.chloemodel" || !lib.Contains(imp) {
		t.Fatalf("import: %s %v", imp, err)
	}
	if err := lib.Delete(imp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatal("import or delete touched the original")
	}

	// An unreadable file is listed with its error, not dropped.
	_ = os.WriteFile(filepath.Join(lib.Dir, "broken.chloemodel"), []byte("{"), 0o644)
	var broken *Entry
	for _, e := range lib.List() {
		if e.Name == "broken" {
			broken = &e
		}
	}
	if broken == nil || broken.Error == "" {
		t.Fatalf("broken: %+v", broken)
	}
}
