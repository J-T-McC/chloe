package core_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/model"
)

func TestIntegrationDataSync(t *testing.T) {
	m, sides, dir := transferRig(t)
	ctx := context.Background()
	wait := func(job core.Job, err error) core.Job {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		done, err := m.WaitJob(wctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if done.Status != core.JobDone || done.Errors > 0 {
			t.Fatalf("%s: %s %s\n%s", done.Title, done.Status, done.Message, strings.Join(done.Log, "\n"))
		}
		return done
	}
	run := func(s side, sql string) {
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
	for _, name := range []string{"mysql", "pg", "sqlite"} {
		t.Run(name, func(t *testing.T) {
			src := sides[name]
			var dst side
			switch name {
			case "mysql":
				dst = side{name, src.id, "dsync_target", ""}
				t.Cleanup(func() { run(src, "DROP DATABASE IF EXISTS dsync_target") })
			case "pg":
				dst = side{name, src.id, src.database, "dsync_target"}
				t.Cleanup(func() { run(src, "DROP SCHEMA IF EXISTS dsync_target CASCADE") })
			default:
				c, err := m.Store.Save(model.Connection{Name: "dsync", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: filepath.Join(dir, "dsync.db"), Create: true}})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.Connect(ctx, c.ID, ""); err != nil {
					t.Fatal(err)
				}
				dst = side{name, c.ID, "main", ""}
			}
			// A copy of the source, then drift in the target.
			o := core.DefaultTransferOptions()
			o.CreateTargetDB = true
			srcEnd := core.Endpoint{ConnID: src.id, Database: src.database, Schema: src.schema}
			dstEnd := core.Endpoint{ConnID: dst.id, Database: dst.database, Schema: dst.schema}
			wait(m.DataTransfer(core.DataTransferRequest{Source: srcEnd, Target: dstEnd, AllTables: true, Options: o}))
			run(dst, "UPDATE customers SET full_name = 'Changed Name' WHERE id = 2")
			run(dst, "DELETE FROM order_items WHERE order_id = 2")
			run(dst, "INSERT INTO customers (id, email, full_name) VALUES (99, 'extra@example.com', 'Extra')")

			tables, err := m.DataSyncTables(ctx, srcEnd, dstEnd)
			if err != nil {
				t.Fatal(err)
			}
			for _, tb := range tables {
				if tb.Message != "" && tb.Source != "audit_log" {
					t.Errorf("%s: %s", tb.Source, tb.Message)
				}
			}
			cmp, err := m.CompareData(ctx, core.DataSyncRequest{Source: srcEnd, Target: dstEnd, Tables: tables})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][4]int{}
			for _, r := range cmp.Tables {
				got[r.Source] = [4]int{r.Insert, r.Update, r.Delete, r.Same}
			}
			if c := got["customers"]; c[0] != 0 || c[1] != 1 || c[2] != 1 {
				t.Errorf("customers insert/update/delete/same = %v, want 0/1/1", c)
			}
			if c := got["order_items"]; c[0] != 1 || c[1] != 0 || c[2] != 0 {
				t.Errorf("order_items = %v, want 1 insert", c)
			}
			if c := got["products"]; c[0]+c[1]+c[2] != 0 || c[3] != 2 {
				t.Errorf("products = %v, want 2 same", c)
			}
			page, err := m.DataSyncRecords(cmp.ID, "customers", "update", 0, 10)
			if err != nil || page.Total != 1 || *page.Records[0].Target[page.Records[0].Changed[0]] != "Changed Name" {
				t.Fatalf("records %+v %v", page, err)
			}

			var sel []core.DataSyncSelection
			for _, r := range cmp.Tables {
				sel = append(sel, core.DataSyncSelection{Table: r.Source, Insert: true, Update: true, Delete: true})
			}
			script, _ := m.DataSyncScript(cmp.ID, sel)
			t.Logf("%s script:\n%s", name, strings.Join(script, ";\n"))
			wait(m.DeployDataSync(core.DataSyncDeployRequest{ID: cmp.ID, Tables: sel, UseTransaction: true}))

			again, err := m.CompareData(ctx, core.DataSyncRequest{Source: srcEnd, Target: dstEnd, Tables: tables})
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range again.Tables {
				if r.Insert+r.Update+r.Delete != 0 {
					t.Errorf("after deploying, %s still differs: %+v", r.Source, r)
				}
			}
			m.DiscardDataSync(again.ID)
		})
	}
}
