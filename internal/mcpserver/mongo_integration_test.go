package mcpserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"chloe/internal/core"
	"chloe/internal/model"
	"chloe/internal/store"
	"chloe/internal/testdb"
)

// The test's own database, so other packages' tests don't see it.
const mongoDB = "chloe_mcp_it"

func setupMongo(t *testing.T, access store.MCPConnAccess, caps store.MCPCapabilities) (*fixture, *mongo.Database) {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "cfg"), &store.MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Save(model.Connection{Name: "docs", Type: model.MongoDB, Host: "127.0.0.1", Port: testdb.Mongo, User: "root", Password: "chloe", SavePassword: true})
	if err != nil {
		t.Fatal(err)
	}
	m := core.NewManager(st, nil, core.DefaultSettings)
	t.Cleanup(m.Close)
	ctx := context.Background()
	if err := m.Ensure(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	mc, _ := m.Mongo(c.ID)
	db := mc.Mongo().Database(mongoDB)
	if err := db.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Drop(context.Background()) })
	_, err = db.Collection("orders").InsertMany(ctx, []any{
		bson.D{{Key: "_id", Value: 1}, {Key: "customer", Value: "ada"}, {Key: "status", Value: "open"}, {Key: "total", Value: 30},
			{Key: "items", Value: bson.A{bson.D{{Key: "sku", Value: "A1"}, {Key: "qty", Value: 2}}}}},
		bson.D{{Key: "_id", Value: 2}, {Key: "customer", Value: "bob"}, {Key: "status", Value: "open"}, {Key: "total", Value: 10}},
		bson.D{{Key: "_id", Value: 3}, {Key: "customer", Value: "ada"}, {Key: "status", Value: "shipped"}, {Key: "total", Value: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateView(ctx, "open_orders", "orders", bson.A{bson.D{{Key: "$match", Value: bson.D{{Key: "status", Value: "open"}}}}}); err != nil {
		t.Fatal(err)
	}
	return serve(t, m, c.ID, store.MCPEndpoint{Name: "test", Enabled: true, Capabilities: caps, Access: map[string]store.MCPConnAccess{c.ID: access}}), db
}

func docs(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	var r []map[string]any
	for _, d := range out["documents"].([]any) {
		r = append(r, d.(map[string]any))
	}
	return r
}

func TestIntegrationMongoTools(t *testing.T) {
	f, db := setupMongo(t, store.MCPConnAccess{Level: store.MCPWrite, Databases: map[string]store.MCPLevel{"admin": store.MCPOff}}, store.MCPCapabilities{})
	ctx := context.Background()
	at := func(extra map[string]any) map[string]any {
		args := map[string]any{"connection": "docs", "database": mongoDB, "collection": "orders"}
		for k, v := range extra {
			args[k] = v
		}
		return args
	}

	conns := f.call(t, "list_connections", nil, "")["connections"].([]any)
	if conns[0].(map[string]any)["engine"] != "mongodb" {
		t.Fatalf("connections: %v", conns)
	}
	dbs := fmt.Sprint(f.call(t, "mongo_databases", map[string]any{"connection": "docs"}, "")["databases"])
	if !strings.Contains(dbs, mongoDB) || strings.Contains(dbs, "name:admin") {
		t.Fatalf("databases: %s", dbs)
	}
	f.call(t, "list_tables", map[string]any{"connection": "docs", "database": mongoDB}, "is a MongoDB connection")
	f.call(t, "mongo_find", map[string]any{"connection": "docs", "database": "admin", "collection": "anything"}, "isn't shared")
	if cs := fmt.Sprint(f.call(t, "mongo_collections", map[string]any{"connection": "docs", "database": mongoDB}, "")["collections"]); !strings.Contains(cs, "count:3") {
		t.Fatalf("collections: %s", cs)
	}
	desc := f.call(t, "mongo_describe", at(nil), "")
	if !strings.Contains(fmt.Sprint(desc["fields"]), "path:total seen_in:3 type:int") || !strings.Contains(fmt.Sprint(desc["indexes"]), "_id_") {
		t.Fatalf("describe: %v", desc)
	}

	// Reads.
	out := f.call(t, "mongo_find", at(map[string]any{"filter": map[string]any{"total": map[string]any{"$gte": 20}}, "sort": []any{map[string]any{"field": "total", "desc": true}}, "count": true}), "")
	if d := docs(t, out); len(d) != 2 || d[0]["_id"].(float64) != 1 || out["total"].(float64) != 2 {
		t.Fatalf("find: %v", out)
	}
	if out := f.call(t, "mongo_find", at(map[string]any{"limit": 2}), ""); len(docs(t, out)) != 2 || out["has_more"] != true || out["truncated"] != false {
		t.Fatalf("limit: %v", out)
	}
	f.call(t, "mongo_find", at(map[string]any{"limit": -5}), "can't be negative")
	f.call(t, "mongo_find", at(map[string]any{"collection": "ordres"}), `no collection "ordres"`)
	f.call(t, "mongo_aggregate", map[string]any{"connection": "docs", "database": "nodb", "collection": "x", "pipeline": []any{}}, "no collection")
	// Longs beyond 2^53 stay exact.
	db.Collection("orders").UpdateOne(ctx, bson.D{{Key: "_id", Value: 3}}, bson.D{{Key: "$set", Value: bson.D{{Key: "big", Value: int64(9007199254740993)}}}})
	if d := docs(t, f.call(t, "mongo_find", at(map[string]any{"filter": map[string]any{"_id": 3}}), "")); fmt.Sprint(d[0]["big"]) != "map[$numberLong:9007199254740993]" {
		t.Fatalf("big long: %v", d[0]["big"])
	}
	// A $sort stage keeps its key order: customer ascending, then total descending.
	out = f.call(t, "mongo_aggregate", at(map[string]any{"pipeline": json.RawMessage(`[{"$sort": {"customer": 1, "total": -1}}, {"$project": {"_id": 1}}]`)}), "")
	if got := fmt.Sprint(docs(t, out)); got != "[map[_id:1] map[_id:3] map[_id:2]]" {
		t.Fatalf("aggregate order: %s", got)
	}
	// Server-side JavaScript is up to the database user (javascriptEnabled), not Chloe.
	if d := docs(t, f.call(t, "mongo_find", at(map[string]any{"filter": map[string]any{"$where": "this.total > 25"}}), "")); len(d) != 1 {
		t.Fatalf("$where: %v", d)
	}
	f.call(t, "mongo_aggregate", at(map[string]any{"pipeline": []any{map[string]any{"$out": "copy"}}}), "writes to a collection")
	f.call(t, "mongo_aggregate", at(map[string]any{"pipeline": []any{map[string]any{"$lookup": map[string]any{"from": map[string]any{"db": "admin", "coll": "system.users"}, "as": "u"}}}}), "same database")

	// A batch with a bad document saves the rest and says which failed.
	part := f.call(t, "mongo_insert", at(map[string]any{"documents": []any{map[string]any{"_id": 20}, map[string]any{"_id": 1}, map[string]any{"_id": 21}}}), "")
	if part["inserted"].(float64) != 2 || !strings.Contains(fmt.Sprint(part["failed"]), "index:1") || !strings.Contains(fmt.Sprint(part["failed"]), "E11000") {
		t.Fatalf("partial insert: %v", part)
	}
	db.Collection("orders").DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{20, 21}}}}})
	// Writes to a view are refused before anyone is asked.
	asked := len(f.appr.asked)
	f.call(t, "mongo_delete", map[string]any{"connection": "docs", "database": mongoDB, "collection": "open_orders", "filter": map[string]any{"_id": 1}}, "is a view")
	if len(f.appr.asked) != asked {
		t.Fatal("asked about a write to a view")
	}
	// Expressions aren't stored as data by accident.
	f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{"_id": 1}, "update": map[string]any{"$set": map[string]any{"n": map[string]any{"$add": []any{1, 2}}}}}), "not evaluated")
	f.call(t, "mongo_insert", at(map[string]any{"documents": []any{map[string]any{"n": map[string]any{"$sum": 1}}}}), "Store the computed value instead")
	// Fields with several types say so.
	db.Collection("orders").InsertOne(ctx, bson.D{{Key: "_id", Value: "s1"}, {Key: "total", Value: "n/a"}})
	if fs := fmt.Sprint(f.call(t, "mongo_describe", at(nil), "")["fields"]); !strings.Contains(fs, "types:map[int:3 string:1]") {
		t.Fatalf("mixed types: %s", fs)
	}
	db.Collection("orders").InsertOne(ctx, bson.D{{Key: "_id", Value: "s2"}, {Key: "total", Value: true}})
	db.Collection("orders").InsertOne(ctx, bson.D{{Key: "_id", Value: "s3"}, {Key: "total", Value: 1.5}})
	if fs := fmt.Sprint(f.call(t, "mongo_describe", at(nil), "")["fields"]); !strings.Contains(fs, "path:total seen_in:6 type:mixed") {
		t.Fatalf("no majority: %s", fs)
	}
	db.Collection("orders").DeleteMany(ctx, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: bson.A{"s1", "s2", "s3"}}}}})
	// Fields of documents in arrays are described too.
	if fs := fmt.Sprint(f.call(t, "mongo_describe", at(nil), "")["fields"]); !strings.Contains(fs, "path:items.sku seen_in:1") {
		t.Fatalf("array fields: %s", fs)
	}
	// Writes keep field order and need approval.
	ins := f.call(t, "mongo_insert", at(map[string]any{"documents": json.RawMessage(`[{"zeta": 1, "alpha": 2, "_id": 10, "status": "open"}]`)}), "")
	if ins["inserted"].(float64) != 1 {
		t.Fatalf("insert: %v", ins)
	}
	var stored bson.D
	db.Collection("orders").FindOne(ctx, bson.D{{Key: "_id", Value: 10}}).Decode(&stored)
	if stored[1].Key != "zeta" || stored[2].Key != "alpha" { // the server puts _id first
		t.Fatalf("field order: %v", stored)
	}
	f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{}, "update": map[string]any{"$set": map[string]any{"x": 1}}}), "can't be empty")
	f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{"_id": 1}, "update": map[string]any{"total": 5}}), "not a replacement document")

	// A document that starts matching while the dialog is open isn't changed.
	f.appr.mu.Lock()
	f.appr.during = func() {
		db.Collection("orders").InsertOne(context.Background(), bson.D{{Key: "_id", Value: 99}, {Key: "status", Value: "open"}})
	}
	f.appr.mu.Unlock()
	up := f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{"status": "open"}, "update": map[string]any{"$set": map[string]any{"status": "held"}}, "many": true}), "")
	if up["modified"].(float64) != 3 {
		t.Fatalf("update: %v", up)
	}
	if a := f.appr.last(); a.Summary != "Update 3 documents in orders" || !strings.Contains(a.SQL, `// _id: [1, 2, 10]`) {
		t.Fatalf("approval: %+v", a)
	}
	var late bson.D
	db.Collection("orders").FindOne(ctx, bson.D{{Key: "_id", Value: 99}}).Decode(&late)
	if len(late) != 2 || late[1].Value != "open" {
		t.Fatalf("late document changed: %v", late)
	}
	f.appr.mu.Lock()
	f.appr.during = nil
	f.appr.mu.Unlock()

	up = f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{"_id": 50}, "update": map[string]any{"$set": map[string]any{"status": "new"}}, "upsert": true}), "")
	if up["upserted_id"].(float64) != 50 || !strings.Contains(f.appr.last().SQL, `{"upsert": true}`) {
		t.Fatalf("upsert: %v %s", up, f.appr.last().SQL)
	}
	if up := f.call(t, "mongo_update", at(map[string]any{"filter": map[string]any{"_id": 404}, "update": map[string]any{"$set": map[string]any{"a": 1}}}), ""); up["matched"].(float64) != 0 {
		t.Fatalf("no match: %v", up)
	}
	del := f.call(t, "mongo_delete", at(map[string]any{"filter": map[string]any{"status": "held"}}), "")
	if del["deleted"].(float64) != 1 {
		t.Fatalf("delete one: %v", del)
	}
	del = f.call(t, "mongo_delete", at(map[string]any{"filter": map[string]any{"status": "held"}, "many": true}), "")
	if del["deleted"].(float64) != 2 {
		t.Fatalf("delete many: %v", del)
	}
}

