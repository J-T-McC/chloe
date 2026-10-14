package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"chloe/internal/datamodel"
	"chloe/internal/design"
	"chloe/internal/model"
)

func TestIntegrationDataModel(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			pg := e.conn.Type == model.PostgreSQL

			mdl, err := m.ReverseModel(ctx, id, e.database, e.schema, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(mdl.Tables) != 4 || mdl.Dialect != map[bool]string{true: "postgres", false: "mysql"}[pg] {
				t.Fatalf("reverse: %d tables, dialect %s", len(mdl.Tables), mdl.Dialect)
			}
			// File round trip.
			data, _ := mdl.Marshal()
			if mdl2, err := datamodel.Parse(data); err != nil || len(mdl2.Tables) != 4 {
				t.Fatalf("parse: %v", err)
			}

			// Forward engineer into a fresh database.
			target := "chloe_model"
			admin := e.database
			m.ExecuteDDL(ctx, id, admin, "", []string{"DROP DATABASE IF EXISTS " + target}, false)
			if r := m.ExecuteDDL(ctx, id, admin, "", []string{"CREATE DATABASE " + target}, false); r.Error != "" {
				t.Fatal(r.Error)
			}
			t.Cleanup(func() {
				if pg {
					m.ExecuteDDL(ctx, id, admin, "", []string{"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '" + target + "' AND pid <> pg_backend_pid()"}, false)
				}
				m.ExecuteDDL(ctx, id, admin, "", []string{"DROP DATABASE IF EXISTS " + target}, false)
			})
			to := datamodel.Target{Database: target, Schema: e.schema}
			if pg {
				// Types used by the tables (the enum) have to exist first.
				if r := m.ExecuteDDL(ctx, id, target, "", []string{"CREATE SCHEMA shop", "CREATE TYPE shop.order_status AS ENUM ('new','paid','shipped')"}, true); r.Error != "" {
					t.Fatal(r.Error)
				}
			}
			sql, err := mdl.GenerateSQL(to)
			if err != nil {
				t.Fatal(err)
			}
			if r := m.ExecuteDDL(ctx, id, target, e.schema, sql, pg); r.Error != "" {
				t.Fatalf("generated SQL failed: %s\n%s", r.Error, r.FailedStatement)
			}

			// Synchronizing the same model is a no-op.
			plan, err := m.PlanModelSync(ctx, id, mdl, to, false)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Actions) != 0 {
				t.Fatalf("expected no changes, got:\n%s", strings.Join(plan.Statements(), ";\n"))
			}

			// Edit the model and synchronize.
			ci := slices.IndexFunc(mdl.Tables, func(x design.Table) bool { return x.Name == "customers" })
			cust := &mdl.Tables[ci]
			cust.Columns = append(cust.Columns, design.Column{Name: "phone", Type: "varchar", Length: "40", Nullable: true})
			cust.Columns = slices.DeleteFunc(cust.Columns, func(c design.Column) bool { return c.Name == "avatar" })
			notes := design.Table{Schema: mdl.Schema, Name: "notes",
				Columns:     []design.Column{{Name: "id", Type: "int"}, {Name: "customer_id", Type: "int"}, {Name: "body", Type: "text", Nullable: true}},
				PrimaryKey:  &design.PrimaryKey{Columns: []string{"id"}},
				ForeignKeys: []design.ForeignKey{{Name: "notes_customer_fk", Columns: []string{"customer_id"}, RefTable: "customers", RefColumns: []string{"id"}, OnDelete: "CASCADE"}},
			}
			if !pg {
				notes.Schema = ""
			}
			mdl.Tables = append(mdl.Tables, notes)
			plan, err = m.PlanModelSync(ctx, id, mdl, to, false)
			if err != nil {
				t.Fatal(err)
			}
			kinds := map[string]string{}
			for _, a := range plan.Actions {
				kinds[a.Table] = a.Kind
			}
			if kinds["notes"] != "create" || kinds["customers"] != "alter" || len(plan.Unchanged) != 3 {
				t.Fatalf("plan %+v unchanged %v", kinds, plan.Unchanged)
			}
			t.Logf("sync:\n%s", strings.Join(plan.Statements(), ";\n"))
			if r := m.ApplyModelSync(ctx, id, to, plan); r.Error != "" {
				t.Fatalf("apply: %s\n%s", r.Error, r.FailedStatement)
			}
			plan, _ = m.PlanModelSync(ctx, id, mdl, to, false)
			if len(plan.Actions) != 0 {
				t.Fatalf("not converged:\n%s", strings.Join(plan.Statements(), ";\n"))
			}

			// Tables missing from the model are only dropped when asked.
			mdl.Tables = slices.DeleteFunc(mdl.Tables, func(x design.Table) bool { return x.Name == "notes" })
			plan, _ = m.PlanModelSync(ctx, id, mdl, to, true)
			if len(plan.Actions) != 1 || plan.Actions[0].Kind != "drop" || plan.Actions[0].Table != "notes" {
				t.Fatalf("drop plan %+v", plan.Actions)
			}
			if r := m.ApplyModelSync(ctx, id, to, plan); r.Error != "" {
				t.Fatal(r.Error)
			}
		})
	}
}
