package core_test

// SQLite needs no server, so these tests always run.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/model"
	"chloe/internal/store"
)

const sqliteSeed = `
CREATE TABLE customers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  email VARCHAR(255) NOT NULL UNIQUE COLLATE NOCASE,
  name TEXT DEFAULT 'anon',
  CONSTRAINT email_has_at CHECK (email LIKE '%@%')
);
CREATE TABLE orders (
  id INTEGER PRIMARY KEY,
  customer_id INTEGER NOT NULL REFERENCES customers (id) ON DELETE CASCADE,
  total DECIMAL(10, 2) DEFAULT 0,
  placed_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  note BLOB
);
CREATE INDEX orders_customer ON orders (customer_id, total DESC);
CREATE UNIQUE INDEX orders_note ON orders (lower(note)) WHERE note IS NOT NULL;
CREATE TABLE audit (msg TEXT);
CREATE TRIGGER orders_audit AFTER INSERT ON orders FOR EACH ROW WHEN NEW.total > 0
BEGIN
  INSERT INTO audit (msg) VALUES ('order ' || NEW.id || ';');
END;
CREATE VIEW big_orders AS SELECT o.id, c.email FROM orders o JOIN customers c ON c.id = o.customer_id WHERE o.total > 10;
INSERT INTO customers (email, name) VALUES ('ada@example.com', 'Ada'), ('bob@example.com', NULL);
INSERT INTO orders (customer_id, total, placed_at, note) VALUES (1, 25.5, '2024-03-01 10:30:00', x'CAFE'), (2, 5, '2024-03-02', NULL);
`

func sqliteSetup(t *testing.T) (*core.Manager, string, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shop.db")
	saved, err := st.Save(model.Connection{Name: "lite", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: path, Create: true}})
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	if _, err := m.Connect(context.Background(), saved.ID, ""); err != nil {
		t.Fatal(err)
	}
	if p, _ := st.Get(saved.ID); p.SQLite.Create {
		t.Fatal("Create should be cleared once the file exists")
	}
	exec(t, m, saved.ID, sqliteSeed)
	return m, saved.ID, dir
}

// exec runs a script and fails on any statement error.
func exec(t *testing.T, m *core.Manager, id, script string) []model.StatementResult {
	t.Helper()
	ctx := context.Background()
	sid, err := m.OpenSession(ctx, id, "main", "")
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
	return res
}

