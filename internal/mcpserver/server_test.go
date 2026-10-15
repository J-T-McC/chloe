package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/core"
	_ "chloe/internal/driver/sqlite"
	"chloe/internal/mcpserver"
	"chloe/internal/model"
	"chloe/internal/store"
)

const seed = `
CREATE TABLE customers (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, name TEXT, vip BOOLEAN DEFAULT 0);
CREATE TABLE secrets (id INTEGER PRIMARY KEY, value TEXT);
INSERT INTO customers (email, name) VALUES ('ada@example.com', 'Ada'), ('bob@example.com', 'Bob'), ('cy@example.com', NULL);
INSERT INTO secrets VALUES (1, 'hunter2');
`

// approver answers approval requests with a fixed decision and records them.
type approver struct {
	mu     sync.Mutex
	answer bool
	asked  []mcpserver.ApprovalRequest
}

func (a *approver) Approve(_ context.Context, r mcpserver.ApprovalRequest) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asked = append(a.asked, r)
	return a.answer
}

type fixture struct {
	m      *core.Manager
	srv    *mcpserver.Server
	conn   string // connection ID
	ep     store.MCPEndpoint
	appr   *approver
	client *mcp.ClientSession
	url    string
}

func setup(t *testing.T, access store.MCPConnAccess) *fixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shop.db")
	return setupConn(t, model.Connection{Name: "shop", Type: model.SQLite, SQLite: model.SQLiteConfig{Path: path, Create: true}}, "main", "", seed, access)
}

// setupConn saves a connection, runs seed on it and serves an endpoint with access to it.
func setupConn(t *testing.T, conn model.Connection, database, schema, seed string, access store.MCPConnAccess) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(conn)
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	ctx := context.Background()
	if err := m.Ensure(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	sid, err := m.OpenSession(ctx, c.ID, database, schema)
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.Execute(ctx, sid, seed, core.ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.Error != "" {
			t.Fatalf("seed: %s: %s", r.SQL, r.Error)
		}
	}
	m.CloseSession(sid)

	appr := &approver{answer: true}
	srv := mcpserver.New(m, appr, nil)
	ep := store.MCPEndpoint{Name: "test", Enabled: true, Access: map[string]store.MCPConnAccess{c.ID: access}}
	// Port 0 never listens (Enabled is false); the test serves the handler itself.
	if err := srv.Save(store.MCPSettings{Endpoints: []store.MCPEndpoint{ep}}); err != nil {
		t.Fatal(err)
	}
	ep = srv.Settings().Endpoints[0]
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(hs.Close)

	f := &fixture{m: m, srv: srv, conn: c.ID, ep: ep, appr: appr, url: hs.URL + "/mcp"}
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	cs, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.url, HTTPClient: tokenClient(ep.Token)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	f.client = cs
	return f
}

// exec runs SQL on the fixture's connection directly, outside MCP.
func (f *fixture) exec(t *testing.T, database, schema, sql string) {
	sid, err := f.m.OpenSession(context.Background(), f.conn, database, schema)
	if err != nil {
		t.Error(err)
		return
	}
	defer f.m.CloseSession(sid)
	if _, err := f.m.Execute(context.Background(), sid, sql, core.ExecOptions{}); err != nil {
		t.Error(err)
	}
}

type tokenTransport struct{ token string }

func (t tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return http.DefaultTransport.RoundTrip(r)
}

func tokenClient(tok string) *http.Client { return &http.Client{Transport: tokenTransport{tok}} }

// call runs a tool and decodes its structured result; it fails the test on a tool error unless wantErr.
func (f *fixture) call(t *testing.T, tool string, args map[string]any, wantErr string) map[string]any {
	t.Helper()
	res, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	text := ""
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			text += tc.Text
		}
	}
	if wantErr != "" {
		if !res.IsError || !strings.Contains(text, wantErr) {
			t.Fatalf("%s: want error containing %q, got %v %s", tool, wantErr, res.IsError, text)
		}
		return nil
	}
	if res.IsError {
		t.Fatalf("%s: %s", tool, text)
	}
	var out map[string]any
	data, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func rows(out map[string]any) [][]any {
	var r [][]any
	for _, x := range out["rows"].([]any) {
		r = append(r, x.([]any))
	}
	return r
}

