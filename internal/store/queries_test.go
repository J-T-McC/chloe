package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chloe/internal/model"
)

func TestSavedQueries(t *testing.T) {
	dir := t.TempDir()
	kc := &persistentSecrets{}
	s, _ := Open(dir, kc)
	c, _ := s.Save(model.Connection{Name: "db", Type: model.MySQL})
	w := s.Workspace()

	q, err := w.SaveQuery(SavedQuery{ConnID: c.ID, Database: "shop", Name: "Top customers", SQL: "SELECT * FROM customers"})
	if err != nil || q.ID == "" || q.Created == "" {
		t.Fatalf("save %+v %v", q, err)
	}
	if _, err := w.SaveQuery(SavedQuery{ConnID: c.ID, Database: "shop", Name: "top CUSTOMERS"}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	if _, err := w.SaveQuery(SavedQuery{ConnID: c.ID, Database: "other", Name: "Top customers"}); err != nil {
		t.Fatal("same name in another database:", err)
	}
	if n := w.UniqueQueryName(c.ID, "shop", "", "Top customers"); n != "Top customers 2" {
		t.Fatalf("unique name %q", n)
	}
	q.SQL = "SELECT 1"
	if q2, err := w.SaveQuery(q); err != nil || q2.Created != q.Created {
		t.Fatalf("update %+v %v", q2, err)
	}
	if _, err := w.RenameQuery(q.ID, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if got := w.Queries(c.ID, "shop", ""); len(got) != 1 || got[0].Name != "Renamed" || got[0].SQL != "SELECT 1" {
		t.Fatalf("list %+v", got)
	}

	// Encrypted on disk, and read back by a new store.
	raw, _ := os.ReadFile(filepath.Join(dir, "queries.json"))
	if strings.Contains(string(raw), "SELECT") || !isSealed(raw) {
		t.Fatalf("queries not encrypted: %s", raw)
	}
	s2, _ := Open(dir, kc)
	if got := s2.Workspace().Queries(c.ID, "shop", ""); len(got) != 1 || got[0].SQL != "SELECT 1" {
		t.Fatalf("reopen %+v", got)
	}

	if err := w.DeleteQuery(q.ID); err != nil || len(w.Queries(c.ID, "shop", "")) != 0 {
		t.Fatal("delete", err)
	}
	// Deleting the connection drops its queries.
	s.Delete(c.ID)
	if len(w.Queries(c.ID, "other", "")) != 0 {
		t.Fatal("queries outlived their connection")
	}
}

func TestHistoryAndDrafts(t *testing.T) {
	historyDelay = 10 * time.Millisecond
	dir := t.TempDir()
	kc := &persistentSecrets{}
	s, _ := Open(dir, kc)
	w := s.Workspace()
	w.AddHistory(HistoryEntry{ConnID: "a", SQL: "SELECT 1", Rows: 1}, HistoryEntry{ConnID: "b", SQL: "SELECT 2"})
	w.AddHistory(HistoryEntry{ConnID: "a", SQL: "bad", Error: "syntax"})
	if h := w.History(""); len(h) != 3 || h[0].SQL != "bad" || h[0].Time == "" {
		t.Fatalf("history %+v", h)
	}
	if h := w.History("a"); len(h) != 2 {
		t.Fatalf("filtered %+v", h)
	}
	time.Sleep(100 * time.Millisecond) // the batched write
	if got := mustOpen(t, dir, kc).Workspace().History(""); len(got) != 3 {
		t.Fatalf("history not written: %d", len(got))
	}
	for i := 0; i < MaxHistory+10; i++ {
		w.AddHistory(HistoryEntry{ConnID: "a", SQL: "x"})
	}
	if n := len(w.History("")); n != MaxHistory {
		t.Fatalf("history not capped: %d", n)
	}
	if err := w.ClearHistory(); err != nil || len(w.History("")) != 0 {
		t.Fatal("clear", err)
	}

	if err := w.SetDrafts([]Draft{{TabID: "t1", ConnID: "a", Title: "Query", SQL: "SELECT draft", Kind: "query"}}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "drafts.json")); strings.Contains(string(raw), "draft") {
		t.Fatal("drafts not encrypted")
	}
	if d := mustOpen(t, dir, kc).Workspace().Drafts(); len(d) != 1 || d[0].SQL != "SELECT draft" {
		t.Fatalf("drafts %+v", d)
	}
}

func TestLockedWorkspaceNeverWrites(t *testing.T) {
	dir := t.TempDir()
	kc := &persistentSecrets{}
	s, _ := Open(dir, kc)
	s.Save(model.Connection{Name: "x", Type: model.MySQL})
	s.Workspace().SaveQuery(SavedQuery{ConnID: "c", Database: "d", Name: "q", SQL: "SELECT 1"})
	before, _ := os.ReadFile(filepath.Join(dir, "queries.json"))

	locked := mustOpen(t, dir, &persistentSecrets{})
	w := locked.Workspace()
	if w.Problem() == "" {
		t.Fatal("expected a locked workspace")
	}
	if _, err := w.SaveQuery(SavedQuery{ConnID: "c", Database: "d", Name: "new"}); err == nil {
		t.Fatal("locked workspace saved")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "queries.json")); string(after) != string(before) {
		t.Fatal("locked workspace changed the file")
	}
}

func mustOpen(t *testing.T, dir string, sec Secrets) *Store {
	t.Helper()
	s, err := Open(dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
