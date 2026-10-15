package mcpserver_test

// Run with CHLOE_IT=1 against the Docker databases (see docker-compose.yml).

import (
	"fmt"
	"os"
	"testing"

	_ "chloe/internal/driver/mysql"
	_ "chloe/internal/driver/postgres"
	"chloe/internal/model"
	"chloe/internal/store"
)

func TestIntegrationEngines(t *testing.T) {
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	for _, e := range []struct {
		conn             model.Connection
		database, schema string
		seedDB           string // where the seed runs (the test database doesn't exist yet)
		seed             string
	}{
		{model.Connection{Name: "my", Type: model.MySQL, Host: "127.0.0.1", Port: 33306, User: "root", Password: "chloe", SavePassword: true}, "chloe_mcp_it", "", "shop",
			"CREATE DATABASE IF NOT EXISTS chloe_mcp_it; USE chloe_mcp_it; DROP TABLE IF EXISTS mcp_it; CREATE TABLE mcp_it (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50), qty INT, ok BOOLEAN DEFAULT FALSE);" +
				"INSERT INTO mcp_it (name, qty) VALUES ('a', 1), ('b', 2), ('c', 3);"},
		{model.Connection{Name: "pg", Type: model.PostgreSQL, Host: "127.0.0.1", Port: 55432, User: "postgres", Password: "chloe", SavePassword: true, InitialDatabase: "postgres"}, "postgres", "chloe_mcp_it", "postgres",
			"CREATE SCHEMA IF NOT EXISTS chloe_mcp_it; DROP TABLE IF EXISTS chloe_mcp_it.mcp_it; CREATE TABLE chloe_mcp_it.mcp_it (id SERIAL PRIMARY KEY, name VARCHAR(50), qty INT, ok BOOLEAN DEFAULT FALSE);" +
				"INSERT INTO chloe_mcp_it.mcp_it (name, qty) VALUES ('a', 1), ('b', 2), ('c', 3);"},
	} {
		t.Run(e.conn.Name, func(t *testing.T) {
			f := setupConn(t, e.conn, e.seedDB, e.schema, e.seed, store.MCPConnAccess{Level: store.MCPWrite})
			// Its own database/schema, so tests of other packages running alongside don't see it.
			drop := "DROP DATABASE chloe_mcp_it"
			if e.schema != "" {
				drop = "DROP SCHEMA chloe_mcp_it CASCADE"
			}
			t.Cleanup(func() { f.exec(t, e.seedDB, "", drop) })
			at := func(extra map[string]any) map[string]any {
				args := map[string]any{"connection": e.conn.Name, "database": e.database, "table": "mcp_it"}
				if e.schema != "" {
					args["schema"] = e.schema
				}
				for k, v := range extra {
					args[k] = v
				}
				return args
			}
			out := f.call(t, "select_rows", at(map[string]any{"where": []any{map[string]any{"column": "qty", "op": ">", "value": 1}}, "order_by": []any{map[string]any{"column": "id"}}}), "")
			if r := rows(out); len(r) != 2 || r[0][1] != "b" {
				t.Fatalf("select: %v", out)
			}
			out = f.call(t, "insert_rows", at(map[string]any{"rows": []any{map[string]any{"name": "d", "qty": 4, "ok": true}}}), "")
			if fmt.Sprint(out["keys"]) != "[map[id:4]]" {
				t.Fatalf("insert: %v", out)
			}
			out = f.call(t, "update_rows", at(map[string]any{"where": []any{map[string]any{"column": "name", "op": "like", "value": "%"}, map[string]any{"column": "qty", "op": "<", "value": 3}}, "set": map[string]any{"qty": 10}}), "")
			if out["rows"] != 2.0 {
				t.Fatalf("update: %v", out)
			}
			out = f.call(t, "delete_rows", at(map[string]any{"where": []any{map[string]any{"column": "ok", "op": "=", "value": true}}}), "")
			if out["rows"] != 1.0 {
				t.Fatalf("delete: %v", out)
			}
			q := map[string]any{"connection": e.conn.Name, "database": e.database, "schema": e.schema, "sql": "SELECT sum(qty) AS total FROM mcp_it"}
			if e.schema == "" {
				delete(q, "schema")
			}
			out = f.call(t, "run_query", q, "")
			if r := rows(out); r[0][0] != "23" {
				t.Fatalf("sum: %v", out)
			}
			if e.conn.Type == model.PostgreSQL {
				q["sql"] = "WITH d AS (DELETE FROM mcp_it RETURNING *) SELECT count(*) FROM d"
				f.call(t, "run_query", q, "read-only transaction")
				schemas := f.call(t, "list_schemas", map[string]any{"connection": "pg", "database": "postgres"}, "")
				if fmt.Sprint(schemas["names"]) == "[]" {
					t.Fatal("no schemas")
				}
			} else {
				q["sql"] = "SELECT * FROM mcp_it FOR UPDATE"
				f.call(t, "run_query", q, "READ ONLY transaction")
				q["sql"] = "SHOW TABLES"
				f.call(t, "run_query", q, "")
			}
			args := at(nil)
			delete(args, "table")
			tables := f.call(t, "list_tables", args, "")
			if len(tables["tables"].([]any)) == 0 {
				t.Fatal("no tables")
			}

			// The agent leaves the password out; the user types it in the dialog.
			st := f.srv.Settings()
			st.Endpoints[0].Capabilities.CreateConnections = true
			st.Endpoints[0].Groups = map[string]store.MCPLevel{"Agents": store.MCPRead}
			st.Endpoints[0].ConnectionGroup = "Agents"
			if err := f.srv.Save(st); err != nil {
				t.Fatal(err)
			}
			args = map[string]any{"name": "made", "engine": string(e.conn.Type), "host": "127.0.0.1", "port": e.conn.Port, "user": e.conn.User, "database": e.seedDB}
			f.appr.password = "wrong"
			f.call(t, "create_connection", args, "wasn't saved")
			f.appr.password = "chloe"
			if out := f.call(t, "create_connection", args, ""); out["access"] != "read" {
				t.Fatalf("created: %v", out)
			}
			if req := f.appr.asked[len(f.appr.asked)-1]; !req.AskPassword || req.HasPassword {
				t.Fatalf("request: %+v", req)
			}
			f.call(t, "list_databases", map[string]any{"connection": "made"}, "")
		})
	}
}
