package mcpserver_test

// Run with CHLOE_IT=1 against the Docker databases (see docker-compose.yml).

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"

	_ "chloe/internal/driver/mysql"
	_ "chloe/internal/driver/postgres"
	"chloe/internal/model"
	"chloe/internal/store"
	"chloe/internal/testdb"
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
		{model.Connection{Name: "my", Type: model.MySQL, Host: "127.0.0.1", Port: testdb.MySQL, User: "root", Password: "chloe", SavePassword: true}, "chloe_mcp_it", "", "shop",
			"CREATE DATABASE IF NOT EXISTS chloe_mcp_it; USE chloe_mcp_it; DROP TABLE IF EXISTS mcp_it; CREATE TABLE mcp_it (id INT AUTO_INCREMENT PRIMARY KEY, name VARCHAR(50), qty INT, ok BOOLEAN DEFAULT FALSE);" +
				"INSERT INTO mcp_it (name, qty) VALUES ('a', 1), ('b', 2), ('c', 3);"},
		{model.Connection{Name: "pg", Type: model.PostgreSQL, Host: "127.0.0.1", Port: testdb.Postgres, User: "postgres", Password: "chloe", SavePassword: true, InitialDatabase: "postgres"}, "postgres", "chloe_mcp_it", "postgres",
			"CREATE SCHEMA IF NOT EXISTS chloe_mcp_it; DROP TABLE IF EXISTS chloe_mcp_it.mcp_it; CREATE TABLE chloe_mcp_it.mcp_it (id SERIAL PRIMARY KEY, name VARCHAR(50), qty INT, ok BOOLEAN DEFAULT FALSE, tags TEXT[]);" +
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
			// The plans the guide tells agents to read run through run_query.
			plans := []string{"EXPLAIN FORMAT=TREE SELECT * FROM mcp_it WHERE qty > 1", "EXPLAIN ANALYZE SELECT * FROM mcp_it WHERE qty > 1"}
			if e.schema != "" {
				plans = []string{"EXPLAIN SELECT * FROM mcp_it WHERE qty > 1", "EXPLAIN (ANALYZE, BUFFERS) SELECT * FROM mcp_it WHERE qty > 1"}
			}
			for _, plan := range plans {
				q["sql"] = plan
				if out := f.call(t, "run_query", q, ""); len(rows(out)) == 0 {
					t.Fatalf("%s: %v", plan, out)
				}
			}
			// Reads share a warm session instead of logging in each time.
			q["sql"] = "SELECT CONNECTION_ID()"
			if e.conn.Type == model.PostgreSQL {
				q["sql"] = "SELECT pg_backend_pid()"
			}
			if a, b := rows(f.call(t, "run_query", q, ""))[0][0], rows(f.call(t, "run_query", q, ""))[0][0]; a != b {
				t.Errorf("two reads, two sessions: %v %v", a, b)
			}
			// The server stops at the limit: ORDER BY and duplicate column names survive.
			q["limit"] = 5
			q["sql"] = "SELECT TABLE_NAME, TABLE_NAME FROM information_schema.COLUMNS ORDER BY TABLE_NAME DESC"
			if e.conn.Type == model.PostgreSQL {
				q["sql"] = "SELECT g, g FROM generate_series(1, 50) g ORDER BY g DESC"
			}
			out = f.call(t, "run_query", q, "")
			if r := rows(out); len(r) != 5 || out["truncated"] != true || len(r[0]) != 2 {
				t.Fatalf("limited read: %v", out)
			} else if e.conn.Type == model.PostgreSQL && r[0][0] != "50" {
				t.Fatalf("order lost: %v", r)
			}
			delete(q, "limit")
			// A write hidden inside a SELECT, through a function, is stopped by the
			// read-only transaction rather than the statement check.
			if e.conn.Type == model.PostgreSQL {
				f.exec(t, e.seedDB, "", "CREATE FUNCTION chloe_mcp_it.bump() RETURNS int AS $$ UPDATE chloe_mcp_it.mcp_it SET qty = qty + 1; SELECT 1 $$ LANGUAGE sql")
				q["sql"] = "SELECT chloe_mcp_it.bump()"
				f.call(t, "run_query", q, "read-only transaction")
				q["sql"] = "SELECT nextval('chloe_mcp_it.mcp_it_id_seq')"
				f.call(t, "run_query", q, "read-only transaction")
			} else {
				f.exec(t, e.database, "", "DELIMITER $$\nCREATE FUNCTION bump() RETURNS INT DETERMINISTIC MODIFIES SQL DATA BEGIN UPDATE mcp_it SET qty = qty + 1; RETURN 1; END$$")
				q["sql"] = "SELECT bump()"
				f.call(t, "run_query", q, "READ ONLY transaction")
			}
			q["sql"] = "SELECT sum(qty) AS total FROM mcp_it"
			if r := rows(f.call(t, "run_query", q, "")); r[0][0] != "23" {
				t.Fatalf("a read changed data: sum %v", r)
			}
			if e.conn.Type == model.PostgreSQL {
				// JSON lists become arrays.
				f.call(t, "update_rows", at(map[string]any{"where": []any{map[string]any{"column": "name", "op": "=", "value": "c"}},
					"set": map[string]any{"tags": []any{"x", `say "hi"`, nil}}}), "")
				q["sql"] = "SELECT tags[2], array_length(tags, 1), tags[3] IS NULL FROM mcp_it WHERE name = 'c'"
				if r := rows(f.call(t, "run_query", q, "")); fmt.Sprint(r[0]) != `[say "hi" 3 true]` {
					t.Fatalf("array: %v", r)
				}
				q["sql"] = "WITH d AS (DELETE FROM mcp_it RETURNING *) SELECT count(*) FROM d"
				f.call(t, "run_query", q, "read-only transaction")
				schemas := f.call(t, "list_schemas", map[string]any{"connection": "pg", "database": "postgres"}, "")
				if fmt.Sprint(schemas["names"]) == "[]" {
					t.Fatal("no schemas")
				}
			} else {
				q["sql"] = "SELECT * FROM mcp_it FOR UPDATE"
				f.call(t, "run_query", q, "READ ONLY transaction")
				// MySQL runs executable comments, so DDL could hide behind a SELECT.
				q["sql"] = "/*!CREATE TABLE mcp_it_pwned AS*/ SELECT * FROM mcp_it"
				f.call(t, "run_query", q, "executable comments")
				q["sql"] = "SHOW TABLES"
				if out := f.call(t, "run_query", q, ""); strings.Contains(fmt.Sprint(rows(out)), "mcp_it_pwned") {
					t.Fatal("an executable comment created a table")
				}
			}
			args := at(nil)
			delete(args, "table")
			tables := f.call(t, "list_tables", args, "")
			if len(tables["tables"].([]any)) == 0 {
				t.Fatal("no tables")
			}

			testMonitor(t, f, e.conn)
			testModel(t, f, e.conn.Name, e.database, e.schema)

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

// testMonitor kills a session opened outside Chloe, after a denied attempt.
func testMonitor(t *testing.T, f *fixture, conn model.Connection) {
	f.enable(t, func(c *store.MCPCapabilities) { c.Monitor = true })
	driverName, dsn, idSQL := "mysql", fmt.Sprintf("root:chloe@tcp(127.0.0.1:%d)/", conn.Port), "SELECT CONNECTION_ID()"
	if conn.Type == model.PostgreSQL {
		driverName, dsn, idSQL = "pgx", fmt.Sprintf("postgres://postgres:chloe@127.0.0.1:%d/postgres", conn.Port), "SELECT pg_backend_pid()"
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	victim, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	var id string
	if err := victim.QueryRowContext(ctx, idSQL).Scan(&id); err != nil {
		t.Fatal(err)
	}

	sessions := f.call(t, "list_sessions", map[string]any{"connection": conn.Name}, "")["sessions"].([]any)
	var found bool
	var own string
	for _, x := range sessions {
		s := x.(map[string]any)
		found = found || s["id"] == id
		// Other test packages' Chloe sessions come and go on the same server; the
		// one listing the sessions is this server's own and stays open.
		if q, _ := s["query"].(string); s["chloe"] == true && (own == "" || strings.Contains(strings.ToUpper(q), "PROCESSLIST") || strings.Contains(q, "pg_stat_activity")) {
			own = s["id"].(string)
		}
	}
	if !found || own == "" {
		t.Fatalf("sessions (want %s and one of Chloe's): %v", id, sessions)
	}
	vars := f.call(t, "server_variables", map[string]any{"connection": conn.Name, "filter": "MAX_CONNECTIONS"}, "")["values"].([]any)
	if len(vars) == 0 || vars[0].(map[string]any)["name"] != "max_connections" {
		t.Fatalf("variables: %v", vars)
	}
	if st := f.call(t, "server_status", map[string]any{"connection": conn.Name}, "")["values"].([]any); len(st) == 0 {
		t.Fatal("no status")
	}
	if conn.Type == model.PostgreSQL {
		// The filter matches PostgreSQL's own counter names, given in the descriptions.
		st := f.call(t, "server_status", map[string]any{"connection": conn.Name, "filter": "numbackends"}, "")["values"].([]any)
		if len(st) != 1 || st[0].(map[string]any)["name"] != "connections" {
			t.Fatalf("numbackends: %v", st)
		}
	}

	f.call(t, "kill_session", map[string]any{"connection": conn.Name, "id": own}, "Chloe's own")
	f.appr.answer = false
	f.call(t, "kill_session", map[string]any{"connection": conn.Name, "id": id}, "declined")
	if err := victim.PingContext(ctx); err != nil {
		t.Fatalf("denied kill ran: %v", err)
	}
	f.appr.answer = true
	if out := f.call(t, "kill_session", map[string]any{"connection": conn.Name, "id": id}, ""); out["killed"] != "session "+id {
		t.Fatalf("kill: %v", out)
	}
	if req := f.appr.asked[len(f.appr.asked)-1]; req.Summary != "Kill session "+id+" on "+conn.Name || req.Kind != "write" {
		t.Fatalf("approval: %+v", req)
	}
	if err := victim.QueryRowContext(ctx, "SELECT 1").Scan(new(int)); err == nil {
		t.Fatal("the killed session still works")
	}
}

// testModel reverse engineers the test table, then diffs an edited copy with the database.
func testModel(t *testing.T, f *fixture, conn, database, schema string) {
	f.enable(t, func(c *store.MCPCapabilities) { c.Models = true })
	at := map[string]any{"connection": conn, "database": database}
	if schema != "" {
		at["schema"] = schema
	}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range at {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	m := f.call(t, "create_model", with(map[string]any{"name": "it " + conn, "tables": []any{"mcp_it"}}), "")
	p := m["path"].(string)
	if m["table_count"] != 1.0 {
		t.Fatalf("reverse: %v", m)
	}
	same := f.call(t, "generate_sql", with(map[string]any{"model": p}), "")
	if len(same["statements"].([]any)) != 0 || fmt.Sprint(same["unchanged"]) == "[]" {
		t.Fatalf("unchanged model: %v", same)
	}
	table := f.call(t, "get_model", map[string]any{"model": p}, "")["tables"].([]any)[0].(map[string]any)
	table["columns"] = append(table["columns"].([]any), map[string]any{"name": "note", "type": "text", "nullable": true})
	f.call(t, "edit_model", map[string]any{"model": p, "set_tables": []any{table}}, "")
	diff := f.call(t, "generate_sql", with(map[string]any{"model": p}), "")
	if sql := fmt.Sprint(diff["statements"]); !strings.Contains(sql, "ADD") || !strings.Contains(sql, "note") {
		t.Fatalf("diff: %v", diff)
	}
	// Nothing ran.
	q := with(map[string]any{"sql": "SELECT * FROM mcp_it LIMIT 1"})
	if cols := fmt.Sprint(f.call(t, "run_query", q, "")["columns"]); strings.Contains(cols, "note") {
		t.Fatalf("generate_sql changed the table: %v", cols)
	}
}