func TestSQLiteMetadata(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	ctx := context.Background()
	ref := func(name string, k model.ObjectKind) model.ObjectRef {
		return model.ObjectRef{Database: "main", Name: name, Kind: k}
	}
	dbs, err := m.ListDatabases(ctx, id)
	if err != nil || !slices.Equal(dbs, []string{"main"}) {
		t.Fatalf("databases %v %v", dbs, err)
	}
	names := func(k model.ObjectKind) []string {
		objs, err := m.ListObjects(ctx, id, "main", "", k)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, o := range objs {
			out = append(out, o.Name)
		}
		return out
	}
	if got := names(model.KindTable); !slices.Equal(got, []string{"audit", "customers", "orders"}) {
		t.Fatalf("tables %v", got)
	}
	if got := names(model.KindView); !slices.Equal(got, []string{"big_orders"}) {
		t.Fatalf("views %v", got)
	}
	if got := names(model.KindIndex); !slices.Equal(got, []string{"orders_customer", "orders_note"}) {
		t.Fatalf("indexes %v", got)
	}
	if got := names(model.KindTrigger); !slices.Equal(got, []string{"orders_audit"}) {
		t.Fatalf("triggers %v", got)
	}

	td, err := m.TableDetail(ctx, id, ref("orders", model.KindTable))
	if err != nil {
		t.Fatal(err)
	}
	if len(td.Columns) != 5 || !td.Columns[0].PrimaryKey || !td.Columns[0].AutoIncrement || len(td.ForeignKeys) != 1 ||
		td.ForeignKeys[0].RefTable != "customers" || td.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("detail %+v", td)
	}
	cat, err := m.Catalog(ctx, id, "main")
	if err != nil || len(cat.Columns) != 3+5+1+2 || len(cat.FKs) != 1 {
		t.Fatalf("catalog cols=%d fks=%d %v", len(cat.Columns), len(cat.FKs), err)
	}

	// The designer reads what the pragmas don't report from the stored SQL.
	d, err := m.LoadDesign(ctx, id, ref("customers", model.KindTable))
	if err != nil {
		t.Fatal(err)
	}
	email := d.Columns[1]
	if !d.Columns[0].AutoIncrement || email.Type != "VARCHAR" || email.Length != "255" || email.Collation != "NOCASE" || email.Nullable ||
		len(d.Uniques) != 1 || len(d.Checks) != 1 || d.Checks[0].Name != "email_has_at" || *d.Columns[2].Default != "'anon'" {
		t.Fatalf("customers design %+v", d)
	}
	o, err := m.LoadDesign(ctx, id, ref("orders", model.KindTable))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Indexes) != 2 || o.Indexes[0].Columns[1].Name != "total" || !o.Indexes[0].Columns[1].Desc ||
		o.Indexes[1].Kind != "unique" || o.Indexes[1].Columns[0].Expr != "lower(note)" || o.Indexes[1].Where != "note IS NOT NULL" {
		t.Fatalf("indexes %+v", o.Indexes)
	}
	if len(o.Triggers) != 1 || o.Triggers[0].Timing != "AFTER" || o.Triggers[0].Events[0] != "INSERT" || o.Triggers[0].When != "NEW.total > 0" ||
		!strings.HasPrefix(o.Triggers[0].Body, "BEGIN") {
		t.Fatalf("triggers %+v", o.Triggers)
	}
	if len(o.ForeignKeys) != 1 || o.ForeignKeys[0].RefColumns[0] != "id" {
		t.Fatalf("fks %+v", o.ForeignKeys)
	}
	// Unchanged designs produce no statements.
	if p, err := m.PreviewDesign(id, &o, o); err != nil || len(p.Statements) != 0 {
		t.Fatalf("no-op alter %v %v", p.Statements, err)
	}

	v, err := m.LoadView(ctx, id, ref("big_orders", model.KindView))
	if err != nil || !strings.HasPrefix(v.Definition, "SELECT o.id") {
		t.Fatalf("view %+v %v", v, err)
	}

	// Values: blobs as hex. The driver parses DATE/DATETIME/TIMESTAMP columns,
	// so dates show in SQLite's datetime format.
	rows := queryAll(t, m, id, "main", "", "SELECT note, placed_at, total FROM orders ORDER BY id")
	if *rows[0][0] != "0xCAFE" || *rows[0][1] != "2024-03-01 10:30:00" || *rows[1][1] != "2024-03-02 00:00:00" || *rows[0][2] != "25.5" {
		t.Fatalf("values %v %v %v", deref(rows[0][0]), deref(rows[0][1]), deref(rows[1][1]))
	}
}

