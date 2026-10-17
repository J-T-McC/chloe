package core

// Data Synchronization between two SQLite databases, which need no server.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chloe/internal/model"
	"chloe/internal/store"
)

func TestCompareKeys(t *testing.T) {
	s := func(v string) *string { return &v }
	cases := []struct {
		a, b    *string
		numeric bool
		want    int
	}{
		{s("9"), s("10"), true, -1},
		{s("-5"), s("2"), true, -1},
		{s("1.50"), s("1.5"), true, 1}, // equal values, so bytes decide
		{s("99999999999999999999"), s("100000000000000000000"), true, -1},
		{s("9"), s("10"), false, 1},
		{s("B"), s("a"), false, -1},
		{nil, s("0"), true, -1},
		{nil, nil, false, 0},
		{s("x"), s("x"), false, 0},
	}
	for _, c := range cases {
		if got := compareKeys([]*string{c.a}, []*string{c.b}, []bool{c.numeric}); got != c.want {
			t.Errorf("compareKeys(%v, %v, %v) = %d, want %d", deref(c.a), deref(c.b), c.numeric, got, c.want)
		}
	}
}

func deref(p *string) string {
	if p == nil {
		return "NULL"
	}
	return *p
}

func TestDataSyncSQLite(t *testing.T) {
	keep := syncKeep
	syncKeep = 3 // past it, deploying compares again
	t.Cleanup(func() { syncKeep = keep })

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, DefaultSettings)
	t.Cleanup(m.Close)
	ctx := context.Background()
	open := func(name string) Endpoint {
		c, err := st.Save(model.Connection{Name: name, Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, name+".db"), Create: true}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Connect(ctx, c.ID, ""); err != nil {
			t.Fatal(err)
		}
		return Endpoint{ConnID: c.ID, Database: "main"}
	}
	run := func(e Endpoint, sql string) {
		t.Helper()
		sid, err := m.OpenSession(ctx, e.ConnID, e.Database, "")
		if err != nil {
			t.Fatal(err)
		}
		defer m.CloseSession(sid)
		res, err := m.Execute(ctx, sid, sql, ExecOptions{})
		if err != nil || res[len(res)-1].Error != "" {
			t.Fatalf("%s: %v %+v", sql, err, res)
		}
	}
	src, dst := open("src"), open("dst")

	// nums: integer keys, where 10 sorts after 9. words: a NOCASE text key, read
	// in binary order. pairs: a composite key. mixed: an integer key in the
	// source and a text key in the target, compared by key instead of in order.
	// loose: an untyped key sorted as numbers but compared as bytes, which
	// breaks the order mid-stream and falls back to comparing by key.
	schema := func(mixedKey string) string {
		return `CREATE TABLE nums (id INTEGER PRIMARY KEY, v TEXT);
CREATE TABLE words (w TEXT PRIMARY KEY COLLATE NOCASE, v INTEGER);
CREATE TABLE pairs (a INTEGER, b VARCHAR(10), v TEXT, PRIMARY KEY (a, b));
CREATE TABLE mixed (id ` + mixedKey + ` PRIMARY KEY, v TEXT);
CREATE TABLE loose (id PRIMARY KEY, v);`
	}
	run(src, schema("INTEGER"))
	run(dst, schema("TEXT"))
	var sb, db strings.Builder
	for i := -3; i <= 20; i++ {
		fmt.Fprintf(&sb, "INSERT INTO nums VALUES (%d, 'n%d');\n", i, i)
		if i%4 != 0 { // the target lacks every fourth row, so they're inserted
			v := fmt.Sprintf("n%d", i)
			if i%5 == 0 {
				v = "changed"
			}
			fmt.Fprintf(&db, "INSERT INTO nums VALUES (%d, '%s');\n", i, v)
		}
	}
	for i := 100; i < 106; i++ { // only in the target: deleted
		fmt.Fprintf(&db, "INSERT INTO nums VALUES (%d, 'extra');\n", i)
	}
	run(src, sb.String()+`INSERT INTO words VALUES ('apple', 1), ('Banana', 2), ('cherry', 3), ('Date', 4);
INSERT INTO pairs VALUES (1, 'b', 'x'), (1, 'a', 'x'), (2, 'a', 'x'), (10, 'a', 'x');
INSERT INTO mixed VALUES (1, 'a'), (2, 'b'), (10, 'c');
INSERT INTO loose VALUES (2, 'a'), (10, 'b'), (11, 'c');`)
	run(dst, db.String()+`INSERT INTO words VALUES ('Apple', 1), ('Banana', 9), ('date', 4), ('Eel', 5);
INSERT INTO pairs VALUES (1, 'a', 'x'), (2, 'a', 'y'), (9, 'z', 'x');
INSERT INTO mixed VALUES ('2', 'b'), ('10', 'changed'), ('3', 'd');
INSERT INTO loose VALUES (2, 'a'), (10, 'x'), (12, 'd');`)

	tables := []DataSyncTable{{Source: "nums", Target: "nums"}, {Source: "words", Target: "words"}, {Source: "pairs", Target: "pairs"}, {Source: "mixed", Target: "mixed"}, {Source: "loose", Target: "loose"}}
	cmp, err := m.CompareData(ctx, DataSyncRequest{Source: src, Target: dst, Tables: tables})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][4]int{ // insert, update, delete, same
		"nums":  {6, 3, 6, 15},
		"words": {3, 1, 3, 0}, // keys match by bytes: apple ≠ Apple
		"pairs": {2, 1, 1, 1},
		"mixed": {1, 1, 1, 1},
		"loose": {1, 1, 1, 1},
	}
	for _, r := range cmp.Tables {
		if r.Message != "" {
			t.Fatalf("%s: %s", r.Source, r.Message)
		}
		if got := [4]int{r.Insert, r.Update, r.Delete, r.Same}; got != want[r.Source] {
			t.Errorf("%s insert/update/delete/same = %v, want %v", r.Source, got, want[r.Source])
		}
	}
	ds, _ := m.dataSync(cmp.ID)
	for _, tb := range ds.tables {
		if wantOrdered := tb.res.Source != "mixed" && tb.res.Source != "loose"; tb.ordered != wantOrdered {
			t.Errorf("%s ordered = %v, want %v", tb.res.Source, tb.ordered, wantOrdered)
		}
	}

	page, err := m.DataSyncRecords(cmp.ID, "nums", "insert", 0, 100)
	if err != nil || page.Total != 3 || page.More != 3 {
		t.Fatalf("nums inserts page: total %d more %d, %v", page.Total, page.More, err)
	}
	script, err := m.DataSyncScript(cmp.ID, []DataSyncSelection{{Table: "nums", Insert: true, Update: true, Delete: true}})
	if err != nil || !strings.HasPrefix(script[len(script)-1], "-- ") {
		t.Fatalf("script %q %v", script, err)
	}

	var sel []DataSyncSelection
	for _, tb := range tables {
		if tb.Source == "loose" {
			continue // untyped values are written as text, which doesn't match integers
		}
		sel = append(sel, DataSyncSelection{Table: tb.Source, Insert: true, Update: true, Delete: true})
	}
	job, err := m.DeployDataSync(DataSyncDeployRequest{ID: cmp.ID, Tables: sel, UseTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	done, err := m.WaitJob(wctx, job.ID)
	if err != nil || done.Status != JobDone || done.Errors > 0 {
		t.Fatalf("deploy: %v %s %s\n%s", err, done.Status, done.Message, strings.Join(done.Log, "\n"))
	}
	again, err := m.CompareData(ctx, DataSyncRequest{Source: src, Target: dst, Tables: tables})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range again.Tables {
		if r.Source != "loose" && (r.Insert+r.Update+r.Delete != 0 || r.Message != "") {
			t.Errorf("after deploying, %s still differs: %+v", r.Source, r)
		}
	}
}
