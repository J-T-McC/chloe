package core_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/model"
	"chloe/internal/transfer"
)

func waitOK(t *testing.T, m *core.Manager, start func() (core.Job, error)) core.Job {
	t.Helper()
	j, err := start()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	done, err := m.WaitJob(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != core.JobDone || done.Errors > 0 {
		t.Fatalf("job %s: %s %s\n%s", done.Title, done.Status, done.Message, strings.Join(done.Log, "\n"))
	}
	return done
}

func queryAll(t *testing.T, m *core.Manager, id, db, schema, q string) [][]*string {
	t.Helper()
	ctx := context.Background()
	sid, err := m.OpenSession(ctx, id, db, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseSession(sid)
	res, err := m.Execute(ctx, sid, q, core.ExecOptions{})
	if err != nil || res[0].Error != "" {
		t.Fatalf("%s: %v %+v", q, err, res)
	}
	page, _ := m.FetchRows(res[0].ResultID, 0, 5000, 0)
	return page.Rows
}

func TestIntegrationExportImport(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			dir := t.TempDir()
			customers := model.ObjectRef{Database: e.database, Schema: e.schema, Name: "customers", Kind: model.KindTable}

			for _, f := range []transfer.Format{transfer.CSV, transfer.JSON, transfer.XLSX} {
				path := filepath.Join(dir, "customers."+f.Extension())
				waitOK(t, m, func() (core.Job, error) {
					return m.Export(id, core.ExportRequest{Source: core.ExportSource{Ref: &customers, Columns: []string{"id", "email", "full_name"}},
						Options: transfer.ExportOptions{Format: f, IncludeHeaders: true}, Path: path})
				})

				// Import into a new table built from the preview's inferred types.
				target := model.ObjectRef{Database: e.database, Schema: e.schema, Name: "imp_" + string(f), Kind: model.KindTable}
				m.RunTableAction(ctx, id, target, core.ActionDrop, "")
				t.Cleanup(func() { m.RunTableAction(ctx, id, target, core.ActionDrop, "") })
				opts := transfer.ImportOptions{Format: f, HeaderRow: 1, EmptyAsNull: true}
				prev, err := m.PreviewImport(id, path, opts)
				if err != nil || len(prev.Columns) != 3 || len(prev.Rows) != 3 {
					t.Fatalf("%s preview %+v %v", f, prev, err)
				}
				tbl := design.Table{Database: e.database, Schema: e.schema, Name: target.Name, PrimaryKey: &design.PrimaryKey{Columns: []string{"id"}}}
				var mapping []core.FieldMap
				for i, c := range prev.Columns {
					typ, length := prev.Types[i], ""
					if j := strings.Index(typ, "("); j > 0 {
						typ, length = typ[:j], strings.Trim(typ[j:], "()")
					}
					tbl.Columns = append(tbl.Columns, design.Column{Name: c, Type: typ, Length: length, Nullable: c != "id"})
					mapping = append(mapping, core.FieldMap{Source: c, Target: c})
				}
				waitOK(t, m, func() (core.Job, error) {
					return m.Import(ctx, id, core.ImportRequest{Path: path, Options: opts, Target: target, CreateTable: &tbl, Mapping: mapping, Mode: core.ImportAppend, Transaction: true})
				})
				got := queryAll(t, m, id, e.database, e.schema, fmt.Sprintf("SELECT id, email, full_name FROM %s ORDER BY id", target.Name))
				if len(got) != 3 || *got[0][1] != "ada@example.com" || *got[2][2] != "Grace Hopper" {
					t.Fatalf("%s round trip: %v", f, got)
				}
			}

			// Import modes against a keyed table.
			scratch := model.ObjectRef{Database: e.database, Schema: e.schema, Name: "imp_modes", Kind: model.KindTable}
			m.RunTableAction(ctx, id, scratch, core.ActionDrop, "")
			t.Cleanup(func() { m.RunTableAction(ctx, id, scratch, core.ActionDrop, "") })
			if r := m.ExecuteDDL(ctx, id, e.database, e.schema, []string{"CREATE TABLE imp_modes (k int PRIMARY KEY, v varchar(20), n int)",
				"INSERT INTO imp_modes VALUES (1, 'one', 10), (2, 'two', 20)"}, false); r.Error != "" {
				t.Fatal(r.Error)
			}
			csv := filepath.Join(dir, "modes.csv")
			os.WriteFile(csv, []byte("k,v,n\n2,TWO,22\n3,three,\n"), 0o600)
			mapping := []core.FieldMap{{Source: "k", Target: "k"}, {Source: "v", Target: "v"}, {Source: "n", Target: "n"}}
			run := func(mode core.ImportMode) string {
				waitOK(t, m, func() (core.Job, error) {
					return m.Import(ctx, id, core.ImportRequest{Path: csv, Options: transfer.ImportOptions{Format: transfer.CSV, HeaderRow: 1, EmptyAsNull: true},
						Target: scratch, Mapping: mapping, Mode: mode, Transaction: true})
				})
				var parts []string
				for _, r := range queryAll(t, m, id, e.database, e.schema, "SELECT k, v, n FROM imp_modes ORDER BY k") {
					n := "null"
					if r[2] != nil {
						n = *r[2]
					}
					parts = append(parts, *r[0]+":"+*r[1]+":"+n)
				}
				return strings.Join(parts, ",")
			}
			for _, tc := range []struct {
				mode core.ImportMode
				want string
			}{
				{core.ImportSkipExisting, "1:one:10,2:two:20,3:three:null"},
				{core.ImportUpsert, "1:one:10,2:TWO:22,3:three:null"},
				{core.ImportDelete, "1:one:10"},
				{core.ImportCopy, "2:TWO:22,3:three:null"},
				{core.ImportUpdate, "2:TWO:22,3:three:null"},
			} {
				if got := run(tc.mode); got != tc.want {
					t.Fatalf("%s: got %s want %s", tc.mode, got, tc.want)
				}
			}
			// Append over an existing key fails, and the transaction leaves the table as it was.
			j, err := m.Import(ctx, id, core.ImportRequest{Path: csv, Options: transfer.ImportOptions{Format: transfer.CSV, HeaderRow: 1, EmptyAsNull: true},
				Target: scratch, Mapping: mapping, Mode: core.ImportAppend, Transaction: true})
			if err != nil {
				t.Fatal(err)
			}
			done, _ := m.WaitJob(ctx, j.ID)
			if done.Status != core.JobFailed {
				t.Fatalf("append duplicate should fail: %+v", done)
			}
			if n := len(queryAll(t, m, id, e.database, e.schema, "SELECT k FROM imp_modes")); n != 2 {
				t.Fatalf("rollback left %d rows", n)
			}
		})
	}
}

