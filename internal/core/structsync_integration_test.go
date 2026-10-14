package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
)

func TestIntegrationStructureSync(t *testing.T) {
	m, sides, _ := transferRig(t)
	ctx := context.Background()
	for _, name := range []string{"mysql", "pg"} {
		t.Run(name, func(t *testing.T) {
			src := sides[name]
			dst := side{name, src.id, "sync_target", ""}
			run := func(s side, sql string) {
				t.Helper()
				sid, err := m.OpenSession(ctx, s.id, s.database, s.schema)
				if err != nil {
					t.Fatal(err)
				}
				defer m.CloseSession(sid)
				for _, st := range strings.Split(sql, ";\n") {
					res, err := m.Execute(ctx, sid, st, core.ExecOptions{})
					if err != nil || res[len(res)-1].Error != "" {
						t.Fatalf("%s: %v %+v", st, err, res)
					}
				}
			}
			// A target that has drifted: customers lost a column and gained one,
			// a legacy table, no products/orders/order_items and no view.
			if name == "mysql" {
				run(sides["mysql"], "DROP DATABASE IF EXISTS sync_target;\nCREATE DATABASE sync_target")
				t.Cleanup(func() { run(sides["mysql"], "DROP DATABASE IF EXISTS sync_target") })
				run(dst, "CREATE TABLE sync_target.customers (id INT AUTO_INCREMENT PRIMARY KEY, email VARCHAR(100) NOT NULL, nickname VARCHAR(20))"+
					";\nCREATE TABLE sync_target.legacy (id INT PRIMARY KEY)")
			} else {
				dst = side{name, src.id, src.database, "sync_target"}
				run(src, "DROP SCHEMA IF EXISTS sync_target CASCADE;\nCREATE SCHEMA sync_target")
				t.Cleanup(func() { run(src, "DROP SCHEMA IF EXISTS sync_target CASCADE") })
				run(dst, "CREATE TABLE sync_target.customers (id serial PRIMARY KEY, email varchar(100) NOT NULL, nickname varchar(20))"+
					";\nCREATE TABLE sync_target.legacy (id int PRIMARY KEY)")
			}
			req := core.StructureCompareRequest{
				Source:  core.Endpoint{ConnID: src.id, Database: src.database, Schema: src.schema},
				Target:  core.Endpoint{ConnID: dst.id, Database: dst.database, Schema: dst.schema},
				Options: core.StructureOptions{CompareTables: true, CompareViews: true},
			}
			cmp, err := m.CompareStructure(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			ops := map[string]string{}
			for _, it := range cmp.Items {
				ops[it.Kind+":"+it.Name] = it.Op
			}
			want := map[string]string{"table:customers": "modify", "table:products": "create", "table:orders": "create", "table:order_items": "create", "table:legacy": "drop", "view:customer_orders": "create"}
			for k, v := range want {
				if ops[k] != v {
					t.Errorf("%s: %q, want %q (all: %v)", k, ops[k], v, ops)
				}
			}
			script := core.StructureScript(cmp.Items)
			job, err := m.Deploy(core.DeployRequest{Target: req.Target, Statements: script})
			if err != nil {
				t.Fatal(err)
			}
			wctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			done, _ := m.WaitJob(wctx, job.ID)
			if done.Status != core.JobDone {
				t.Fatalf("deploy: %s %s\n%s\n--- script\n%s", done.Status, done.Message, strings.Join(done.Log, "\n"), strings.Join(script, ";\n"))
			}
			// Now they match.
			again, err := m.CompareStructure(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, it := range again.Items {
				if it.Op != "same" {
					t.Errorf("after deploying, %s %s is still %s: %v", it.Kind, it.Name, it.Op, it.Statements)
				}
			}
			t.Logf("%s: %d statements; %s", name, len(script), done.Message)
		})
	}
}