func TestReadTools(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	conns := f.call(t, "list_connections", nil, "")["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["name"] != "shop" || conns[0].(map[string]any)["access"] != "read" {
		t.Fatalf("connections: %v", conns)
	}
	if dbs := f.call(t, "list_databases", map[string]any{"connection": "shop"}, "")["names"]; fmt.Sprint(dbs) != "[main]" {
		t.Fatalf("databases: %v", dbs)
	}
	tables := f.call(t, "list_tables", map[string]any{"connection": "shop"}, "")["tables"].([]any)
	if len(tables) != 2 {
		t.Fatalf("tables: %v", tables)
	}
	desc := f.call(t, "describe_table", map[string]any{"connection": "shop", "table": "customers"}, "")
	if cols := desc["columns"].([]any); len(cols) != 4 {
		t.Fatalf("columns: %v", cols)
	}

	out := f.call(t, "select_rows", map[string]any{"connection": "shop", "table": "customers", "columns": []string{"id", "name"},
		"where": []any{map[string]any{"column": "id", "op": ">=", "value": 2}}, "order_by": []any{map[string]any{"column": "id", "desc": true}}}, "")
	if r := rows(out); len(r) != 2 || r[0][0] != "3" || r[0][1] != nil || r[1][1] != "Bob" {
		t.Fatalf("select: %v", out)
	}
	out = f.call(t, "select_rows", map[string]any{"connection": "shop", "table": "customers", "limit": 2}, "")
	if out["truncated"] != true || len(rows(out)) != 2 {
		t.Fatalf("limit: %v", out)
	}
	f.call(t, "select_rows", map[string]any{"connection": "shop", "table": "customers", "where": []any{map[string]any{"column": "nope", "op": "=", "value": 1}}}, `no column "nope"`)
	f.call(t, "select_rows", map[string]any{"connection": "shop", "table": "customers", "where": []any{map[string]any{"column": "id; DROP TABLE x", "op": "=", "value": 1}}}, "no column")

	out = f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "SELECT count(*) AS n FROM customers WHERE name IN ('Ada', 'Bob')"}, "")
	if r := rows(out); r[0][0] != "2" {
		t.Fatalf("run_query: %v", out)
	}
	f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "DELETE FROM customers"}, "only runs read statements")
	f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "PRAGMA query_only = OFF"}, "only runs read statements")
	f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "SELECT 1; DELETE FROM customers"}, "exactly one statement")
	// A CTE that writes gets past the keyword check; query_only stops it.
	f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "WITH x AS (SELECT 1) INSERT INTO secrets SELECT 2, 'x' FROM x"}, "readonly")

	// Read access isn't offered the write tools.
	tools, err := f.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if strings.HasSuffix(tl.Name, "_rows") && tl.Name != "select_rows" {
			t.Fatalf("read-only endpoint lists %s", tl.Name)
		}
	}
	if _, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "delete_rows", Arguments: map[string]any{"connection": "shop"}}); err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Fatalf("delete_rows on a read-only endpoint: %v", err)
	}
	if n := len(f.appr.asked); n != 0 {
		t.Fatalf("asked %d times", n)
	}
}

