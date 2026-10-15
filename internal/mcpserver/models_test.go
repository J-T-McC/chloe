package mcpserver_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/datamodel"
	"chloe/internal/design"
	"chloe/internal/store"
)

func TestModels(t *testing.T) {
	f := setup(t, store.MCPConnAccess{Level: store.MCPRead})
	if _, err := f.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_models", Arguments: map[string]any{}}); err == nil {
		t.Fatal("list_models without the capability")
	}
	f.enable(t, func(c *store.MCPCapabilities) { c.Models = true })

	// One of the user's models, in the library, and a model file elsewhere.
	users := datamodel.New("Shop", "postgres")
	users.Tables = []design.Table{{Schema: "public", Name: "orders", Columns: []design.Column{{Name: "id", Type: "integer"}}, PrimaryKey: &design.PrimaryKey{Columns: []string{"id"}}}}
	lib := f.m.Store.Models()
	userPath, err := lib.Create(users)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(userPath)
	secret := filepath.Join(t.TempDir(), "other.chloemodel")
	_ = os.WriteFile(secret, data, 0o644)

	ms := f.call(t, "list_models", map[string]any{}, "")["models"].([]any)
	if len(ms) != 1 || ms[0].(map[string]any)["path"] != userPath || ms[0].(map[string]any)["editable"] != false {
		t.Fatalf("list: %v", ms)
	}
	f.call(t, "get_model", map[string]any{"model": secret}, "no model")
	f.call(t, "get_model", map[string]any{"model": "/etc/passwd"}, "no model")
	f.call(t, "edit_model", map[string]any{"model": userPath, "drop_tables": []any{"orders"}}, "copy it")
	f.call(t, "create_model", map[string]any{"name": "x", "connection": "shop"}, "not SQLite")
	f.call(t, "create_model", map[string]any{"name": "x"}, "dialect")

	// A copy is the agent's to change.
	cp := f.call(t, "create_model", map[string]any{"name": "Shop v2", "copy_of": userPath}, "")
	p := cp["path"].(string)
	if cp["editable"] != true || cp["table_count"] != 1.0 || cp["created_by"] != "mcp:test" || !lib.Contains(p) {
		t.Fatalf("copy: %v", cp)
	}
	customers := map[string]any{"name": "customers", "columns": []any{
		map[string]any{"name": "id", "type": "integer"}, map[string]any{"name": "email", "type": "text", "nullable": true}},
		"primaryKey": map[string]any{"columns": []any{"id"}}}
	orders := map[string]any{"name": "orders", "columns": []any{
		map[string]any{"name": "id", "type": "integer"}, map[string]any{"name": "customer_id", "type": "integer"}},
		"primaryKey":  map[string]any{"columns": []any{"id"}},
		"foreignKeys": []any{map[string]any{"name": "fk_orders_customer", "columns": []any{"customer_id"}, "refTable": "customers", "refColumns": []any{"id"}}}}
	f.call(t, "edit_model", map[string]any{"model": p, "set_tables": []any{map[string]any{"name": "t", "colums": []any{}}}}, "unknown field")
	f.call(t, "edit_model", map[string]any{"model": p, "set_tables": []any{map[string]any{"name": "t", "columns": []any{}}}}, "at least one column")
	f.call(t, "edit_model", map[string]any{"model": p, "drop_tables": []any{"nope"}}, "no table")
	out := f.call(t, "edit_model", map[string]any{"model": p, "set_tables": []any{customers, orders}, "rename": "Shop v3"}, "")
	if out["table_count"] != 2.0 || out["name"] != "Shop v3" || f.events.models[len(f.events.models)-1] != p {
		t.Fatalf("edit: %v %v", out, f.events.models)
	}
	got := f.call(t, "get_model", map[string]any{"model": p, "tables": []any{"orders"}}, "")
	if ts := got["tables"].([]any); len(ts) != 1 || len(ts[0].(map[string]any)["foreignKeys"].([]any)) != 1 {
		t.Fatalf("get: %v", got)
	}
	sql := f.call(t, "generate_sql", map[string]any{"model": p}, "")["statements"].([]any)
	all := ""
	for _, s := range sql {
		all += s.(string) + ";\n"
	}
	if !strings.Contains(all, "CREATE TABLE public.customers") || !strings.Contains(all, "FOREIGN KEY") {
		t.Fatalf("sql:\n%s", all)
	}
	f.call(t, "edit_model", map[string]any{"model": p, "drop_tables": []any{"customers"}}, "")
	if m := f.call(t, "get_model", map[string]any{"model": p}, ""); m["table_count"] != 1.0 {
		t.Fatalf("drop: %v", m)
	}
	// The user's file is untouched.
	if after, _ := os.ReadFile(userPath); string(after) != string(data) {
		t.Fatal("the user's model changed")
	}
	f.call(t, "open_model", map[string]any{"model": userPath}, "")
	if len(f.events.opened) != 1 || f.events.opened[0] != userPath {
		t.Fatalf("opened: %v", f.events.opened)
	}
	// Names are made safe for a file and kept unique.
	if a := f.call(t, "create_model", map[string]any{"name": "../../etc/x", "dialect": "mysql"}, ""); !lib.Contains(a["path"].(string)) {
		t.Fatalf("escaped: %v", a)
	}
	if c := f.call(t, "create_model", map[string]any{"name": "Shop – copy (2)", "dialect": "mysql"}, "")["path"].(string); filepath.Base(c) != "Shop – copy (2).chloemodel" {
		t.Fatalf("readable name: %s", c)
	}
	b := f.call(t, "create_model", map[string]any{"name": "Shop v2", "dialect": "mysql"}, "")["path"].(string)
	if filepath.Base(b) != "Shop v2 2.chloemodel" {
		t.Fatalf("unique: %s", b)
	}
}
