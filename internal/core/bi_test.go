package core_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"chloe/internal/bi"
	"chloe/internal/core"
	"chloe/internal/model"
	"chloe/internal/store"
)

// lastingSecrets behaves like the keychain: files are encrypted under its key.
type lastingSecrets struct{ store.MemorySecrets }

func (*lastingSecrets) Ephemeral() bool { return false }

func biSetup(t *testing.T) (*core.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cfg"), &lastingSecrets{})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := st.Save(model.Connection{Name: "lite", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, "shop.db"), Create: true}})
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	if _, err := m.Connect(context.Background(), saved.ID, ""); err != nil {
		t.Fatal(err)
	}
	exec(t, m, saved.ID, sqliteSeed+`
INSERT INTO orders (customer_id, total, placed_at) VALUES (1, 100, '2024-04-10 08:00:00'), (2, 50, '2024-04-11'), (1, 7.25, '2024-05-01');`)
	return m, saved.ID
}

func TestBIChartsOnSQLite(t *testing.T) {
	m, id := biSetup(t)
	ctx := context.Background()
	w := bi.New("Sales")
	src := bi.DataSource{Meta: bi.Meta{ID: "orders", Name: "Orders"}, ConnID: id, Database: "main", Table: "orders", Mode: bi.ModeLive}
	fields, err := m.BIFields(ctx, bi.Workspace{}, src)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, f := range fields {
		types[f.Name] = f.Type
	}
	if types["placed_at"] != bi.TypeDateTime || types["total"] != bi.TypeNumber || types["note"] != bi.TypeString {
		t.Fatalf("fields: %+v", fields)
	}
	src.Fields = fields
	w.DataSources = append(w.DataSources, src)

	chart := bi.Chart{Meta: bi.Meta{Name: "Revenue"}, DataSource: "orders", Type: "line",
		Dimension: []bi.ChartField{{Field: "placed_at", Aggregate: "month"}},
		Value:     []bi.ChartField{{Field: "total", Aggregate: "sum"}, {Field: "id", Aggregate: "count"}}}
	data, err := m.BIChartData(ctx, w, chart, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"2024-03", 30.5, 2.0}, {"2024-04", 150.0, 2.0}, {"2024-05", 7.25, 1.0}}
	if !data.Complete || !reflect.DeepEqual(data.Rows, want) || data.Mode != bi.ModeLive {
		t.Fatalf("live chart: %+v", data)
	}

	// Filters and a query source.
	q := bi.DataSource{Meta: bi.Meta{ID: "q", Name: "Big"}, ConnID: id, Database: "main", SQL: "SELECT customer_id, total FROM orders WHERE total > 6;", Mode: bi.ModeLive,
		Filters: []bi.Filter{{Field: "customer_id", Op: "=", Values: []string{"1"}}}, Sorts: []bi.Sort{{Field: "total", Desc: true}}}
	if q.Fields, err = m.BIFields(ctx, bi.Workspace{}, q); err != nil {
		t.Fatal(err)
	}
	prev, err := m.BIPreview(ctx, w, q, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(prev.Rows) != 3 || *prev.Rows[0][1] != "100" || *prev.Rows[2][1] != "7.25" {
		t.Fatalf("preview: %+v", prev)
	}
	for _, bad := range []string{"UPDATE orders SET total = 0", "SELECT 1; DELETE FROM orders", "PRAGMA table_info(orders)"} {
		q.SQL = bad
		if _, err := m.BIFields(ctx, bi.Workspace{}, q); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}

	// An incomplete chart isn't an error.
	if d, err := m.BIChartData(ctx, w, bi.Chart{DataSource: "orders", Type: "bar"}, nil); err != nil || d.Complete {
		t.Errorf("incomplete: %+v %v", d, err)
	}
}