func TestWritesNeedApproval(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPWrite})
	out := f.call(t, "insert_rows", map[string]any{"connection": "shop", "table": "customers",
		"rows": []any{map[string]any{"email": "dee@example.com", "name": "Dee", "vip": true}, map[string]any{"email": "eve@example.com"}}}, "")
	if out["inserted"] != 2.0 || fmt.Sprint(out["keys"]) != "[map[id:4] map[id:5]]" {
		t.Fatalf("insert: %v", out)
	}
	a := f.appr.asked[0]
	if a.Summary != "Insert 2 rows into customers" || !strings.Contains(a.SQL, "'dee@example.com'") || a.Endpoint != "test" || a.Connection != "shop" {
		t.Fatalf("approval: %+v", a)
	}

	out = f.call(t, "update_rows", map[string]any{"connection": "shop", "table": "customers",
		"where": []any{map[string]any{"column": "name", "op": "is_null"}}, "set": map[string]any{"name": "Anon"}}, "")
	if out["rows"] != 2.0 || f.appr.asked[1].Rows != 2 || f.appr.asked[1].Summary != "Update 2 rows in customers" {
		t.Fatalf("update: %v %+v", out, f.appr.asked[1])
	}
	f.call(t, "update_rows", map[string]any{"connection": "shop", "table": "customers", "where": []any{}, "set": map[string]any{"name": "x"}}, "at least one condition")

	f.appr.answer = false
	f.call(t, "delete_rows", map[string]any{"connection": "shop", "table": "customers", "where": []any{map[string]any{"column": "id", "op": "in", "value": []any{1, 2}}}}, "declined by the user")
	out = f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "SELECT count(*) FROM customers"}, "")
	if rows(out)[0][0] != "5" {
		t.Fatalf("denied delete ran: %v", out)
	}
	if acts := f.srv.Activity(); acts[1].Outcome != "denied" || acts[1].Tool != "delete_rows" {
		t.Fatalf("activity: %+v", acts[1])
	}

	// Danger mode skips the dialog.
	st := f.srv.Settings()
	st.Endpoints[0].Danger = true
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	asked := len(f.appr.asked)
	out = f.call(t, "delete_rows", map[string]any{"connection": "shop", "table": "customers", "where": []any{map[string]any{"column": "id", "op": "between", "value": []any{4, 5}}}}, "")
	if out["rows"] != 2.0 || len(f.appr.asked) != asked {
		t.Fatalf("danger delete: %v, asked %d", out, len(f.appr.asked)-asked)
	}
	if acts := f.srv.Activity(); acts[0].Outcome != "unasked" {
		t.Fatalf("activity: %+v", acts[0])
	}
}

func TestWriteToolsFollowAccess(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead, Databases: map[string]store.MCPLevel{"main": store.MCPWrite}})
	names := func() []string {
		tools, err := f.client.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var n []string
		for _, tl := range tools.Tools {
			n = append(n, tl.Name)
		}
		return n
	}
	if n := names(); !slices.Contains(n, "update_rows") {
		t.Fatalf("a database with write access should offer writes: %v", n)
	}
	// Taking write access away drops the tools on the next request.
	st := f.srv.Settings()
	st.Endpoints[0].Access[f.conn] = store.MCPConnAccess{Level: store.MCPRead}
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	if n := names(); slices.Contains(n, "update_rows") {
		t.Fatalf("still offers writes: %v", n)
	}

	// With writes allowed somewhere, a read-only database still refuses them.
	st.Endpoints[0].Access[f.conn] = store.MCPConnAccess{Level: store.MCPWrite, Databases: map[string]store.MCPLevel{"main": store.MCPRead}}
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	f.call(t, "insert_rows", map[string]any{"connection": "shop", "table": "customers", "rows": []any{map[string]any{"email": "x@y.z"}}}, "can only read")
}

func TestDatabaseOverrides(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPOff, Databases: map[string]store.MCPLevel{"main": store.MCPRead}})
	f.call(t, "select_rows", map[string]any{"connection": "shop", "table": "customers"}, "")
	// Raw SQL could reach other attached databases, so it needs the whole connection readable.
	f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "SELECT 1"}, "run_query isn't available")
	f.call(t, "select_rows", map[string]any{"connection": "shop", "database": "other", "table": "t"}, "isn't shared")
}

func TestAuth(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	post := func(token, origin string) int {
		req, _ := http.NewRequest("POST", f.url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := post("", ""); c != http.StatusUnauthorized {
		t.Fatalf("no token: %d", c)
	}
	if c := post("chloe_wrong", ""); c != http.StatusUnauthorized {
		t.Fatalf("wrong token: %d", c)
	}
	if c := post(f.ep.Token, "https://evil.example"); c != http.StatusForbidden {
		t.Fatalf("cross-origin: %d", c)
	}
	if c := post(f.ep.Token, ""); c != http.StatusOK {
		t.Fatalf("good token: %d", c)
	}
	// Disabling the endpoint revokes its token at once.
	st := f.srv.Settings()
	st.Endpoints[0].Enabled = false
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	if c := post(f.ep.Token, ""); c != http.StatusUnauthorized {
		t.Fatalf("disabled endpoint: %d", c)
	}
}