func TestIntegrationMongoReadOnlyAndMonitor(t *testing.T) {
	f, _ := setupMongo(t, store.MCPConnAccess{Level: store.MCPRead}, store.MCPCapabilities{Monitor: true})
	names := ""
	tools, err := f.client.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		names += tl.Name + " "
	}
	if !strings.Contains(names, "mongo_find") || strings.Contains(names, "mongo_insert") || strings.Contains(names, "redis_") {
		t.Fatalf("tools: %s", names)
	}
	if g := f.call(t, "guide", map[string]any{"topic": "mongodb"}, ""); !strings.Contains(g["text"].(string), "$match") {
		t.Fatalf("mongodb topic: %v", g)
	}
	f.call(t, "guide", map[string]any{"topic": "redis"}, "this endpoint has mongodb")
	if text := f.client.InitializeResult().Instructions; !strings.Contains(text, "mongo_describe") || strings.Contains(text, "describe_table") {
		t.Fatalf("a MongoDB endpoint's instructions:\n%s", text)
	}
	if s := f.call(t, "server_status", map[string]any{"connection": "docs", "filter": "uptime"}, "")["values"].([]any); len(s) == 0 {
		t.Fatal("server_status")
	}
	if s := f.call(t, "list_sessions", map[string]any{"connection": "docs"}, "")["sessions"].([]any); len(s) == 0 {
		t.Fatal("list_sessions")
	}
	f.call(t, "server_variables", map[string]any{"connection": "docs", "filter": "featureCompatibility"}, "")
}
