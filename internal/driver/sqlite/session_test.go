package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"chloe/internal/model"
)

// A session keeps a statement's cancel func only while it runs or its rows are open.
func TestSessionReleasesFinishedStatements(t *testing.T) {
	ctx := context.Background()
	c, err := Driver{}.Open(ctx, model.Connection{Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(t.TempDir(), "x.db"), Create: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ds, err := c.NewSession(ctx, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	defer ds.Close()
	s := ds.(*Session)
	if _, err := s.Exec(ctx, "CREATE TABLE t (x)"); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		rows, err := s.Query(ctx, "SELECT 1")
		if err != nil {
			t.Fatal(err)
		}
		rows.Next(make([]*string, 1))
		rows.Close()
	}
	open, _ := s.Query(ctx, "SELECT 2")
	if n := len(s.cancels); n != 1 {
		t.Fatalf("%d cancel funcs kept with one result open", n)
	}
	open.Close()
	if n := len(s.cancels); n != 0 {
		t.Fatalf("%d cancel funcs kept after closing", n)
	}
}
