package core_test

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/model"
)

var pasteAt = time.Date(2026, 11, 26, 15, 30, 45, 0, time.Local)

func liteTable(name string) model.ObjectRef {
	return model.ObjectRef{Database: "main", Name: name, Kind: model.KindTable}
}

func schemaNames(t *testing.T, m *core.Manager, id, db string) []string {
	t.Helper()
	var out []string
	for _, r := range queryAll(t, m, id, "main", "", `SELECT name FROM "`+db+`".sqlite_schema ORDER BY name`) {
		out = append(out, deref(r[0]))
	}
	return out
}

func TestPasteSQLiteMain(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	res, err := m.PasteTables(context.Background(), id, []model.ObjectRef{liteTable("orders")}, core.PasteTarget{Database: "main"}, pasteAt)
	if err != nil {
		t.Fatal(err)
	}
	want := core.PasteResult{Source: liteTable("orders"), Name: "orders_copy_20261126_153045", Created: true}
	if len(res) != 1 || res[0] != want {
		t.Fatalf("results %+v", res)
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM orders_copy_20261126_153045"); deref(n[0][0]) != "2" {
		t.Fatalf("copied rows %s", deref(n[0][0]))
	}
	// The seed's two orders fired the trigger; the copy's rows go in before its trigger exists.
	if a := queryAll(t, m, id, "main", "", "SELECT count(*) FROM audit"); deref(a[0][0]) != "2" {
		t.Fatalf("copy fired the trigger: %s", deref(a[0][0]))
	}
	names := schemaNames(t, m, id, "main")
	for _, n := range []string{"orders_copy_20261126_153045_customer", "orders_copy_20261126_153045_note", "orders_copy_20261126_153045_audit"} {
		if !slices.Contains(names, n) {
			t.Fatalf("%s missing from %v", n, names)
		}
	}
}

func TestPasteSQLiteAttached(t *testing.T) {
	m, id, dir := sqliteSetup(t)
	ctx := context.Background()
	if r := m.ApplyDatabase(ctx, id, nil, design.Database{Name: "archive", File: filepath.Join(dir, "archive.db"), Create: true}); r.Error != "" {
		t.Fatal(r.Error)
	}
	res, err := m.PasteTables(ctx, id, []model.ObjectRef{liteTable("customers")}, core.PasteTarget{Database: "archive"}, pasteAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Error != "" || !res[0].Created || res[0].Name != "customers_copy_20261126_153045" {
		t.Fatalf("results %+v", res)
	}
	if n := queryAll(t, m, id, "main", "", `SELECT count(*) FROM archive.customers_copy_20261126_153045`); deref(n[0][0]) != "2" {
		t.Fatalf("copied rows %s", deref(n[0][0]))
	}
	if slices.Contains(schemaNames(t, m, id, "main"), "customers_copy_20261126_153045") {
		t.Fatal("the copy landed in main")
	}
}

func TestPasteSQLiteTakenNames(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	exec(t, m, id, `CREATE TABLE "Orders_Copy_20261126_153045" (x); CREATE INDEX customers_copy_20261126_153045 ON audit (msg)`)
	refs := []model.ObjectRef{liteTable("orders"), liteTable("customers"), liteTable("orders")}
	res, err := m.PasteTables(context.Background(), id, refs, core.PasteTarget{Database: "main"}, pasteAt)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range res {
		if r.Error != "" || !r.Created {
			t.Fatalf("result %+v", r)
		}
		got = append(got, r.Name)
	}
	// A table differing only in case and an index both take the name; the same
	// table pasted twice in one call gets the next free one.
	want := []string{"orders_copy_20261126_153045_2", "customers_copy_20261126_153045_2", "orders_copy_20261126_153045_3"}
	if !slices.Equal(got, want) {
		t.Fatalf("names %v, want %v", got, want)
	}
}

func TestPasteSQLiteOneFails(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	exec(t, m, id, "CREATE TABLE gone (x); DROP TABLE gone")
	res, err := m.PasteTables(context.Background(), id, []model.ObjectRef{liteTable("gone"), liteTable("customers")}, core.PasteTarget{Database: "main"}, pasteAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].Error == "" || res[0].Created || res[0].Name != "gone_copy_20261126_153045" {
		t.Fatalf("failed table %+v", res)
	}
	if res[1].Error != "" || !res[1].Created {
		t.Fatalf("other table %+v", res[1])
	}
}

func TestPasteValidation(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	ctx := context.Background()
	main := core.PasteTarget{Database: "main"}
	for _, c := range []struct {
		conn string
		refs []model.ObjectRef
		want string
	}{
		{id, nil, "nothing to paste"},
		{id, []model.ObjectRef{liteTable("orders"), {Database: "main", Name: "big_orders", Kind: model.KindView}}, "only tables can be pasted"},
	} {
		if _, err := m.PasteTables(ctx, c.conn, c.refs, main, pasteAt); err == nil || err.Error() != c.want {
			t.Fatalf("%v: got %v, want %q", c.refs, err, c.want)
		}
	}
	if _, err := m.PasteTables(ctx, "nope", []model.ObjectRef{liteTable("orders")}, main, pasteAt); err == nil {
		t.Fatal("unknown connection accepted")
	}
	if _, err := m.PasteTables(ctx, id, []model.ObjectRef{liteTable("orders")}, core.PasteTarget{Database: "nope"}, pasteAt); err == nil {
		t.Fatal("unknown target database accepted")
	}
	if names := schemaNames(t, m, id, "main"); slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "_copy_") }) {
		t.Fatalf("a rejected paste created %v", names)
	}
}

// Duplicate Table's SQL is pinned: Paste shares its builder, and must not change it.
func TestSQLiteDuplicateSQLPinned(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	create := "CREATE TABLE \"main\".\"orders2\" (\n  \"id\" INTEGER PRIMARY KEY,\n  \"customer_id\" INTEGER NOT NULL,\n  \"total\" DECIMAL(10,2) DEFAULT 0,\n  \"placed_at\" DATETIME DEFAULT CURRENT_TIMESTAMP,\n  \"note\" BLOB,\n  FOREIGN KEY (\"customer_id\") REFERENCES \"customers\" (\"id\") ON DELETE CASCADE\n)"
	indexes := []string{
		"CREATE INDEX \"main\".\"orders2_customer\" ON \"orders2\" (\"customer_id\", \"total\" DESC)",
		"CREATE UNIQUE INDEX \"main\".\"orders2_note\" ON \"orders2\" (lower(note)) WHERE note IS NOT NULL",
	}
	insert := "INSERT INTO \"main\".\"orders2\" (\"id\", \"customer_id\", \"total\", \"placed_at\", \"note\") SELECT \"id\", \"customer_id\", \"total\", \"placed_at\", \"note\" FROM \"main\".\"orders\""
	trigger := "CREATE TRIGGER \"main\".\"orders2_audit\" AFTER INSERT ON \"orders2\" FOR EACH ROW WHEN NEW.total > 0\nBEGIN\n  INSERT INTO audit (msg) VALUES ('order ' || NEW.id || ';');\nEND"
	for action, want := range map[core.TableAction][]string{
		core.ActionCopy:      append(append([]string{create}, indexes...), insert, trigger),
		core.ActionDuplicate: append(append([]string{create}, indexes...), trigger),
	} {
		got, err := m.TableActionSQL(id, liteTable("orders"), action, "orders2")
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("%s SQL changed:\n%q\nwant\n%q", action, got, want)
		}
	}
}