func TestSQLiteDesignRebuild(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	ctx := context.Background()
	ref := model.ObjectRef{Database: "main", Name: "orders", Kind: model.KindTable}
	old, err := m.LoadDesign(ctx, id, ref)
	if err != nil {
		t.Fatal(err)
	}

	// Simple changes use ALTER TABLE.
	nw := old
	nw.Columns = slices.Clone(old.Columns)
	nw.Columns[4].Name = "memo"
	nw.Columns = append(nw.Columns, design.Column{Name: "status", Type: "TEXT", Nullable: true})
	p, err := m.PreviewDesign(id, &old, nw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(p.Statements, ";"), design.RebuildPrefix) || len(p.Statements) != 2 {
		t.Fatalf("expected ALTERs: %v", p.Statements)
	}
	res, err := m.ApplyDesign(ctx, id, &old, nw)
	if err != nil || res.Error != "" {
		t.Fatalf("apply %v %s", err, res.Error)
	}

	// Changing a type, dropping a column and reordering rebuild the table.
	old = *res.Table
	nw = old
	nw.Columns = slices.Clone(old.Columns)
	nw.Columns[2].Type, nw.Columns[2].Length, nw.Columns[2].Scale = "REAL", "", ""
	nw.Columns = slices.Delete(nw.Columns, 3, 4) // placed_at
	nw.Columns[0], nw.Columns[1] = nw.Columns[1], nw.Columns[0]
	p, err = m.PreviewDesign(id, &old, nw)
	if err != nil || !strings.Contains(strings.Join(p.Statements, ";"), design.RebuildPrefix) || !p.Transactional {
		t.Fatalf("expected a rebuild: %v %v", p.Statements, err)
	}
	t.Logf("rebuild:\n%s", strings.Join(p.Statements, ";\n"))
	res, err = m.ApplyDesign(ctx, id, &old, nw)
	if err != nil || res.Error != "" {
		t.Fatalf("rebuild %v %s (%s)", err, res.Error, res.FailedStatement)
	}
	got := res.Table
	if got.Columns[0].Name != "customer_id" || got.Columns[2].Type != "REAL" || len(got.Columns) != 5 || len(got.Indexes) != 2 || len(got.Triggers) != 1 {
		t.Fatalf("rebuilt %+v", got)
	}
	rows := queryAll(t, m, id, "main", "", "SELECT id, customer_id, total, memo FROM orders ORDER BY id")
	if len(rows) != 2 || *rows[0][2] != "25.5" || *rows[0][3] != "0xCAFE" {
		t.Fatalf("rows after rebuild %v", rows)
	}
	// The view still resolves, and the trigger was re-created.
	if v := queryAll(t, m, id, "main", "", "SELECT email FROM big_orders"); len(v) != 1 {
		t.Fatalf("view after rebuild %v", v)
	}
	exec(t, m, id, "INSERT INTO orders (customer_id, total) VALUES (1, 3)")
	// Two seed orders fired it, plus this one.
	if a := queryAll(t, m, id, "main", "", "SELECT count(*) FROM audit"); *a[0][0] != "3" {
		t.Fatalf("trigger after rebuild: %v", deref(a[0][0]))
	}
	// Foreign keys stay enforced on other sessions, and the parent's rebuild kept the children.
	cust, _ := m.LoadDesign(ctx, id, model.ObjectRef{Database: "main", Name: "customers", Kind: model.KindTable})
	cn := cust
	cn.Columns = slices.Clone(cust.Columns)
	cn.Columns[2].Type = "VARCHAR"
	cn.Columns[2].Length = "100"
	if r, err := m.ApplyDesign(ctx, id, &cust, cn); err != nil || r.Error != "" {
		t.Fatalf("parent rebuild %v %s", err, r.Error)
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM orders"); *n[0][0] != "3" {
		t.Fatalf("children lost: %s", deref(n[0][0]))
	}

	// A rebuild that would orphan rows is rolled back: customer_id becomes a new
	// column whose default matches no customer.
	ord, _ := m.LoadDesign(ctx, id, ref)
	on := ord
	on.Columns = slices.Clone(ord.Columns)
	def := "99"
	on.Columns[0] = design.Column{Name: "customer_id", Type: "INTEGER", Default: &def}
	if r, _ := m.ApplyDesign(ctx, id, &ord, on); r.Error == "" || !strings.Contains(r.Error, "foreign keys") {
		t.Fatalf("expected foreign key failure, got %q", r.Error)
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM orders WHERE customer_id IN (1, 2)"); *n[0][0] != "3" {
		t.Fatalf("rollback failed: %s", deref(n[0][0]))
	}

	// Duplicate and copy rename the indexes and triggers.
	if r := m.RunTableAction(ctx, id, ref, core.ActionCopy, "orders2"); r.Error != "" {
		t.Fatal(r.Error)
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM orders2"); *n[0][0] != "3" {
		t.Fatalf("copy rows %s", deref(n[0][0]))
	}
	if a := queryAll(t, m, id, "main", "", "SELECT count(*) FROM audit"); *a[0][0] != "3" {
		t.Fatalf("copy fired the trigger: %v", deref(a[0][0]))
	}
	if r := m.RunTableAction(ctx, id, ref, core.ActionTruncate, ""); r.Error != "" {
		t.Fatal(r.Error)
	}
}