func TestIntegrationDumpRestore(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			pg := e.conn.Type == model.PostgreSQL
			path := filepath.Join(t.TempDir(), "dump.sql")
			waitOK(t, m, func() (core.Job, error) {
				return m.Dump(id, core.DumpRequest{Database: e.database, Schema: e.schema, Data: true, Path: path})
			})
			script, _ := os.ReadFile(path)
			if !strings.Contains(string(script), "INSERT INTO") {
				t.Fatalf("dump has no data:\n%s", script)
			}

			// Restore into a fresh database.
			restore := "chloe_restore"
			admin := e.database
			drop := []string{"DROP DATABASE IF EXISTS " + restore}
			create := []string{"CREATE DATABASE " + restore}
			m.ExecuteDDL(ctx, id, admin, "", drop, false)
			if r := m.ExecuteDDL(ctx, id, admin, "", create, false); r.Error != "" {
				t.Fatal(r.Error)
			}
			t.Cleanup(func() {
				if pg {
					m.ExecuteDDL(ctx, id, admin, "", []string{"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '" + restore + "' AND pid <> pg_backend_pid()"}, false)
				}
				m.ExecuteDDL(ctx, id, admin, "", drop, false)
			})
			if j, err := m.ExecuteFile(id, core.ExecuteFileRequest{Path: path, Database: restore}); err != nil {
				t.Fatal(err)
			} else if done, _ := m.WaitJob(ctx, j.ID); done.Status != core.JobDone {
				numbered := strings.Split(string(script), "\n")
				for i := range numbered {
					numbered[i] = fmt.Sprintf("%4d  %s", i+1, numbered[i])
				}
				t.Fatalf("restore failed: %s\n%s", done.Message, strings.Join(numbered, "\n"))
			}

			// Same tables, same definitions, same row counts.
			tables, _ := m.ListObjects(ctx, id, e.database, e.schema, model.KindTable)
			for _, tb := range tables {
				orig, err := m.LoadDesign(ctx, id, model.ObjectRef{Database: e.database, Schema: e.schema, Name: tb.Name, Kind: model.KindTable})
				if err != nil {
					t.Fatal(err)
				}
				copyRef := model.ObjectRef{Database: restore, Schema: e.schema, Name: tb.Name, Kind: model.KindTable}
				restored, err := m.LoadDesign(ctx, id, copyRef)
				if err != nil {
					t.Fatalf("%s not restored: %v", tb.Name, err)
				}
				restored.Database = orig.Database
				gen := design.Generator(design.MySQL{})
				if pg {
					gen = design.Postgres{}
				}
				diff, err := gen.Alter(orig, restored)
				if err != nil {
					t.Fatal(err)
				}
				if len(diff) > 0 {
					t.Errorf("%s differs after restore:\n%s", tb.Name, strings.Join(diff, ";\n"))
				}
				q := fmt.Sprintf("SELECT count(*) FROM %s", tb.Name)
				a := queryAll(t, m, id, e.database, e.schema, q)
				b := queryAll(t, m, id, restore, e.schema, q)
				if *a[0][0] != *b[0][0] {
					t.Errorf("%s rows: %s vs %s", tb.Name, *a[0][0], *b[0][0])
				}
			}
			views, _ := m.ListObjects(ctx, id, restore, e.schema, model.KindView)
			if !slices.ContainsFunc(views, func(o model.ObjectInfo) bool { return o.Name == "customer_orders" }) {
				t.Errorf("view not restored: %+v", views)
			}
			if pg {
				// Sequences continue after the restored ids.
				r := queryAll(t, m, id, restore, e.schema, "INSERT INTO customers (email, full_name) VALUES ('new@x.io', 'New') RETURNING id")
				if *r[0][0] != "4" {
					t.Errorf("sequence not reset, new id %s", *r[0][0])
				}
			}
		})
	}
}

func TestIntegrationMaintain(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			refs := []model.ObjectRef{{Database: e.database, Schema: e.schema, Name: "customers", Kind: model.KindTable}, {Database: e.database, Schema: e.schema, Name: "orders", Kind: model.KindTable}}
			ops := []core.MaintainOp{core.MaintainAnalyze, core.MaintainCheck, core.MaintainOptimize}
			if e.conn.Type == model.PostgreSQL {
				ops = []core.MaintainOp{core.MaintainAnalyze, core.MaintainVacuum, core.MaintainVacuumAnalyz, core.MaintainReindex}
			}
			for _, op := range ops {
				res, err := m.Maintain(context.Background(), id, refs, op)
				if err != nil || len(res) < 2 {
					t.Fatalf("%s: %+v %v", op, res, err)
				}
				for _, r := range res {
					if r.Status == "error" || r.Status == "Error" {
						t.Fatalf("%s: %+v", op, r)
					}
				}
			}
		})
	}
}
