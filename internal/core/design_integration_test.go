package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/model"
)

func sp(s string) *string { return &s }

// designFixture is a table using most designer features, for each engine.
func designFixture(e engine) design.Table {
	pg := e.conn.Type == model.PostgreSQL
	t := design.Table{
		Database: e.database, Schema: e.schema, Name: "dt_items", Comment: "designer test",
		Columns: []design.Column{
			{Name: "id", Type: "int", AutoIncrement: !pg},
			{Name: "sku", Type: "varchar", Length: "32"},
			{Name: "title", Type: "varchar", Length: "200", Nullable: true, Comment: "display title"},
			{Name: "price", Type: "decimal", Length: "10", Scale: "2", Default: sp("0.00")},
			{Name: "customer_id", Type: "int", Nullable: true},
			{Name: "created_at", Type: "timestamp", Default: sp("CURRENT_TIMESTAMP")},
		},
		PrimaryKey: &design.PrimaryKey{Columns: []string{"id"}},
		Indexes: []design.Index{
			{Name: "dt_items_sku", Kind: "unique", Columns: []design.IndexColumn{{Name: "sku"}}},
			{Name: "dt_items_title_price", Columns: []design.IndexColumn{{Name: "title"}, {Name: "price", Desc: true}}},
		},
		ForeignKeys: []design.ForeignKey{{Name: "dt_items_customer", Columns: []string{"customer_id"}, RefTable: "customers", RefColumns: []string{"id"}, OnDelete: "SET NULL"}},
		Checks:      []design.Check{{Name: "dt_items_price_ck", Expr: "price >= 0"}},
	}
	if pg {
		t.Columns[0].Identity = "by default"
		t.Columns[5].Type = "timestamptz"
		t.Columns = append(t.Columns, design.Column{Name: "tags", Type: "text[]", Nullable: true})
		t.Indexes = append(t.Indexes, design.Index{Name: "dt_items_lower_sku", Columns: []design.IndexColumn{{Expr: "lower((sku)::text)"}}, Where: "(price > (0)::numeric)"})
		t.PrimaryKey.Name = "dt_items_pkey"
	} else {
		t.Columns[2].Collation = "utf8mb4_bin"
		t.Columns[5].OnUpdateNow = true
		t.Options = design.Options{Engine: "InnoDB"}
		t.Triggers = []design.Trigger{{Name: "dt_items_upper", Timing: "BEFORE", Events: []string{"INSERT"}, Body: "SET NEW.sku = UPPER(NEW.sku)"}}
	}
	return t
}

