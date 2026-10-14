package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/model"
	"chloe/internal/store"
)

// side is a database (and schema) on one of the test connections.
type side struct {
	name     string // mysql, pg, sqlite
	id       string
	database string
	schema   string
}

// transferRig connects to the Docker MySQL and PostgreSQL and to a copy of testdata/shop.db.
func transferRig(t *testing.T) (*core.Manager, map[string]side, string) {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../testdata/shop.db")
	if err != nil {
		t.Fatal(err)
	}
	lite := filepath.Join(dir, "shop.db")
	os.WriteFile(lite, data, 0o600)
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	sides := map[string]side{}
	add := func(name string, c model.Connection, db, schema string) {
		saved, err := st.Save(c)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if _, err := m.Connect(ctx, saved.ID, ""); err != nil {
			t.Fatal(err)
		}
		sides[name] = side{name, saved.ID, db, schema}
	}
	for _, e := range engines() {
		add(e.conn.Name, e.conn, e.database, e.schema)
	}
	add("sqlite", model.Connection{Name: "sqlite", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: lite}}, "main", "")
	return m, sides, dir
}

func count(t *testing.T, m *core.Manager, s side, table string) string {
	t.Helper()
	q := "SELECT COUNT(*) FROM " + table
	if s.name == "mysql" {
		q = "SELECT COUNT(*) FROM `" + s.database + "`.`" + table + "`"
	} else if s.name == "pg" {
		q = `SELECT COUNT(*) FROM "` + s.schema + `"."` + table + `"`
	}
	return *queryAll(t, m, s.id, s.database, s.schema, q)[0][0]
}

func TestIntegrationDataTransfer(t *testing.T) {
	m, sides, dir := transferRig(t)
	ctx := context.Background()
	tables := []string{"customers", "products", "orders", "order_items"}

	// Fresh, empty targets on each engine.
	exec := func(s side, sql string) {
		t.Helper()
		sid, err := m.OpenSession(ctx, s.id, s.database, s.schema)
		if err != nil {
			t.Fatal(err)
		}
		defer m.CloseSession(sid)
		res, err := m.Execute(ctx, sid, sql, core.ExecOptions{})
		if err != nil || res[len(res)-1].Error != "" {
			t.Fatalf("%s: %v %+v", sql, err, res)
		}
	}
	target := func(src, dst string) side {
		s := sides[dst]
		name := "xfer_from_" + src
		switch dst {
		case "mysql":
			exec(s, "DROP DATABASE IF EXISTS "+name)
			t.Cleanup(func() { exec(s, "DROP DATABASE IF EXISTS "+name) })
			return side{dst, s.id, name, ""}
		case "pg":
			exec(s, "DROP SCHEMA IF EXISTS "+name+" CASCADE")
			t.Cleanup(func() { exec(s, "DROP SCHEMA IF EXISTS "+name+" CASCADE") })
			return side{dst, s.id, s.database, name}
		default:
			// a new SQLite file per source
			st := m.Store
			c, err := st.Save(model.Connection{Name: name, Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, name+".db"), Create: true}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Connect(ctx, c.ID, ""); err != nil {
				t.Fatal(err)
			}
			return side{dst, c.ID, "main", ""}
		}
	}

	for _, from := range []string{"mysql", "pg", "sqlite"} {
		for _, to := range []string{"mysql", "pg", "sqlite"} {
			t.Run(from+"_to_"+to, func(t *testing.T) {
				src := sides[from]
				dst := target(from, to)
				o := core.DefaultTransferOptions()
				o.CreateTargetDB = true
				req := core.DataTransferRequest{Source: core.Endpoint{ConnID: src.id, Database: src.database, Schema: src.schema},
					Target: core.Endpoint{ConnID: dst.id, Database: dst.database, Schema: dst.schema}, Tables: tables, AllViews: true, Options: o}
				job, err := m.DataTransfer(req)
				if err != nil {
					t.Fatal(err)
				}
				wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				defer cancel()
				done, err := m.WaitJob(wctx, job.ID)
				if err != nil {
					t.Fatal(err)
				}
				log := strings.Join(done.Log, "\n")
				if done.Status != core.JobDone {
					t.Fatalf("%s %s\n%s", done.Status, done.Message, log)
				}
				// Views only go between the same engine; between engines they're skipped, not failed.
				if done.Errors > 0 {
					t.Fatalf("%d errors: %s\n%s", done.Errors, done.Message, log)
				}
				for _, tb := range tables {
					if a, b := count(t, m, src, tb), count(t, m, dst, tb); a != b {
						t.Errorf("%s: %s rows in the source, %s in the target\n%s", tb, a, b, log)
					}
				}
				// Values survive: text, decimals, binary, nulls.
				q := func(s side, sql string) string {
					rows := queryAll(t, m, s.id, s.database, s.schema, sql)
					var out []string
					for _, r := range rows {
						for _, c := range r {
							if c == nil {
								out = append(out, "NULL")
							} else {
								out = append(out, *c)
							}
						}
					}
					return strings.Join(out, "|")
				}
				names := func(s side) string { return q(s, "SELECT full_name FROM customers ORDER BY id") }
				if a, b := names(src), names(dst); a != b {
					t.Errorf("customer names %q vs %q", a, b)
				}
				// Compared as numbers: SQLite keeps NUMERIC as a number (19.50 reads back as 19.5).
				prices := func(s side) string {
					var out []string
					for _, v := range strings.Split(q(s, "SELECT price FROM products ORDER BY id"), "|") {
						f, _ := strconv.ParseFloat(v, 64)
						out = append(out, strconv.FormatFloat(f, 'f', 2, 64))
					}
					return strings.Join(out, "|")
				}
				if a, b := prices(src), prices(dst); a != b {
					t.Errorf("prices %q vs %q", a, b)
				}
				avatar := func(s side) string {
					v := strings.ToUpper(strings.TrimPrefix(strings.TrimPrefix(q(s, "SELECT avatar FROM customers WHERE id = 1"), `\x`), "0x"))
					return v
				}
				if a, b := avatar(src), avatar(dst); a != b {
					t.Errorf("avatar %q vs %q", a, b)
				}
				// The target's ids continue after the copied ones.
				exec(dst, "INSERT INTO customers (email, full_name) VALUES ('new@example.com', 'New Person')")
				// (Same-engine MySQL also carries the source's AUTO_INCREMENT counter, which may be higher.)
				if id, _ := strconv.Atoi(q(dst, "SELECT id FROM customers WHERE email = 'new@example.com'")); id < 4 {
					t.Errorf("new id %d reuses a copied id", id)
				}
				// Foreign keys came across and are enforced.
				sid, _ := m.OpenSession(ctx, dst.id, dst.database, dst.schema)
				if to == "sqlite" {
					m.Execute(ctx, sid, "PRAGMA foreign_keys = ON", core.ExecOptions{})
				}
				res, _ := m.Execute(ctx, sid, "INSERT INTO orders (customer_id, status) VALUES (999, 'new')", core.ExecOptions{})
				m.CloseSession(sid)
				if res[0].Error == "" {
					t.Errorf("an order for a missing customer was accepted: foreign key missing\n%s", log)
				}
				if from == to {
					if v := q(dst, "SELECT COUNT(*) FROM customer_orders"); v == "" || v == "0" {
						t.Errorf("view not copied\n%s", log)
					}
				} else if !strings.Contains(log, "Skipped: views") {
					t.Errorf("cross-engine views should be skipped with a note\n%s", log)
				}
				t.Logf("%s → %s: %s", from, to, done.Message)
			})
		}
	}
}
