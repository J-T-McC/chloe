package bi

import (
	"testing"
)

// Find and List parse a workspace again only after its file changes.
func TestReadCachedSeesEdits(t *testing.T) {
	l := Library{Dir: t.TempDir()}
	p, err := l.Create(New("Sales"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := l.Find("sales"); err != nil || got != p {
		t.Fatalf("find %q %v", got, err)
	}
	w1, _ := ReadCached(p)
	if w2, _ := ReadCached(p); w1.ID != w2.ID || len(w2.Charts) != 0 {
		t.Fatal("a second read of an unchanged file differs")
	}
	if _, err := l.Update(p, func(w *Workspace) error {
		w.Name = "Revenue" // the file keeps its name
		w.Charts = append(w.Charts, Chart{Meta: Meta{ID: "c1", Name: "c"}})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if w, _ := ReadCached(p); w.Name != "Revenue" || len(w.Charts) != 1 {
		t.Fatalf("stale workspace %q with %d charts", w.Name, len(w.Charts))
	}
	if got, err := l.Find("Revenue"); err != nil || got != p {
		t.Fatalf("find after rename %q %v", got, err)
	}
	if _, err := l.Find("sales"); err == nil {
		t.Fatal("found the old name")
	}
}
