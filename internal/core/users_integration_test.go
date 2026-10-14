package core_test

import (
	"context"
	"strings"
	"testing"

	"chloe/internal/model"
	"chloe/internal/privs"
)

func TestIntegrationUsers(t *testing.T) {
	for _, e := range engines() {
		t.Run(e.conn.Name, func(t *testing.T) {
			m, id := setup(t, e)
			ctx := context.Background()
			pg := e.conn.Type == model.PostgreSQL
			host := "%"
			if pg {
				host = ""
			}
			info := func(n string) privs.UserInfo { return privs.UserInfo{Name: n, Host: host} }
			cleanup := func() {
				for _, n := range []string{"chloe_u", "chloe_u2", "chloe_r"} {
					if pg {
						// Grants have to go before a role can be dropped.
						m.ExecuteDDL(ctx, id, e.database, "", []string{"DROP OWNED BY " + n}, false)
					}
					m.DropUsers(ctx, id, []privs.UserInfo{info(n)})
				}
			}
			cleanup()
			t.Cleanup(cleanup)

			if r := m.ApplyUser(ctx, id, nil, privs.User{Name: "chloe_r", Host: host, ConnLimit: -1, Locked: !pg}); r.Error != "" {
				t.Fatalf("create role: %s\n%s", r.Error, r.FailedStatement)
			}
			pw := "Secr3t!pw"
			role := "chloe_r"
			if !pg {
				role = "chloe_r@%"
			}
			u := privs.User{Name: "chloe_u", Host: host, Password: &pw, ConnLimit: -1, CanLogin: true, Inherit: true, MaxQueries: 500, MemberOf: []string{role}}
			db, schema := "shop", ""
			if pg {
				db, schema = e.database, "shop"
				u.Comment = "integration test"
				u.Privileges = []privs.Grant{
					{Level: privs.Database, Database: db, Privilege: "CONNECT"},
					{Level: privs.Schema, Database: db, Schema: schema, Privilege: "USAGE"},
					{Level: privs.Table, Database: db, Schema: schema, Object: "orders", Privilege: "SELECT", Grantable: true},
					{Level: privs.Column, Database: db, Schema: schema, Object: "customers", Column: "email", Privilege: "UPDATE"},
					{Level: privs.Sequence, Database: db, Schema: schema, Object: "orders_id_seq", Privilege: "USAGE"},
					{Level: privs.Function, Database: db, Schema: schema, Object: "order_total(oid bigint)", Privilege: "EXECUTE"},
				}
			} else {
				u.Privileges = []privs.Grant{
					{Level: privs.Global, Privilege: "PROCESS"},
					{Level: privs.Database, Database: db, Privilege: "SELECT"},
					{Level: privs.Table, Database: db, Object: "orders", Privilege: "DELETE", Grantable: true},
					{Level: privs.Table, Database: db, Object: "orders", Privilege: "INSERT", Grantable: true},
					{Level: privs.Column, Database: db, Object: "customers", Column: "email", Privilege: "UPDATE"},
					{Level: privs.Function, Database: db, Object: "add_order", RoutineType: "PROCEDURE", Privilege: "EXECUTE"},
				}
			}
			// save applies, reloads and checks the stored account matches the design.
			save := func(old *privs.User, want privs.User) privs.User {
				t.Helper()
				if r := m.ApplyUser(ctx, id, old, want); r.Error != "" {
					t.Fatalf("apply: %s\n%s", r.Error, r.FailedStatement)
				}
				got, err := m.LoadUser(ctx, id, want.Name, want.Host, db)
				if err != nil {
					t.Fatal(err)
				}
				want.Password = nil
				st, err := m.UserSQL(id, &got, want)
				if err != nil || len(st) != 0 {
					t.Fatalf("reloaded user differs: %v %v", err, st)
				}
				return got
			}
			got := save(nil, u)
			if len(got.Privileges) != len(u.Privileges) {
				t.Fatalf("privileges %+v", got.Privileges)
			}
			og, err := m.ObjectGrants(ctx, id, privs.Grant{Level: privs.Table, Database: db, Schema: schema, Object: "orders"})
			if err != nil {
				t.Fatal(err)
			}
			mine := 0
			for _, g := range og {
				if g.User == "chloe_u" {
					mine++
				}
			}
			if (pg && mine != 1) || (!pg && mine != 2) {
				t.Fatalf("object grants %+v", og)
			}
			users, err := m.ListUsers(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, x := range users {
				found = found || x.Name == "chloe_u"
			}
			if !found {
				t.Fatal("new user not listed")
			}

			// Rename, change limits and privileges.
			next := got
			next.Name = "chloe_u2"
			next.MemberOf = nil
			next.Privileges = []privs.Grant{got.Privileges[0], got.Privileges[2]}
			next.Privileges[1].Grantable = false
			if pg {
				next.CreateDB, next.ConnLimit = true, 5
			} else {
				next.MaxQueries, next.Locked = 0, true
			}
			got = save(&got, next)
			if len(got.Privileges) != 2 || len(got.MemberOf) != 0 {
				t.Fatalf("after alter %+v", got)
			}
			st, _ := m.UserSQL(id, nil, got)
			if !strings.Contains(st[0].SQL, "chloe_u2") {
				t.Fatalf("sql %v", st)
			}
			if r := m.DropUsers(ctx, id, []privs.UserInfo{info("chloe_u2")}); r.Error != "" && !pg {
				t.Fatal(r.Error)
			}
		})
	}
}
