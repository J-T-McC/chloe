package mdb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"chloe/internal/model"
	"chloe/internal/tunnel"
)

// Integration tests run against the Docker MongoDB with the seed loaded:
//   docker exec -i chloe-mongo mongosh -u root -p chloe --quiet < testdata/seed_mongo.js
//   CHLOE_IT=1 go test ./internal/mdb

func open(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run against the Docker MongoDB")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Open(ctx, model.Connection{Type: model.MongoDB, Host: "127.0.0.1", Port: 37017, User: "root", Password: "chloe"}, tunnel.NewDirect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestBrowse(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	if !strings.HasPrefix(c.ServerVersion(), "MongoDB 8") {
		t.Errorf("version %q", c.ServerVersion())
	}
	dbs, err := c.Databases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range dbs {
		names[d.Name] = true
	}
	if !names["shop"] || !names["analytics"] || !names["admin"] {
		t.Errorf("databases %v", names)
	}
	cs, err := c.Collections(ctx, "shop", true)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]CollInfo{}
	for _, ci := range cs {
		byName[ci.Name] = ci
	}
	if o := byName["orders"]; o.Count != 60 || o.Indexes != 3 || o.Size == 0 {
		t.Errorf("orders %+v", o)
	}
	if v := byName["paid_orders"]; v.Type != "view" || v.ViewOn != "orders" {
		t.Errorf("view %+v", v)
	}
	if e := byName["events"]; !e.Capped {
		t.Errorf("events %+v", e)
	}
	if _, ok := byName["system.views"]; ok {
		t.Error("system.views listed")
	}
	ix, err := c.Indexes(ctx, "shop", "orders")
	if err != nil || len(ix) != 3 || ix[1].Keys[1] != (IndexKey{Field: "placed_at", Type: "-1"}) || ix[2].Partial != "{ status: 'new' }" {
		t.Errorf("indexes %+v %v", ix, err)
	}
}

