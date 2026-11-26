package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"chloe/internal/core"
	"chloe/internal/model"
)

// execIn runs a script in db and fails on any statement error.
func execIn(t *testing.T, m *core.Manager, id, db, script string) {
	t.Helper()
	ctx := context.Background()
	sid, err := m.OpenSession(ctx, id, db, "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseSession(sid)
	res, err := m.Execute(ctx, sid, script, core.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != "" {
			t.Fatalf("%s: %s", r.SQL, r.Error)
		}
	}
}

func rowStrings(rows [][]*string) []string {
	var out []string
	for _, r := range rows {
		var cells []string
		for _, c := range r {
			cells = append(cells, deref(c))
		}
		out = append(out, strings.Join(cells, "|"))
	}
	return out
}

func TestIntegrationPasteMySQL(t *testing.T) {
	m, id := setup(t, engines()[0])
	ctx := context.Background()
	execIn(t, m, id, "shop", "DROP DATABASE IF EXISTS paste_it_a; DROP DATABASE IF EXISTS paste_it_b; CREATE DATABASE paste_it_a; CREATE DATABASE paste_it_b")
	t.Cleanup(func() {
		execIn(t, m, id, "shop", "DROP DATABASE IF EXISTS paste_it_a; DROP DATABASE IF EXISTS paste_it_b")
	})
	execIn(t, m, id, "paste_it_a", `
CREATE TABLE src (
  id INT AUTO_INCREMENT PRIMARY KEY,
  a INT NOT NULL,
  doubled INT AS (a * 2) STORED,
  plus1 INT AS (a + 1) VIRTUAL,
  KEY by_a (a),
  UNIQUE KEY by_plus1 (plus1)
);
INSERT INTO src (a) VALUES (1), (2), (5);
CREATE TABLE other (x INT);
CREATE TABLE `+strings.Repeat("l", 60)+` (x INT);
INSERT INTO other VALUES (7)`)
	ref := func(db, name string) model.ObjectRef {
		return model.ObjectRef{Database: db, Name: name, Kind: model.KindTable}
	}
	src := ref("paste_it_a", "src")
	srcRows := rowStrings(queryAll(t, m, id, "paste_it_a", "", "SELECT id, a, doubled, plus1 FROM src ORDER BY id"))
	srcIndexes := rowStrings(queryAll(t, m, id, "paste_it_a", "", "SELECT index_name, column_name, non_unique FROM information_schema.STATISTICS WHERE table_schema = 'paste_it_a' AND table_name = 'src' ORDER BY index_name, seq_in_index"))

	t.Run("same and other database", func(t *testing.T) {
		for _, db := range []string{"paste_it_a", "paste_it_b"} {
			res, err := m.PasteTables(ctx, id, []model.ObjectRef{src}, core.PasteTarget{Database: db}, pasteAt)
			if err != nil {
				t.Fatal(err)
			}
			name := "src_copy_20261126_153045"
			if len(res) != 1 || res[0] != (core.PasteResult{Source: src, Name: name, Created: true}) {
				t.Fatalf("%s: results %+v", db, res)
			}
			if got := rowStrings(queryAll(t, m, id, db, "", "SELECT id, a, doubled, plus1 FROM "+name+" ORDER BY id")); !slices.Equal(got, srcRows) {
				t.Fatalf("%s: rows %v, want %v", db, got, srcRows)
			}
			ix := rowStrings(queryAll(t, m, id, db, "", "SELECT index_name, column_name, non_unique FROM information_schema.STATISTICS WHERE table_schema = '"+db+"' AND table_name = '"+name+"' ORDER BY index_name, seq_in_index"))
			if !slices.Equal(ix, srcIndexes) {
				t.Fatalf("%s: indexes %v, want %v", db, ix, srcIndexes)
			}
			// The copy keeps AUTO_INCREMENT and computes its generated columns.
			execIn(t, m, id, db, "INSERT INTO "+name+" (a) VALUES (10)")
			if got := rowStrings(queryAll(t, m, id, db, "", "SELECT id, doubled, plus1 FROM "+name+" WHERE a = 10")); !slices.Equal(got, []string{"4|20|11"}) {
				t.Fatalf("%s: new row %v", db, got)
			}
		}
	})

	t.Run("taken by a table or a view", func(t *testing.T) {
		execIn(t, m, id, "paste_it_b", "CREATE TABLE OTHER_COPY_20261126_153045 (x INT); CREATE VIEW src_copy_20261126_153045_2 AS SELECT 1 AS one")
		res, err := m.PasteTables(ctx, id, []model.ObjectRef{ref("paste_it_a", "other"), src}, core.PasteTarget{Database: "paste_it_b"}, pasteAt)
		if err != nil {
			t.Fatal(err)
		}
		// src_copy_… was made by the subtest above, and its _2 is a view.
		var got []string
		for _, r := range res {
			if r.Error != "" || !r.Created {
				t.Fatalf("result %+v", r)
			}
			got = append(got, r.Name)
		}
		if want := []string{"other_copy_20261126_153045_2", "src_copy_20261126_153045_3"}; !slices.Equal(got, want) {
			t.Fatalf("names %v, want %v", got, want)
		}
	})

	t.Run("long name", func(t *testing.T) {
		long := strings.Repeat("l", 60)
		res, err := m.PasteTables(ctx, id, []model.ObjectRef{ref("paste_it_a", long)}, core.PasteTarget{Database: "paste_it_b"}, pasteAt)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Repeat("l", 43) + "_copy_20261126_153045"
		if len(res) != 1 || res[0].Error != "" || res[0].Name != want || len(want) != 64 {
			t.Fatalf("results %+v", res)
		}
		queryAll(t, m, id, "paste_it_b", "", "SELECT count(*) FROM "+want)
	})

	t.Run("created without rows", func(t *testing.T) {
		// Chloe's sessions keep the server's strict mode, which refuses a zero date.
		mode := deref(queryAll(t, m, id, "paste_it_a", "", "SELECT @@SESSION.sql_mode")[0][0])
		if !strings.Contains(mode, "STRICT_TRANS_TABLES") || !strings.Contains(mode, "NO_ZERO_DATE") {
			t.Fatalf("session sql_mode %q isn't strict", mode)
		}
		execIn(t, m, id, "paste_it_a", "SET SESSION sql_mode = ''; CREATE TABLE zero (d DATE); INSERT INTO zero VALUES ('0000-00-00')")
		res, err := m.PasteTables(ctx, id, []model.ObjectRef{ref("paste_it_a", "zero"), ref("paste_it_a", "other")}, core.PasteTarget{Database: "paste_it_a"}, pasteAt)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 2 || res[0].Error == "" || !res[0].Created || res[0].Name != "zero_copy_20261126_153045" {
			t.Fatalf("failed table %+v", res)
		}
		if res[1].Error != "" || !res[1].Created {
			t.Fatalf("other table %+v", res[1])
		}
		if n := deref(queryAll(t, m, id, "paste_it_a", "", "SELECT count(*) FROM zero_copy_20261126_153045")[0][0]); n != "0" {
			t.Fatalf("rows in the half-made copy: %s", n)
		}
	})

	t.Run("postgres needs a schema", func(t *testing.T) {
		pm, pid := setup(t, engines()[1])
		_, err := pm.PasteTables(ctx, pid, []model.ObjectRef{{Database: "postgres", Schema: "shop", Name: "orders", Kind: model.KindTable}}, core.PasteTarget{Database: "postgres"}, pasteAt)
		if err == nil || err.Error() != "choose a schema to paste into" {
			t.Fatalf("no schema: %v", err)
		}
	})
}