func TestIntegrationDesigner(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			pg := e.conn.Type == model.PostgreSQL
			ref := model.ObjectRef{Database: e.database, Schema: e.schema, Name: "dt_items", Kind: model.KindTable}
			cleanup := func() {
				m.RunTableAction(ctx, id, model.ObjectRef{Database: e.database, Schema: e.schema, Name: "dt_items2", Kind: model.KindTable}, core.ActionDrop, "")
				m.RunTableAction(ctx, id, ref, core.ActionDrop, "")
			}
			cleanup()
			t.Cleanup(cleanup)

			if pg {
				res := m.ExecuteDDL(ctx, id, e.database, e.schema, []string{
					"CREATE OR REPLACE FUNCTION shop.dt_touch() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN NEW.sku := upper(NEW.sku); RETURN NEW; END $$",
				}, true)
				if res.Error != "" {
					t.Fatal(res.Error)
				}
			}

			// 1. Create.
			fixture := designFixture(e)
			if pg {
				fixture.Triggers = []design.Trigger{{Name: "dt_items_upper", Timing: "BEFORE", Events: []string{"INSERT", "UPDATE"}, ForEachRow: true, Function: "shop.dt_touch()"}}
			}
			res, err := m.ApplyDesign(ctx, id, nil, fixture)
			if err != nil || res.Error != "" {
				t.Fatalf("create: %v %s\n%s", err, res.Error, res.FailedStatement)
			}
			loaded := *res.Table

			// 2. The loaded definition reflects what was designed.
			names := func(cs []design.Column) []string {
				var out []string
				for _, c := range cs {
					out = append(out, c.Name)
				}
				return out
			}
			wantCols := []string{"id", "sku", "title", "price", "customer_id", "created_at"}
			if pg {
				wantCols = append(wantCols, "tags")
			}
			if !slices.Equal(names(loaded.Columns), wantCols) {
				t.Fatalf("columns %v", names(loaded.Columns))
			}
			if loaded.Comment != "designer test" || loaded.Columns[2].Comment != "display title" || !loaded.Columns[2].Nullable || loaded.Columns[1].Nullable {
				t.Fatalf("column details %+v", loaded.Columns[:3])
			}
			if loaded.Columns[1].Length != "32" || loaded.Columns[3].Scale != "2" {
				t.Fatalf("type modifiers %+v %+v", loaded.Columns[1], loaded.Columns[3])
			}
			if pg && (loaded.Columns[0].Identity != "by default" || loaded.Columns[6].Type != "text[]") {
				t.Fatalf("pg column types %+v %+v", loaded.Columns[0], loaded.Columns[6])
			}
			if !pg && (!loaded.Columns[0].AutoIncrement || !loaded.Columns[5].OnUpdateNow || loaded.Columns[2].Collation != "utf8mb4_bin") {
				t.Fatalf("mysql column options %+v", loaded.Columns)
			}
			if loaded.PrimaryKey == nil || !slices.Equal(loaded.PrimaryKey.Columns, []string{"id"}) {
				t.Fatalf("pk %+v", loaded.PrimaryKey)
			}
			if len(loaded.ForeignKeys) != 1 || loaded.ForeignKeys[0].OnDelete != "SET NULL" || len(loaded.Checks) != 1 || len(loaded.Triggers) != 1 {
				t.Fatalf("fk/check/trigger %+v %+v %+v", loaded.ForeignKeys, loaded.Checks, loaded.Triggers)
			}
			// PG has the expression index; MySQL adds an index backing the foreign key.
			wantIdx := 3
			if len(loaded.Indexes) != wantIdx {
				t.Fatalf("indexes %+v", loaded.Indexes)
			}

			// 3. Loading and saving without edits changes nothing.
			assertNoop(t, m, id, loaded)

			// Some data, to prove alters keep it.
			ins := m.ExecuteDDL(ctx, id, e.database, e.schema, []string{"INSERT INTO dt_items (sku, title, price) VALUES ('ab-1', 'Widget', 9.5)"}, false)
			if ins.Error != "" {
				t.Fatal(ins.Error)
			}

			// 4. Edit: rename + widen a column, drop one, add one, change an index,
			// change the check, rename the table, composite primary key.
			edit := cloneTable(loaded)
			edit.Name = "dt_items2"
			edit.Comment = "renamed"
			edit.Columns[1].Name = "code"
			edit.Columns[1].Length = "64"
			edit.Columns[3].Default = sp("1.00")
			edit.Columns = slices.DeleteFunc(edit.Columns, func(c design.Column) bool { return c.Name == "customer_id" })
			edit.ForeignKeys = nil
			edit.Indexes = slices.DeleteFunc(edit.Indexes, func(ix design.Index) bool { return ix.Name == "dt_items_customer" })
			edit.Columns = append(edit.Columns, design.Column{Name: "qty", Type: "int", Default: sp("1")})
			edit.Indexes[indexOf(edit.Indexes, "dt_items_sku")].Columns = []design.IndexColumn{{Name: "code"}}
			for i := range edit.Indexes {
				if edit.Indexes[i].Name == "dt_items_title_price" {
					edit.Indexes[i].Name = "dt_items_title"
					edit.Indexes[i].Columns = edit.Indexes[i].Columns[:1]
				}
			}
			edit.Checks[0].Expr = "price >= 1"
			edit.PrimaryKey.Columns = []string{"id", "code"}
			if pg {
				edit.Indexes = slices.DeleteFunc(edit.Indexes, func(ix design.Index) bool { return ix.Name == "dt_items_lower_sku" })
			} else {
				// Move "title" first.
				ti := slices.IndexFunc(edit.Columns, func(c design.Column) bool { return c.Name == "title" })
				col := edit.Columns[ti]
				edit.Columns = slices.Insert(slices.Delete(edit.Columns, ti, ti+1), 0, col)
			}
			preview, err := m.PreviewDesign(id, &loaded, edit)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("ALTER:\n%s", strings.Join(preview.Statements, ";\n"))
			res, err = m.ApplyDesign(ctx, id, &loaded, edit)
			if err != nil || res.Error != "" {
				t.Fatalf("alter: %v %s\n%s", err, res.Error, res.FailedStatement)
			}
			after := *res.Table
			if after.Name != "dt_items2" || after.Comment != "renamed" {
				t.Fatalf("table rename/comment %q %q", after.Name, after.Comment)
			}
			wantCols = []string{"id", "code", "title", "price", "created_at", "qty"}
			if pg {
				wantCols = []string{"id", "code", "title", "price", "created_at", "tags", "qty"}
			} else {
				wantCols = []string{"title", "id", "code", "price", "created_at", "qty"}
			}
			if !slices.Equal(names(after.Columns), wantCols) {
				t.Fatalf("columns after alter %v", names(after.Columns))
			}
			if !slices.Equal(after.PrimaryKey.Columns, []string{"id", "code"}) || len(after.ForeignKeys) != 0 {
				t.Fatalf("pk/fk after alter %+v %+v", after.PrimaryKey, after.ForeignKeys)
			}
			if !strings.Contains(after.Checks[0].Expr, "1") {
				t.Fatalf("check %+v", after.Checks)
			}
			assertNoop(t, m, id, after)

			// The row survived the column rename.
			sid, _ := m.OpenSession(ctx, id, e.database, e.schema)
			rows, _ := m.Execute(ctx, sid, "SELECT code, qty FROM dt_items2", core.ExecOptions{})
			page, _ := m.FetchRows(rows[0].ResultID, 0, 5)
			if len(page.Rows) != 1 || *page.Rows[0][0] != "AB-1" || *page.Rows[0][1] != "1" {
				t.Fatalf("data after alter %+v (trigger should have upper-cased the sku)", page.Rows)
			}
			m.CloseSession(sid)

			// 5. Table actions.
			for _, a := range []core.TableAction{core.ActionEmpty, core.ActionTruncate} {
				if r := m.RunTableAction(ctx, id, model.ObjectRef{Database: e.database, Schema: e.schema, Name: "dt_items2", Kind: model.KindTable}, a, ""); r.Error != "" {
					t.Fatalf("%s: %s", a, r.Error)
				}
			}
		})
	}
}