func TestBIArchive(t *testing.T) {
	m, id := biSetup(t)
	ctx := context.Background()
	lib := m.Store.BI()
	p, err := lib.Create(bi.New("Archive"))
	if err != nil {
		t.Fatal(err)
	}
	w, _ := bi.Read(p)
	src := bi.DataSource{Meta: bi.Meta{ID: "c", Name: "Customers"}, ConnID: id, Database: "main",
		SQL: "SELECT c.email, o.total, o.placed_at FROM customers c JOIN orders o ON o.customer_id = c.id", Mode: bi.ModeArchive}
	src, err = m.BIArchive(ctx, w, src)
	if err != nil {
		t.Fatal(err)
	}
	if src.ArchiveRows != 5 || src.RefreshedAt == "" || len(src.Fields) != 3 {
		t.Fatalf("archive: %+v", src)
	}
	w.DataSources = append(w.DataSources, src)
	sealed, err := os.ReadFile(lib.ArchivePath(w.ID, "c"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("ada@example.com")) || bytes.Contains(sealed, []byte("SQLite format")) {
		t.Error("the archive is stored in the clear")
	}
	chart := bi.Chart{DataSource: "c", Type: "bar", Dimension: []bi.ChartField{{Field: "email"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}}
	data, err := m.BIChartData(ctx, w, chart, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]any{{"ada@example.com", 132.75}, {"bob@example.com", 55.0}}
	if !reflect.DeepEqual(data.Rows, want) || data.Mode != bi.ModeArchive || data.RanAt != src.RefreshedAt {
		t.Fatalf("archive chart: %+v", data)
	}
	// Dates were normalised, so buckets work on the snapshot.
	chart.Dimension = []bi.ChartField{{Field: "placed_at", Aggregate: "quarter"}}
	if data, err = m.BIChartData(ctx, w, chart, nil); err != nil || !reflect.DeepEqual(data.Rows, [][]any{{"2024-Q1", 30.5}, {"2024-Q2", 157.25}}) {
		t.Fatalf("archive buckets: %+v %v", data.Rows, err)
	}
	// Live changes don't reach the snapshot until it's refreshed.
	exec(t, m, id, "UPDATE orders SET total = 0")
	if data, _ = m.BIChartData(ctx, w, chart, nil); data.Rows[0][1] != 30.5 {
		t.Errorf("snapshot changed: %+v", data.Rows)
	}
	if src, err = m.BIArchive(ctx, w, src); err != nil {
		t.Fatal(err)
	}
	if data, _ = m.BIChartData(ctx, w, chart, nil); data.Rows[0][1] != 0.0 {
		t.Errorf("refresh: %+v", data.Rows)
	}
	prev, err := m.BIPreview(ctx, w, src, 2)
	if err != nil || len(prev.Rows) != 2 || !strings.Contains(prev.SQL, `"data"`) {
		t.Errorf("archive preview: %+v %v", prev, err)
	}
}

func TestBIJoinedTables(t *testing.T) {
	m, id := biSetup(t)
	ctx := context.Background()
	p, err := m.Store.BI().Create(bi.New("joins"))
	if err != nil {
		t.Fatal(err)
	}
	src, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Orders"}, ConnID: id, Database: "main", Table: "orders", Mode: bi.ModeLive,
		Joins: []bi.Join{{Table: "customers", Type: "inner", On: []bi.JoinOn{{Left: "customer_id", Right: "id"}}}},
		// A custom field can use a joined column.
		CustomFields: []bi.CustomField{{Name: "who", Kind: bi.CustomCalculated, Expression: "COALESCE([customers.name], [customers.email])"}}})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range src.Fields {
		names[f.Name] = true
	}
	if !names["total"] || !names["customers.email"] || !names["customers.id"] || len(src.Joins[0].Columns) != 3 {
		t.Fatalf("fields: %+v", src.Fields)
	}
	w, _ := bi.Read(p)
	chart := bi.Chart{DataSource: src.ID, Type: "bar", Dimension: []bi.ChartField{{Field: "who"}}, Value: []bi.ChartField{{Field: "total", Aggregate: "sum"}}}
	data, err := m.BIChartData(ctx, w, chart, nil)
	if want := [][]any{{"Ada", 132.75}, {"bob@example.com", 55.0}}; err != nil || !reflect.DeepEqual(data.Rows, want) {
		t.Fatalf("joined: %v %v\n%s", data.Rows, err, data.SQL)
	}
	// Archive mode reads the joined rows into its snapshot.
	src.Mode = bi.ModeArchive
	if src, err = m.BISaveSource(ctx, p, src); err != nil {
		t.Fatal(err)
	}
	w, _ = bi.Read(p)
	if data, err = m.BIChartData(ctx, w, chart, nil); err != nil || len(data.Rows) != 2 {
		t.Fatalf("archived: %v %v", data.Rows, err)
	}
	for _, bad := range []bi.Join{
		{Table: "nope", Type: "left", On: []bi.JoinOn{{Left: "customer_id", Right: "id"}}},
		{Table: "customers", Type: "cross", On: []bi.JoinOn{{Left: "customer_id", Right: "id"}}},
		{Table: "customers", Type: "left", On: []bi.JoinOn{{Left: "customer_id", Right: "missing"}}},
		{Table: "customers", Type: "left"},
	} {
		if _, err := m.BISaveSource(ctx, p, bi.DataSource{Meta: bi.Meta{Name: "Bad"}, ConnID: id, Database: "main", Table: "orders", Mode: bi.ModeLive, Joins: []bi.Join{bad}}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}
