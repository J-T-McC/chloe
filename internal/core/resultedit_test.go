package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"chloe/internal/core"
)

func TestResultEditTarget(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	exec(t, m, id, `CREATE TABLE "Mixed Case" ("Id" INTEGER PRIMARY KEY, v TEXT); INSERT INTO "Mixed Case" VALUES (1, 'x');`)
	ctx := context.Background()
	sid, err := m.OpenSession(ctx, id, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseSession(sid)

	for _, tc := range []struct {
		sql     string
		columns []string // nil when read-only
		reason  string
	}{
		{sql: "SELECT * FROM customers WHERE id = 1", columns: []string{"id", "email", "name"}},
		{sql: "select c.* from customers c order by id limit 5;", columns: []string{"id", "email", "name"}},
		{sql: "SELECT id, name AS who, upper(email) e FROM main.customers", columns: []string{"id", "name", ""}},
		{sql: "SELECT ID, Email FROM CUSTOMERS -- trailing comment", columns: []string{"id", "email"}},
		{sql: `SELECT "Id", v FROM "Mixed Case"`, columns: []string{"Id", "v"}},
		{sql: "SELECT id, 1 + 1 AS two, 'from' AS f FROM orders WHERE note IS NULL", columns: []string{"id", "", ""}},
		{sql: "SELECT id FROM orders WHERE customer_id IN (SELECT id FROM customers UNION SELECT 1)", columns: []string{"id"}},
		{sql: "SELECT name FROM customers", reason: "primary key column id"},
		{sql: "SELECT * FROM audit", reason: "no primary key"},
		{sql: "SELECT * FROM big_orders", reason: "no primary key"},
		{sql: "SELECT o.id, c.email FROM orders o JOIN customers c ON c.id = o.customer_id", reason: "joins"},
		{sql: "SELECT * FROM orders, customers", reason: "joins"},
		{sql: "SELECT customer_id, count(*) FROM orders GROUP BY customer_id", reason: "groups"},
		{sql: "SELECT DISTINCT id FROM customers", reason: "DISTINCT"},
		{sql: "SELECT id FROM customers UNION SELECT id FROM orders", reason: "combines"},
		{sql: "WITH x AS (SELECT * FROM customers) SELECT * FROM x", reason: "simple"},
		{sql: "SELECT 1", reason: "no table"},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			res, err := m.Execute(ctx, sid, tc.sql, core.ExecOptions{})
			if err != nil || res[0].Error != "" {
				t.Fatalf("%v %+v", err, res)
			}
			e, err := m.ResultEditTarget(ctx, res[0].ResultID)
			if err != nil {
				t.Fatal(err)
			}
			if tc.columns == nil {
				if e.Editable || !strings.Contains(e.Reason, tc.reason) {
					t.Fatalf("want read-only (%s), got %+v", tc.reason, e)
				}
				return
			}
			if !e.Editable || !slices.Equal(e.Columns, tc.columns) || e.Ref.Name == "" {
				t.Fatalf("want columns %q, got %+v", tc.columns, e)
			}
		})
	}
}

func TestPatchResult(t *testing.T) {
	m, id, _ := sqliteSetup(t)
	ctx := context.Background()
	sid, _ := m.OpenSession(ctx, id, "main", "")
	defer m.CloseSession(sid)
	res, err := m.Execute(ctx, sid, "SELECT id, name FROM customers ORDER BY id", core.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rid := res[0].ResultID
	e, _ := m.ResultEditTarget(ctx, rid)

	// Apply an edit to the table, then patch the buffer the same way the grid does.
	name := "Ada King"
	if _, err := m.ApplyChanges(ctx, id, e.Ref, []core.RowChange{{Kind: core.ChangeUpdate, Key: map[string]*string{"id": ptr("1")}, Values: map[string]*string{"name": &name}}}); err != nil {
		t.Fatal(err)
	}
	st, err := m.PatchResult(rid, core.ResultPatch{Updated: map[int]map[int]*string{0: {1: &name}}, Deleted: []int{1}, Inserted: [][]*string{{nil, ptr("new")}}})
	if err != nil || st.RowCount != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	page, _ := m.FetchRows(rid, 0, 10)
	got := []string{deref(page.Rows[0][1]), deref(page.Rows[1][1])}
	if !slices.Equal(got, []string{"Ada King", "new"}) {
		t.Fatalf("rows %q", got)
	}
	check, _ := m.Execute(ctx, sid, "SELECT name FROM customers WHERE id = 1", core.ExecOptions{})
	p2, _ := m.FetchRows(check[0].ResultID, 0, 1)
	if deref(p2.Rows[0][0]) != "Ada King" {
		t.Fatal("update not applied")
	}
}

func ptr(s string) *string { return &s }

// TestIntegrationResultEdit checks the mapping on MySQL (backticks, case-insensitive
// names, db.table) and PostgreSQL (folding, schema.table), then edits through it.
func TestIntegrationResultEdit(t *testing.T) {
	queries := map[string][]string{
		"mysql": {"SELECT ID, `full_name` AS who, upper(email) FROM customers WHERE id = 1", "select c.* from shop.customers c where c.id = 1"},
		"pg":    {`SELECT ID, "full_name" AS who, upper(email) FROM customers WHERE id = 1`, "select c.* from shop.customers c where c.id = 1"},
	}
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			sid, err := m.OpenSession(ctx, id, e.database, e.schema)
			if err != nil {
				t.Fatal(err)
			}
			defer m.CloseSession(sid)
			q := queries[e.conn.Name]
			res, err := m.Execute(ctx, sid, q[0], core.ExecOptions{})
			if err != nil || res[0].Error != "" {
				t.Fatalf("%v %+v", err, res)
			}
			target, err := m.ResultEditTarget(ctx, res[0].ResultID)
			if err != nil || !target.Editable || !slices.Equal(target.Columns, []string{"id", "full_name", ""}) {
				t.Fatalf("%+v %v", target, err)
			}
			res, _ = m.Execute(ctx, sid, q[1], core.ExecOptions{})
			target, err = m.ResultEditTarget(ctx, res[0].ResultID)
			if err != nil || !target.Editable || target.Columns[0] != "id" || len(target.Columns) != 5 {
				t.Fatalf("%+v %v", target, err)
			}
			// Edit through the mapping and restore.
			for _, name := range []string{"Ada Edited", "Ada Lovelace"} {
				v := name
				if _, err := m.ApplyChanges(ctx, id, target.Ref, []core.RowChange{{Kind: core.ChangeUpdate, Key: map[string]*string{"id": ptr("1")}, Values: map[string]*string{"full_name": &v}}}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