func TestSQLiteAttachEditDump(t *testing.T) {
	m, id, dir := sqliteSetup(t)
	ctx := context.Background()

	// A session opened before attaching sees the new database.
	sid, err := m.OpenSession(ctx, id, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if r := m.ApplyDatabase(ctx, id, nil, design.Database{Name: "archive", File: filepath.Join(dir, "archive.db"), Create: true}); r.Error != "" {
		t.Fatal(r.Error)
	}
	res, err := m.Execute(ctx, sid, "CREATE TABLE archive.old_orders (id INTEGER PRIMARY KEY, total REAL); INSERT INTO archive.old_orders SELECT id, total FROM orders", core.ExecOptions{})
	if err != nil || res[0].Error != "" || res[1].Error != "" {
		t.Fatalf("attached use %+v %v", res, err)
	}
	m.CloseSession(sid)
	if dbs, _ := m.ListDatabases(ctx, id); !slices.Equal(dbs, []string{"main", "archive"}) {
		t.Fatalf("databases %v", dbs)
	}
	if objs, _ := m.ListObjects(ctx, id, "archive", "", model.KindTable); len(objs) != 1 || objs[0].Rows != 2 {
		t.Fatalf("archive tables %+v", objs)
	}
	if p, _ := m.Store.Get(id); len(p.SQLite.Attached) != 1 {
		t.Fatalf("attachment not saved: %+v", p.SQLite)
	}
	// Reconnecting attaches it again.
	m.Disconnect(id)
	if _, err := m.Connect(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM archive.old_orders"); *n[0][0] != "2" {
		t.Fatalf("after reconnect %v", deref(n[0][0]))
	}

	// Grid edits: insert with defaults, update by key.
	ref := model.ObjectRef{Database: "main", Name: "customers", Kind: model.KindTable}
	one := "1"
	newName := "Ada L."
	email := "eve@example.com"
	out, err := m.ApplyChanges(ctx, id, ref, []core.RowChange{
		{Kind: core.ChangeUpdate, Key: map[string]*string{"id": &one}, Values: map[string]*string{"name": &newName}},
		{Kind: core.ChangeInsert, Values: map[string]*string{"email": &email}},
	})
	if err != nil || out[0].Error != "" || out[1].Error != "" || out[0].RowsAffected != 1 {
		t.Fatalf("edits %+v %v", out, err)
	}

	// Dump and restore into a new file.
	dump := filepath.Join(dir, "dump.sql")
	waitOK(t, m, func() (core.Job, error) {
		return m.Dump(id, core.DumpRequest{Database: "main", Data: true, Path: dump})
	})
	b, _ := os.ReadFile(dump)
	if !strings.Contains(string(b), "X'CAFE'") || !strings.Contains(string(b), "CREATE TRIGGER orders_audit") {
		t.Fatalf("dump:\n%s", b)
	}
	if r := m.DropDatabase(ctx, id, "archive"); r.Error != "" {
		t.Fatal(r.Error)
	}
	st2, _ := store.Open(filepath.Join(dir, "cfg2"), &store.MemorySecrets{})
	c2, _ := st2.Save(model.Connection{Name: "restore", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, "restore.db"), Create: true}})
	m2 := core.NewManager(st2, nil, core.DefaultSettings)
	defer m2.Close()
	if _, err := m2.Connect(ctx, c2.ID, ""); err != nil {
		t.Fatal(err)
	}
	waitOK(t, m2, func() (core.Job, error) {
		return m2.ExecuteFile(c2.ID, core.ExecuteFileRequest{Path: dump, Database: "main"})
	})
	if n := queryAll(t, m2, c2.ID, "main", "", "SELECT count(*), sum(total) FROM orders"); *n[0][0] != "2" {
		t.Fatalf("restored %v", deref(n[0][0]))
	}
	if n := queryAll(t, m2, c2.ID, "main", "", "SELECT name FROM customers WHERE id = 1"); *n[0][0] != "Ada L." {
		t.Fatalf("restored edit %v", deref(n[0][0]))
	}
}