func TestFindAndEdit(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	p, err := c.Find(ctx, Query{DB: "shop", Coll: "orders", Filter: `{status: "paid", customer_id: {$in: [1, 2]}}`,
		Sort: `{placed_at: -1}`, Projection: `{items: 0}`, Limit: 5}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Docs) != 5 || p.Total != 10 || p.Fields[0].Name != "_id" {
		t.Fatalf("page %d docs, total %d, fields %v", len(p.Docs), p.Total, p.Fields)
	}
	for _, f := range p.Fields {
		if f.Name == "items" {
			t.Error("projection ignored")
		}
	}
	if _, err := c.Find(ctx, Query{DB: "shop", Coll: "orders", Filter: `{status: }`}, false); err == nil || !strings.Contains(err.Error(), "filter") {
		t.Errorf("bad filter: %v", err)
	}

	coll := "it_edit"
	_ = c.DropCollection(ctx, "shop", coll)
	defer c.DropCollection(ctx, "shop", coll)
	n, err := c.InsertDocs(ctx, "shop", coll, `[{_id: 1, a: "x", sub: {b: 1}}, {_id: 2, a: "y"}]`)
	if err != nil || n != 2 {
		t.Fatalf("insert %d %v", n, err)
	}
	id := Val{T: "int", V: "1"}
	if err := c.EditField(ctx, "shop", coll, FieldEdit{ID: id, Path: "sub.b", Value: Val{T: "long", V: "42"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.EditField(ctx, "shop", coll, FieldEdit{ID: id, Path: "a", Unset: true}); err != nil {
		t.Fatal(err)
	}
	d, err := c.GetDoc(ctx, "shop", coll, id)
	if err != nil || Format(d, "", false) != "{ _id: 1, sub: { b: NumberLong('42') } }" {
		t.Errorf("after edit %s %v", Format(d, "", false), err)
	}
	if err := c.ReplaceDoc(ctx, "shop", coll, id, `{c: ISODate("2024-01-01")}`); err != nil {
		t.Fatal(err)
	}
	d, _ = c.GetDoc(ctx, "shop", coll, id)
	if got := Format(d, "", false); got != "{ _id: 1, c: ISODate('2024-01-01T00:00:00.000Z') }" {
		t.Errorf("after replace %s", got)
	}
	if err := c.ReplaceDoc(ctx, "shop", coll, Val{T: "int", V: "99"}, `{}`); err == nil {
		t.Error("replacing a missing document succeeded")
	}
	if n, err := c.DeleteDocs(ctx, "shop", coll, []Val{id, {T: "int", V: "2"}}); err != nil || n != 2 {
		t.Errorf("delete %d %v", n, err)
	}

	if err := c.CreateIndex(ctx, "shop", coll, IndexSpec{Keys: []IndexKey{{Field: "c", Type: "-1"}}, Sparse: true}); err != nil {
		t.Fatal(err)
	}
	ix, _ := c.Indexes(ctx, "shop", coll)
	if len(ix) != 2 || ix[1].Name != "c_-1" || !ix[1].Sparse {
		t.Errorf("indexes %+v", ix)
	}
	if err := c.DropIndex(ctx, "shop", coll, "c_-1"); err != nil {
		t.Error(err)
	}
	if err := c.SetValidation(ctx, "shop", coll, CollOptions{Validator: `{c: {$exists: true}}`, ValidationAction: "error"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertDocs(ctx, "shop", coll, `{d: 1}`); err == nil || err.Error() != "Document failed validation" {
		t.Errorf("validator: %v", err)
	}
	if _, err := c.InsertDocs(ctx, "shop", "customers", `{full_name: "x"}`); err == nil || err.Error() != "Document failed validation: missing email" {
		t.Errorf("schema validator: %v", err)
	}
}

func TestCollectionAdmin(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	db := "chloe_it_admin"
	_ = c.DropDatabase(ctx, db)
	defer c.DropDatabase(ctx, db)
	if err := c.CreateDatabase(ctx, db, "first"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateCollection(ctx, db, "capped", CollOptions{Capped: true, Size: 4096, Max: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.InsertDocs(ctx, db, "first", `[{a: 1}, {a: 2}]`); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateIndex(ctx, db, "first", IndexSpec{Keys: []IndexKey{{Field: "a"}}, Unique: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateCollection(ctx, db, "v", CollOptions{ViewOn: "first", Pipeline: `[{$match: {a: 2}}]`}); err != nil {
		t.Fatal(err)
	}
	if err := c.DuplicateCollection(ctx, db, "first", "copy", true); err != nil {
		t.Fatal(err)
	}
	if err := c.RenameCollection(ctx, db, "copy", "copy2"); err != nil {
		t.Fatal(err)
	}
	cs, _ := c.Collections(ctx, db, true)
	got := map[string]CollInfo{}
	for _, ci := range cs {
		got[ci.Name] = ci
	}
	if got["copy2"].Count != 2 || got["copy2"].Indexes != 2 || !got["capped"].Capped || got["v"].Type != "view" {
		t.Errorf("collections %+v", got)
	}
	if n, err := c.EmptyCollection(ctx, db, "copy2"); err != nil || n != 2 {
		t.Errorf("empty %d %v", n, err)
	}
	if err := c.SetViewPipeline(ctx, db, "v", "first", `[{$match: {a: 1}}]`); err != nil {
		t.Error(err)
	}
}

func run(t *testing.T, s *Shell, script string) RunOutput {
	t.Helper()
	out := s.Run(context.Background(), script, 20)
	if out.Error != "" {
		t.Fatalf("%s\n-> %s", script, out.Error)
	}
	return out
}

func TestShell(t *testing.T) {
	c := open(t)
	s := c.NewShell("shop")
	out := run(t, s, `db.orders.find({}).sort({placed_at: 1}).limit(25)`)
	r := out.Results[0]
	if r.Kind != "documents" || len(r.Page.Docs) != 20 || !r.HasMore || r.Collection != "orders" {
		t.Fatalf("cursor result %+v", r)
	}
	p, more, err := s.Fetch(context.Background(), r.CursorID, 20, 20)
	if err != nil || len(p.Docs) != 5 || more {
		t.Errorf("second page %d %v %v", len(p.Docs), more, err)
	}

	out = run(t, s, `
		const n = db.orders.countDocuments({customer_id: 1});
		print("orders of 1:", n);
		db.customers.findOne({_id: 1}).address.city
		db.orders.aggregate([{$group: {_id: "$status", n: {$sum: 1}}}, {$sort: {_id: 1}}])
		db.orders.distinct("status").sort()
		use analytics
		db.pageviews.countDocuments()
		db.getName()
	`)
	if out.Output != "orders of 1: 15\n" {
		t.Errorf("output %q", out.Output)
	}
	vals := []string{}
	for _, r := range out.Results {
		switch r.Kind {
		case "value":
			vals = append(vals, Format(*r.Value, "", false))
		case "text":
			vals = append(vals, r.Text)
		case "documents":
			vals = append(vals, Format(r.Page.Docs[0], "", false))
		}
	}
	want := []string{"'London'", "{ _id: 'new', n: 20 }", "[ 'new', 'paid', 'shipped' ]", "switched to db analytics", "30", "'analytics'"}
	if strings.Join(vals, " | ") != strings.Join(want, " | ") {
		t.Errorf("results\n got %q\nwant %q", vals, want)
	}
	if out.DB != "analytics" {
		t.Errorf("db %s", out.DB)
	}

	// Writes, cursors from JavaScript, BSON types and errors.
	out = run(t, s, `
		use chloe_it_shell
		db.dropDatabase();
		db.things.insertMany([{_id: 1, n: NumberLong(5), at: ISODate("2024-01-01T00:00:00Z")}, {_id: 2, n: 7}]);
		db.things.updateOne({_id: 2}, {$inc: {n: 1}}, {upsert: true})
		let total = 0;
		db.things.find().forEach(d => { total += Number(d.n); });
		total
		db.things.find({}, {_id: 1}).map(d => d._id * 10).toArray()
		db.things.findOne({_id: 1}).n instanceof NumberLong
		db.things.findOneAndUpdate({_id: 1}, {$set: {tag: "x"}}, {returnDocument: "after"}).tag
		db.things.find().sort({_id: -1}).limit(1).next()._id
		db.things.createIndex({at: -1}, {expireAfterSeconds: 3600})
		db.things.getIndexes().length
		db.things.deleteMany({})
		db.dropDatabase()
	`)
	vals = vals[:0]
	for _, r := range out.Results[3:] { // after use, dropDatabase and insertMany
		if r.Value != nil {
			vals = append(vals, Format(*r.Value, "", false))
		} else if r.Page != nil {
			vals = append(vals, Format(r.Page.Docs[0], "", false))
		}
	}
	want = []string{
		"{ acknowledged: true, insertedId: null, matchedCount: 1, modifiedCount: 1, upsertedCount: 0 }",
		"13", "[ 10, 20 ]", "true", "'x'", "2", "'at_-1'", "2",
		"{ acknowledged: true, deletedCount: 2 }",
	}
	for i, w := range want {
		if i >= len(vals) || vals[i] != w {
			t.Errorf("write results\n got %q\nwant %q", vals, want)
			break
		}
	}

	errOut := s.Run(context.Background(), "db.orders.find({$bad: 1}).toArray()", 20)
	if !strings.Contains(errOut.Error, "$bad") {
		t.Errorf("server error %q", errOut.Error)
	}
	errOut = s.Run(context.Background(), "let a = 1;\nnosuch.call()", 20)
	if !strings.Contains(errOut.Error, "nosuch") || !strings.Contains(errOut.Error, "line 2") {
		t.Errorf("reference error %q", errOut.Error)
	}

	// A running script stops when its context is cancelled.
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	stopped := s.Run(ctx, "while (true) {}", 20)
	if stopped.Error == "" || time.Since(start) > 3*time.Second {
		t.Errorf("not interrupted: %q", stopped.Error)
	}
	// and the shell still works afterwards
	if out := s.Run(context.Background(), "1 + 1", 20); out.Error != "" || out.Results[0].Value.V != "2" {
		t.Errorf("after interrupt %+v", out)
	}

	show := run(t, s, "use shop\nshow collections")
	if !strings.Contains(show.Results[1].Text, "orders\npaid_orders\nproducts") {
		t.Errorf("show collections %q", show.Results[1].Text)
	}
	_ = bson.D{}
}

func TestShellReviewFixes(t *testing.T) {
	c := open(t)
	s := c.NewShell("chloe_it_fixes")
	defer c.DropDatabase(context.Background(), "chloe_it_fixes")
	out := run(t, s, `
		db.src.insertMany([{n: 1}, {n: 2}, {n: 3}, {n: 4}, {n: 5}]);
		if (true) { db.src.aggregate([{ $match: { n: { $gt: 2 } } }, { $out: "dst" }]) }
		var r = db.src.aggregate([{ $merge: { into: "dst2" } }]);
		const t = `+"`"+`
use other
show dbs
`+"`"+`;
		[db.dst.countDocuments(), db.dst2.countDocuments(), db.getName(), t.trim().split("\n").length]
		db.src.aggregate([{ $sort: { n: 1 } }]).skip(1).limit(2)
		db.src.find({}, { n: 1 })
		db.src.find().map(d => d.n);
		(function () { return db.src.countDocuments() })()
	`)
	vals := func(i int) string {
		r := out.Results[i]
		if r.Value != nil {
			return Format(*r.Value, "", false)
		}
		docs := make([]string, len(r.Page.Docs))
		for j, d := range r.Page.Docs {
			docs[j] = Format(d, "", false)
		}
		return strings.Join(docs, " ")
	}
	n := len(out.Results)
	if got := vals(n - 5); got != "[ 3, 5, 'chloe_it_fixes', 2 ]" {
		t.Errorf("$out/$merge ran lazily or use was rewritten in a string: %s", got)
	}
	if r := out.Results[n-4]; len(r.Page.Docs) != 2 || r.Editable || !strings.Contains(vals(n-4), "n: 2") || !strings.Contains(vals(n-4), "n: 3") {
		t.Errorf("aggregate skip/limit: %s editable=%v", vals(n-4), r.Editable)
	}
	if r := out.Results[n-3]; !r.Editable || r.Collection != "src" {
		t.Errorf("find should be editable: %+v", r)
	}
	if r := out.Results[n-2]; r.Editable {
		t.Errorf("mapped results are not documents of the collection")
	}
	if r := out.Results[n-1]; r.Statement != "(function () { return db.src.countDocuments() })()" || vals(n-1) != "5" {
		t.Errorf("parenthesised statement: %q %s", r.Statement, vals(n-1))
	}
	if e := s.Run(context.Background(), `db.src.aggregate([{ $out: "x" }]).sort({ n: 1 })`, 20).Error; !strings.Contains(e, "$out or $merge") {
		t.Errorf("sort after $out: %q", e)
	}
}

func TestIndexReplace(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	const db = "chloe_it_index"
	_ = c.DropDatabase(ctx, db)
	defer c.DropDatabase(ctx, db)
	s := c.NewShell(db)
	run(t, s, `
		db.p.insertMany([{ name: "Ada", bio: "math" }, { name: "ada", bio: "engines" }]);
		db.p.createIndex({ bio: "text", name: "text" }, { name: "txt", weights: { name: 5 } });
		db.p.createIndex({ name: 1 }, { name: "ci", collation: { locale: "en", strength: 2 }, partialFilterExpression: { bio: { $exists: true } } });
	`)
	byName := func() map[string]IndexInfo {
		ix, err := c.Indexes(ctx, db, "p")
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]IndexInfo{}
		for _, x := range ix {
			m[x.Name] = x
		}
		return m
	}
	spec := func(x IndexInfo) IndexSpec {
		return IndexSpec{Name: x.Name, Keys: x.Keys, Unique: x.Unique, Sparse: x.Sparse, Hidden: x.Hidden, TTL: x.TTL, TTLSet: x.TTL >= 0, Partial: x.Partial, Base: &x.Spec}
	}
	m := byName()
	// Replacing a text index with only "unique" changed keeps its keys and weights.
	sp := spec(m["txt"])
	sp.Sparse = true
	if err := c.ReplaceIndex(ctx, db, "p", "txt", sp); err != nil {
		t.Fatalf("text index: %v", err)
	}
	if x := byName()["txt"]; !x.Sparse || !strings.Contains(Format(x.Spec, "", false), "weights: { bio: 1, name: 5 }") {
		t.Errorf("text index after replace: %s", Format(x.Spec, "", false))
	}
	// Making the collation index unique keeps the collation.
	sp = spec(m["ci"])
	sp.Unique = true
	if err := c.ReplaceIndex(ctx, db, "p", "ci", sp); err == nil {
		t.Error("unique on case-insensitive duplicates should fail")
	}
	if x, ok := byName()["ci"]; !ok || x.Unique || x.Collation == "" {
		t.Errorf("the old index must be restored with its collation: %+v", x)
	}
	// A bad partial filter is refused before anything is dropped.
	sp = spec(m["ci"])
	sp.Partial = "{ bio: "
	if err := c.ReplaceIndex(ctx, db, "p", "ci", sp); err == nil || !strings.Contains(err.Error(), "partial filter") {
		t.Errorf("bad partial: %v", err)
	}
	if _, ok := byName()["ci"]; !ok {
		t.Error("index dropped by a failed edit")
	}
	h := true
	if err := c.ModifyIndex(ctx, db, "p", "ci", &h, nil); err != nil || !byName()["ci"].Hidden {
		t.Errorf("hide: %v", err)
	}
	if err := c.SetValidation(ctx, db, "p", CollOptions{Validator: `{ name: { $type: "string" } }`, ValidationLevel: "moderate", ValidationAction: "warn"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetValidation(ctx, db, "p", CollOptions{Validator: `{ name: { $type: "string" } }`}); err != nil {
		t.Fatal(err)
	}
	cs, _ := c.Collections(ctx, db, false)
	if o := Format(cs[0].Options, "", false); !strings.Contains(o, "validationLevel: 'strict'") || !strings.Contains(o, "validationAction: 'error'") {
		t.Errorf("default validation level/action not restored: %s", o)
	}
}