func assertNoop(t *testing.T, m *core.Manager, id string, tb design.Table) {
	t.Helper()
	p, err := m.PreviewDesign(id, &tb, cloneTable(tb))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Statements) != 0 {
		t.Fatalf("reloaded table is not stable; would run:\n%s", strings.Join(p.Statements, ";\n"))
	}
}

func cloneTable(t design.Table) design.Table {
	c := t
	c.Columns = slices.Clone(t.Columns)
	c.Indexes = slices.Clone(t.Indexes)
	for i := range c.Indexes {
		c.Indexes[i].Columns = slices.Clone(t.Indexes[i].Columns)
	}
	c.ForeignKeys = slices.Clone(t.ForeignKeys)
	c.Checks = slices.Clone(t.Checks)
	c.Uniques = slices.Clone(t.Uniques)
	c.Triggers = slices.Clone(t.Triggers)
	if t.PrimaryKey != nil {
		pk := *t.PrimaryKey
		pk.Columns = slices.Clone(pk.Columns)
		c.PrimaryKey = &pk
	}
	return c
}

func indexOf(ix []design.Index, name string) int {
	return slices.IndexFunc(ix, func(i design.Index) bool { return i.Name == name })
}

func TestIntegrationDatabaseLifecycle(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			pg := e.conn.Type == model.PostgreSQL
			m.DropDatabase(ctx, id, "chloe_newdb")
			m.DropDatabase(ctx, id, "chloe_newdb2")
			meta, err := m.DatabaseMeta(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			d := design.Database{Name: "chloe_newdb", Charset: "latin1", Collation: "latin1_swedish_ci"}
			if pg {
				if len(meta.Owners) == 0 || len(meta.Templates) == 0 {
					t.Fatalf("meta %+v", meta)
				}
				d = design.Database{Name: "chloe_newdb", Owner: "postgres", Template: "template0", Encoding: "LATIN1", Collation: "C", Ctype: "C", Comment: "made by chloe"}
			} else if len(meta.Charsets) == 0 || meta.DefaultCharset == "" {
				t.Fatalf("meta %+v", meta)
			}
			if r := m.ApplyDatabase(ctx, id, nil, d); r.Error != "" {
				t.Fatalf("create: %s\n%s", r.Error, r.FailedStatement)
			}
			got, err := m.LoadDatabase(ctx, id, "chloe_newdb")
			if err != nil {
				t.Fatal(err)
			}
			if (!pg && got.Charset != "latin1") || (pg && (got.Encoding != "LATIN1" || got.Comment != "made by chloe")) {
				t.Fatalf("loaded %+v", got)
			}
			// Open a session in it first: rename and drop must cope with our own pool.
			if pg {
				if _, err := m.ListSchemas(ctx, id, "chloe_newdb"); err != nil {
					t.Fatal(err)
				}
			}
			next := got
			if pg {
				next.Name = "chloe_newdb2"
				next.Comment = ""
			} else {
				next.Charset, next.Collation = "utf8mb4", "utf8mb4_bin"
			}
			if r := m.ApplyDatabase(ctx, id, &got, next); r.Error != "" {
				t.Fatalf("alter: %s\n%s", r.Error, r.FailedStatement)
			}
			if r := m.DropDatabase(ctx, id, next.Name); r.Error != "" {
				t.Fatalf("drop: %s", r.Error)
			}
		})
	}
}