// The sample database in testdata loads, and every table round-trips through the designer.
func TestSQLiteSampleSeed(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "cfg"), &store.MemorySecrets{})
	saved, _ := st.Save(model.Connection{Name: "sample", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, "shop.db"), Create: true}})
	m := core.NewManager(st, nil, core.DefaultSettings)
	defer m.Close()
	ctx := context.Background()
	if _, err := m.Connect(ctx, saved.ID, ""); err != nil {
		t.Fatal(err)
	}
	seed, err := os.ReadFile("../../testdata/seed_sqlite.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(t, m, saved.ID, string(seed))
	tables, _ := m.ListObjects(ctx, saved.ID, "main", "", model.KindTable)
	if len(tables) != 5 {
		t.Fatalf("tables %+v", tables)
	}
	for _, tb := range tables {
		d, err := m.LoadDesign(ctx, saved.ID, tb.ObjectRef)
		if err != nil {
			t.Fatalf("%s: %v", tb.Name, err)
		}
		if p, err := m.PreviewDesign(saved.ID, &d, d); err != nil || len(p.Statements) != 0 {
			t.Fatalf("%s: no-op alter %v %v", tb.Name, p.Statements, err)
		}
		// Recreating from the design gives the same table.
		stmts, err := m.PreviewDesign(saved.ID, nil, d)
		if err != nil || len(stmts.Statements) == 0 {
			t.Fatalf("%s: create %v", tb.Name, err)
		}
	}
	items, _ := m.LoadDesign(ctx, saved.ID, model.ObjectRef{Database: "main", Name: "order_items", Kind: model.KindTable})
	if !items.Options.WithoutRowid || len(items.PrimaryKey.Columns) != 2 || len(items.ForeignKeys) != 2 {
		t.Fatalf("order_items %+v", items)
	}
	orders, _ := m.LoadDesign(ctx, saved.ID, model.ObjectRef{Database: "main", Name: "orders", Kind: model.KindTable})
	if len(orders.Triggers) != 1 || orders.Triggers[0].Events[0] != "UPDATE OF status" || orders.Indexes[0].Where != "status <> 'shipped'" {
		t.Fatalf("orders %+v %+v", orders.Triggers, orders.Indexes)
	}
}

// Execute SQL File decodes the file and reports
// failures by line while it streams.
func TestSQLiteExecuteFile(t *testing.T) {
	m, id, dir := sqliteSetup(t)
	ctx := context.Background()
	path := filepath.Join(dir, "script.sql")
	// windows-1252: é is 0xE9.
	script := "INSERT INTO audit VALUES ('caf\xe9');\n\nINSERT INTO nowhere VALUES (1);\n" +
		strings.Repeat("INSERT INTO audit VALUES ('x;y');\n", 3000)
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := m.ExecuteFile(id, core.ExecuteFileRequest{Path: path, Database: "main", Encoding: "windows-1252", ContinueOnError: true})
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	done, err := m.WaitJob(wctx, j.ID)
	if err != nil || done.Message != "Executed 3002 statements, 1 failed" || !strings.Contains(strings.Join(done.Log, "\n"), "line 3:") {
		t.Fatalf("%v %s\n%s", err, done.Message, strings.Join(done.Log, "\n"))
	}
	if n := queryAll(t, m, id, "main", "", "SELECT count(*) FROM audit WHERE msg = 'café'"); *n[0][0] != "1" {
		t.Fatalf("decoded %v", deref(n[0][0]))
	}
}
