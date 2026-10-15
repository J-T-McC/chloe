package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/store"
)

// enable switches on endpoint capabilities.
func (f *fixture) enable(t *testing.T, fn func(*store.MCPCapabilities)) {
	t.Helper()
	st := f.srv.Settings()
	fn(&st.Endpoints[0].Capabilities)
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
}

func TestSavedQueries(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	if _, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_saved_queries", Arguments: map[string]any{"connection": "shop"}}); err == nil {
		t.Fatal("list_saved_queries without the capability")
	}
	f.enable(t, func(c *store.MCPCapabilities) { c.SavedQueries = true })
	ws := f.m.Store.Workspace()
	if _, err := ws.SaveQuery(store.SavedQuery{ConnID: f.conn, Database: "main", Name: "VIPs", SQL: "SELECT * FROM customers WHERE vip"}); err != nil {
		t.Fatal(err)
	}

	out := f.call(t, "list_saved_queries", map[string]any{"connection": "shop"}, "")
	if qs := out["queries"].([]any); len(qs) != 1 || qs[0].(map[string]any)["name"] != "VIPs" {
		t.Fatalf("list: %v", out)
	}
	if out := f.call(t, "get_saved_query", map[string]any{"connection": "shop", "name": "vips"}, ""); out["sql"] != "SELECT * FROM customers WHERE vip" {
		t.Fatalf("get: %v", out)
	}
	f.call(t, "get_saved_query", map[string]any{"connection": "shop", "name": "nope"}, "no saved query")

	// A read-only endpoint can save new queries but never replace one.
	f.call(t, "save_query", map[string]any{"connection": "shop", "name": "VIPs", "sql": "DELETE FROM customers"}, "can't replace")
	if q, _ := f.call(t, "get_saved_query", map[string]any{"connection": "shop", "name": "VIPs"}, "")["sql"].(string); !strings.HasPrefix(q, "SELECT") {
		t.Fatalf("replaced: %q", q)
	}
	f.call(t, "save_query", map[string]any{"connection": "shop", "name": "Empty", "sql": "  "}, "sql is empty")
	if out := f.call(t, "save_query", map[string]any{"connection": "shop", "name": "Big orders", "sql": "SELECT * FROM orders WHERE total > 100"}, ""); out["name"] != "Big orders" {
		t.Fatalf("save: %v", out)
	}
	if len(ws.Queries(f.conn, "main", "")) != 2 || len(f.events.saved) != 1 || f.events.saved[0] != f.conn+"/main/" {
		t.Fatalf("saved: %v %v", ws.Queries(f.conn, "main", ""), f.events.saved)
	}

	// Tabs open in the UI; nothing runs.
	if out := f.call(t, "open_query_tab", map[string]any{"connection": "shop", "sql": "UPDATE customers SET vip = 1"}, ""); out["opened"] != "Query from test" {
		t.Fatalf("open: %v", out)
	}
	f.call(t, "open_query_tab", map[string]any{"connection": "shop", "name": "VIPs"}, "")
	f.call(t, "open_query_tab", map[string]any{"connection": "shop", "name": "missing"}, "no saved query")
	if tabs := f.events.tabs; len(tabs) != 2 || tabs[0].SQL != "UPDATE customers SET vip = 1" || tabs[0].Database != "main" || tabs[1].QueryName != "VIPs" || tabs[1].SQL != "" {
		t.Fatalf("tabs: %+v", tabs)
	}
	if rows(f.call(t, "run_query", map[string]any{"connection": "shop", "sql": "SELECT count(*) FROM customers WHERE vip = 1"}, ""))[0][0] != "0" {
		t.Fatal("the tab's SQL ran")
	}

	// Hidden databases stay hidden.
	st := f.srv.Settings()
	st.Endpoints[0].Access[f.conn] = store.MCPConnAccess{Level: store.MCPRead, Databases: map[string]store.MCPLevel{"main": store.MCPOff}}
	if err := f.srv.Save(st); err != nil {
		t.Fatal(err)
	}
	f.call(t, "list_saved_queries", map[string]any{"connection": "shop"}, "isn't shared")
}

func TestMonitorTools(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPWrite})
	if _, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_sessions", Arguments: map[string]any{"connection": "shop"}}); err == nil {
		t.Fatal("list_sessions without the capability")
	}
	f.enable(t, func(c *store.MCPCapabilities) { c.Monitor = true })
	f.call(t, "list_sessions", map[string]any{"connection": "shop"}, "no server to monitor")
	f.call(t, "kill_session", map[string]any{"connection": "shop", "id": "1"}, "no server to monitor")
}
