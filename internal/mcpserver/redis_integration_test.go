package mcpserver_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"chloe/internal/core"
	"chloe/internal/mcpserver"
	"chloe/internal/model"
	"chloe/internal/store"
)

// Redis database 6 is this test's own (rdb's tests use 5).
const redisDB = 6

func setupRedis(t *testing.T, access store.MCPConnAccess, caps store.MCPCapabilities) *fixture {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(model.Connection{Name: "cache", Type: model.Redis, Host: "127.0.0.1", Port: 63790, Password: "chloe", SavePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	ctx := context.Background()
	if err := m.Ensure(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	kv, _ := m.Redis(c.ID)
	cl := kv.DB(redisDB)
	if err := cl.FlushDB(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.FlushDB(context.Background()) })
	cl.Set(ctx, "greeting", "hello", 0)
	cl.HSet(ctx, "user:1", "name", "Ada", "role", "admin")
	cl.RPush(ctx, "queue", "a", "b", "c")
	cl.Do(ctx, "ZADD", "scores", "10", "ann", "20", "bob")
	cl.Set(ctx, "bin\xff", "\xff\x01", 0)
	return serve(t, m, c.ID, store.MCPEndpoint{Name: "test", Enabled: true, Capabilities: caps, Access: map[string]store.MCPConnAccess{c.ID: access}})
}

func TestIntegrationRedisTools(t *testing.T) {
	f := setupRedis(t, store.MCPConnAccess{Level: store.MCPWrite, Databases: map[string]store.MCPLevel{"7": store.MCPOff, "8": store.MCPRead}}, store.MCPCapabilities{})

	conns := f.call(t, "list_connections", nil, "")["connections"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["engine"] != "redis" {
		t.Fatalf("connections: %v", conns)
	}
	dbs := f.call(t, "redis_databases", map[string]any{"connection": "cache"}, "")["databases"].([]any)
	seen := map[float64]any{}
	for _, d := range dbs {
		seen[d.(map[string]any)["db"].(float64)] = d
	}
	if _, ok := seen[7]; ok || seen[redisDB].(map[string]any)["keys"].(float64) != 5 || seen[8].(map[string]any)["access"] != "read" {
		t.Fatalf("databases: %v", dbs)
	}
	f.call(t, "list_tables", map[string]any{"connection": "cache"}, "is a Redis connection")
	at := func(extra map[string]any) map[string]any {
		args := map[string]any{"connection": "cache", "db": redisDB}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	// Reads.
	scan := f.call(t, "redis_scan", at(map[string]any{"pattern": "user:*"}), "")
	if keys := scan["keys"].([]any); len(keys) != 1 || keys[0].(map[string]any)["type"] != "hash" || scan["cursor"] != nil {
		t.Fatalf("scan: %v", scan)
	}
	all := f.call(t, "redis_scan", at(nil), "")["keys"].([]any)
	var bin map[string]any
	for _, k := range all {
		if k.(map[string]any)["key_base64"] == true {
			bin = k.(map[string]any)
		}
	}
	if len(all) != 5 || bin == nil {
		t.Fatalf("scan all: %v", all)
	}
	v := f.call(t, "redis_get", at(map[string]any{"key": bin["key"], "key_base64": true}), "")
	if v["value"] != "0xff01" || v["key_base64"] != true {
		t.Fatalf("binary get: %v", v)
	}
	v = f.call(t, "redis_get", at(map[string]any{"key": "queue", "count": 2}), "")
	if items := v["items"].([]any); len(items) != 2 || v["cursor"] != "2" || v["length"].(float64) != 3 {
		t.Fatalf("list page: %v", v)
	}
	v = f.call(t, "redis_get", at(map[string]any{"key": "scores"}), "")
	if items := v["items"].([]any); fmt.Sprint(items[1]) != "map[score:20 value:bob]" {
		t.Fatalf("zset: %v", items)
	}
	f.call(t, "redis_get", at(map[string]any{"key": "nope"}), "no key")
	r := f.call(t, "redis_command", at(map[string]any{"args": []any{"ZRANGEBYSCORE", "scores", "15", "+inf"}}), "")
	if r["reply"] != `1) "bob"` {
		t.Fatalf("command: %v", r)
	}
	f.call(t, "redis_command", at(map[string]any{"args": []any{"DEL", "greeting"}}), "only runs read commands")
	f.call(t, "redis_scan", map[string]any{"connection": "cache", "db": 7}, "isn't shared")

	// Writes, each approved.
	f.call(t, "redis_set", at(map[string]any{"key": "greeting", "type": "string", "value": "hi", "only_if_new": true}), "already exists")
	f.call(t, "redis_set", at(map[string]any{"key": "user:2", "type": "hash", "value": map[string]any{"name": "Bo"}, "ttl": 600}), "")
	v = f.call(t, "redis_get", at(map[string]any{"key": "user:2"}), "")
	if v["ttl"].(float64) <= 0 || len(v["items"].([]any)) != 1 {
		t.Fatalf("set hash: %v", v)
	}
	f.call(t, "redis_set", at(map[string]any{"key": "user:1", "type": "list", "value": []any{"x"}}), "")
	if a := f.appr.last(); !strings.Contains(a.Summary, `Replace hash "user:1" (2 elements) with a list`) || !strings.Contains(a.SQL, `DEL "user:1"`) {
		t.Fatalf("approval: %+v", a)
	}
	f.call(t, "redis_edit", at(map[string]any{"key": "queue", "set_fields": map[string]any{"a": "1"}}), "is a list, not a hash")
	out := f.call(t, "redis_edit", at(map[string]any{"key": "scores", "add_scored": map[string]any{"cy": 5}, "remove": []any{"ann"}}), "")
	if out["changed"].(float64) != 2 {
		t.Fatalf("edit: %v", out)
	}
	r = f.call(t, "redis_command", at(map[string]any{"args": []any{"ZRANGE", "scores", "0", "-1"}}), "")
	if r["reply"] != "1) \"cy\"\n2) \"bob\"" {
		t.Fatalf("after edit: %v", r)
	}
	f.call(t, "redis_edit", at(map[string]any{"key": "events", "add_entry": map[string]any{"kind": "login"}}), "")
	f.call(t, "redis_expire", at(map[string]any{"key": "greeting", "ttl": 60}), "")
	f.appr.mu.Lock()
	f.appr.answer = false
	f.appr.mu.Unlock()
	f.call(t, "redis_delete", at(map[string]any{"keys": []any{"greeting", "queue", "missing"}}), "declined")
	if a := f.appr.last(); a.Rows != 2 || a.Summary != "Delete 2 keys from db6" {
		t.Fatalf("delete approval: %+v", a)
	}
	f.appr.mu.Lock()
	f.appr.answer = true
	f.appr.mu.Unlock()
	if out := f.call(t, "redis_delete", at(map[string]any{"keys": []any{"greeting", "queue"}}), ""); out["changed"].(float64) != 2 {
		t.Fatalf("delete: %v", out)
	}

	// A key written by someone else while the dialog is open isn't overwritten.
	kv, _ := f.m.Redis(f.conn)
	f.appr.mu.Lock()
	f.appr.during = func() { kv.DB(redisDB).Set(context.Background(), "late", "theirs", 0) }
	f.appr.mu.Unlock()
	f.call(t, "redis_set", at(map[string]any{"key": "late", "type": "hash", "value": map[string]any{"a": "1"}}), "changed while waiting for approval")
	f.appr.mu.Lock()
	f.appr.during = func() { kv.DB(redisDB).RPush(context.Background(), "late", "x") } // wrong type now
	f.appr.mu.Unlock()
	kv.DB(redisDB).Del(context.Background(), "late")
	f.call(t, "redis_edit", at(map[string]any{"key": "late", "set_fields": map[string]any{"a": "1"}}), "changed while waiting for approval")
	if typ := kv.DB(redisDB).Type(context.Background(), "late").Val(); typ != "list" {
		t.Fatalf("late is a %s", typ)
	}
	f.appr.mu.Lock()
	f.appr.during = nil
	f.appr.mu.Unlock()

	// Tester findings: cursors, counts, TTLs, duplicates, truncation.
	cl := kv.DB(redisDB)
	f.call(t, "redis_get", at(map[string]any{"key": "late", "cursor": "-1"}), "isn't one redis_get returned")
	f.call(t, "redis_scan", at(map[string]any{"cursor": "abc"}), "isn't one redis_scan returned")
	for i := range 300 {
		cl.Set(context.Background(), fmt.Sprintf("bulk:%d", i), "v", 0)
	}
	if keys := f.call(t, "redis_scan", at(map[string]any{"pattern": "bulk:*", "count": 100}), "")["keys"].([]any); len(keys) > 120 {
		t.Fatalf("scan page of %d keys for count 100", len(keys))
	}
	if out := f.call(t, "redis_edit", at(map[string]any{"key": "scores", "remove": []any{"nope"}}), ""); out["changed"].(float64) != 0 {
		t.Fatalf("no-op remove: %v", out)
	}
	if out := f.call(t, "redis_edit", at(map[string]any{"key": "scores", "add_scored": map[string]any{"bob": 21}}), ""); out["changed"].(float64) != 1 {
		t.Fatalf("rescore: %v", out)
	}
	f.call(t, "redis_edit", at(map[string]any{"key": "hits", "incr": 2}), "")
	if out := f.call(t, "redis_edit", at(map[string]any{"key": "hits", "incr": 3}), ""); fmt.Sprint(out["new_values"]) != "map[hits:5]" {
		t.Fatalf("incr result: %v", out)
	}
	if v := cl.Get(context.Background(), "hits").Val(); v != "5" {
		t.Fatalf("hits %s", v)
	}
	f.call(t, "redis_set", at(map[string]any{"key": "x", "type": "string", "value": "1", "ttl": -1}), "can't be negative")
	cl.Set(context.Background(), "sess", "a", time.Hour)
	f.call(t, "redis_set", at(map[string]any{"key": "sess", "type": "string", "value": "b"}), "")
	if a := f.appr.last(); strings.Contains(a.SQL, "DEL") || !strings.Contains(a.SQL, "KEEPTTL") {
		t.Fatalf("string replace: %s", a.SQL)
	}
	f.call(t, "redis_set", at(map[string]any{"key": "sess", "type": "hash", "value": map[string]any{"a": "1"}}), "")
	if a := f.appr.last(); !strings.Contains(a.SQL, "PEXPIREAT") {
		t.Fatalf("type replace: %s", a.SQL)
	}
	if d := cl.TTL(context.Background(), "sess").Val(); d < 59*time.Minute {
		t.Fatalf("ttl %v", d)
	}
	f.call(t, "redis_delete", at(map[string]any{"keys": []any{"sess", "sess", "gone"}}), "")
	if a := f.appr.last(); a.Rows != 1 || strings.Count(a.SQL, "sess") != 1 {
		t.Fatalf("duplicate delete: %+v", a)
	}
	cl.Set(context.Background(), "big", strings.Repeat("x", 70000), 0)
	if v := f.call(t, "redis_get", at(map[string]any{"key": "big"}), ""); v["truncated"] != true {
		t.Fatal("big string not marked truncated")
	}
	f.call(t, "redis_set", map[string]any{"connection": "cache", "db": 8, "key": "x", "type": "string", "value": "1"}, "can only read")

	// The overview groups keys by prefix and type; bulk:* came from the checks above.
	ov := f.call(t, "redis_overview", at(nil), "")
	groups := fmt.Sprint(ov["groups"])
	if ov["complete"] != true || !strings.Contains(groups, "count:300 example:bulk:") || !strings.Contains(groups, "pattern:greeting") && !strings.Contains(groups, "pattern:hits") {
		t.Fatalf("overview: %v", ov)
	}
	// Write results say how they were cleared.
	if out := f.call(t, "redis_expire", at(map[string]any{"key": "hits", "ttl": 0}), ""); out["approval"] != "approved by the user in Chloe" {
		t.Fatalf("approval: %v", out)
	}
}

func TestIntegrationRedisMonitor(t *testing.T) {
	f := setupRedis(t, store.MCPConnAccess{Level: store.MCPRead}, store.MCPCapabilities{Monitor: true})
	vars := f.call(t, "server_variables", map[string]any{"connection": "cache", "filter": "requirepass"}, "")["values"].([]any)
	if len(vars) != 1 || vars[0].(map[string]any)["value"] != "(hidden)" {
		t.Fatalf("variables: %v", vars)
	}
	status := f.call(t, "server_status", map[string]any{"connection": "cache", "filter": "redis_version"}, "")["values"].([]any)
	if len(status) != 1 || !strings.HasPrefix(status[0].(map[string]any)["value"].(string), "7.") {
		t.Fatalf("status: %v", status)
	}
	// Entries match on their INFO section too, and say which it is.
	ks := f.call(t, "server_status", map[string]any{"connection": "cache", "filter": "keyspace"}, "")["values"].([]any)
	if !strings.Contains(fmt.Sprint(ks), "name:db6 section:Keyspace") {
		t.Fatalf("keyspace section: %v", ks)
	}
	sessions := f.call(t, "list_sessions", map[string]any{"connection": "cache"}, "")["sessions"].([]any)
	self := false
	for _, s := range sessions {
		self = self || s.(map[string]any)["chloe"] == true
	}
	if !self {
		t.Fatalf("sessions: %v", sessions)
	}
}

// last is the most recent approval request.
func (a *approver) last() mcpserver.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.asked) == 0 {
		return mcpserver.ApprovalRequest{}
	}
	return a.asked[len(a.asked)-1]
}
