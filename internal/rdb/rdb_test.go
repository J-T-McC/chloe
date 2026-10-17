package rdb

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"chloe/internal/model"
	"chloe/internal/testdb"
	"chloe/internal/tunnel"
)

func TestSplitArgs(t *testing.T) {
	cases := map[string][]string{
		`SET k v`:                      {"SET", "k", "v"},
		`  set  "hello world"  'a b' `: {"set", "hello world", "a b"},
		`SET k "a\nb\x41"`:             {"SET", "k", "a\nbA"},
		`SET k 'it\'s'`:                {"SET", "k", "it's"},
		``:                             nil,
	}
	for in, want := range cases {
		got, err := SplitArgs(in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	if _, err := SplitArgs(`SET "open`); err == nil {
		t.Error("unbalanced quote accepted")
	}
}

func TestFormat(t *testing.T) {
	got := Format([]any{"a", int64(3), nil, []any{"x", "y"}}, "")
	want := "1) \"a\"\n2) (integer) 3\n3) (nil)\n4) 1) \"x\"\n   2) \"y\""
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if Format("OK", "") != "OK" || Format([]any{}, "") != "(empty array)" {
		t.Error("simple replies")
	}
}

func TestValueEncoding(t *testing.T) {
	bin := "\xff\x00\x01"
	v := Enc(bin)
	if !v.Base64 {
		t.Fatal("binary not base64")
	}
	if b, _ := v.Bytes(); b != bin {
		t.Fatal("round trip")
	}
	if v := Enc("héllo"); v.Base64 || v.Text != "héllo" {
		t.Fatal("utf8 text")
	}
}

func open(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("CHLOE_IT") == "" {
		t.Skip("set CHLOE_IT=1 to run integration tests")
	}
	c, err := Open(context.Background(), model.Connection{Type: model.Redis, Host: "127.0.0.1", Port: testdb.Redis, Password: "chloe"}, tunnel.NewDirect())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestIntegrationRedis(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	const db = 5
	if err := c.FlushDB(ctx, db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.FlushDB(ctx, db) })
	if !strings.HasPrefix(c.ServerVersion(), "Redis ") {
		t.Fatalf("version %q", c.ServerVersion())
	}
	key := func(s string) Value { return Value{Text: s} }

	// Create one key of each type.
	for _, nk := range []NewKey{
		{Name: key("s"), Type: "string", Value: key("hello"), TTL: 100},
		{Name: key("h"), Type: "hash", Field: key("f"), Value: key("1")},
		{Name: key("l"), Type: "list", Value: key("a")},
		{Name: key("st"), Type: "set", Value: key("m")},
		{Name: key("z"), Type: "zset", Value: key("p"), Score: 2.5},
		{Name: key("x"), Type: "stream", Field: key("kind"), Value: key("e1")},
	} {
		if err := c.CreateKey(ctx, db, nk); err != nil {
			t.Fatalf("create %s: %v", nk.Type, err)
		}
	}
	if err := c.CreateKey(ctx, db, NewKey{Name: key("s"), Type: "string"}); err == nil {
		t.Fatal("duplicate key accepted")
	}
	dbs, err := c.Databases(ctx)
	if err != nil || len(dbs) < 16 || dbs[db].Keys != 6 {
		t.Fatalf("databases %v %v", dbs, err)
	}
	res, err := c.ScanKeys(ctx, db, "0", "*", "", 100)
	if err != nil || len(res.Keys) != 6 || res.Cursor != "0" {
		t.Fatalf("scan %+v %v", res, err)
	}
	types := map[string]string{}
	for _, k := range res.Keys {
		types[k.Name.Text] = k.Type
	}
	if types["z"] != "zset" || types["x"] != "stream" {
		t.Fatalf("types %v", types)
	}
	if res, _ := c.ScanKeys(ctx, db, "0", "*", "hash", 100); len(res.Keys) != 1 {
		t.Fatalf("type filter %+v", res)
	}

	// String with TTL and binary content.
	kv, err := c.GetValue(ctx, db, key("s"), "", 0)
	if err != nil || kv.String.Text != "hello" || kv.TTL <= 0 || kv.TTL > 100 {
		t.Fatalf("string %+v %v", kv, err)
	}
	bin := Enc("\x00\xffbin")
	if err := c.SetString(ctx, db, key("s"), bin); err != nil {
		t.Fatal(err)
	}
	kv, _ = c.GetValue(ctx, db, key("s"), "", 0)
	if !kv.String.Base64 || kv.String.Text != bin.Text || kv.TTL <= 0 {
		t.Fatalf("binary/keepttl %+v", kv)
	}
	if err := c.SetTTL(ctx, db, key("s"), 0); err != nil {
		t.Fatal(err)
	}
	if kv, _ = c.GetValue(ctx, db, key("s"), "", 0); kv.TTL != -1 {
		t.Fatalf("persist %d", kv.TTL)
	}

	// Hash: add, rename a field, delete.
	f2, f3 := key("g"), key("f")
	c.EditItem(ctx, db, key("h"), "hash", ItemEdit{Field: &f2, Value: key("2")})
	f4 := key("renamed")
	if err := c.EditItem(ctx, db, key("h"), "hash", ItemEdit{Field: &f4, OldField: &f3, Value: key("1")}); err != nil {
		t.Fatal(err)
	}
	kv, _ = c.GetValue(ctx, db, key("h"), "", 0)
	if kv.Length != 2 || fieldNames(kv.Items) != "g,renamed" {
		t.Fatalf("hash %+v", kv.Items)
	}
	c.DeleteItems(ctx, db, key("h"), "hash", []ItemRef{{Field: &f2}})
	if kv, _ = c.GetValue(ctx, db, key("h"), "", 0); kv.Length != 1 {
		t.Fatalf("hash delete %d", kv.Length)
	}

	// List: push, set by index, delete by index, paging.
	c.PushList(ctx, db, key("l"), []Value{key("b"), key("c"), key("d")}, false)
	c.PushList(ctx, db, key("l"), []Value{key("first")}, true)
	one := int64(1)
	c.EditItem(ctx, db, key("l"), "list", ItemEdit{Index: &one, Value: key("A")})
	two := int64(2)
	if err := c.DeleteItems(ctx, db, key("l"), "list", []ItemRef{{Index: &two}}); err != nil {
		t.Fatal(err)
	}
	kv, _ = c.GetValue(ctx, db, key("l"), "", 2)
	if kv.Length != 4 || values(kv.Items) != "first,A" || kv.Cursor != "2" {
		t.Fatalf("list page1 %+v cursor %q", kv.Items, kv.Cursor)
	}
	kv, _ = c.GetValue(ctx, db, key("l"), kv.Cursor, 2)
	if values(kv.Items) != "c,d" || kv.Cursor != "" {
		t.Fatalf("list page2 %+v cursor %q", kv.Items, kv.Cursor)
	}

	// Set and sorted set: replace a member, change a score.
	om, nm := key("m"), key("n")
	c.EditItem(ctx, db, key("st"), "set", ItemEdit{Value: nm, OldValue: &om})
	if kv, _ = c.GetValue(ctx, db, key("st"), "", 0); values(kv.Items) != "n" {
		t.Fatalf("set %+v", kv.Items)
	}
	s := 9.0
	c.EditItem(ctx, db, key("z"), "zset", ItemEdit{Value: key("q"), Score: &s})
	if kv, _ = c.GetValue(ctx, db, key("z"), "", 0); values(kv.Items) != "p,q" || *kv.Items[1].Score != 9 {
		t.Fatalf("zset %+v", kv.Items)
	}

	// Stream: add, page, delete.
	id, err := c.AddStreamEntry(ctx, db, key("x"), "", []Field{{Field: &f3, Value: key("e2")}})
	if err != nil {
		t.Fatal(err)
	}
	kv, _ = c.GetValue(ctx, db, key("x"), "", 1)
	if len(kv.Entries) != 1 || kv.Cursor == "" {
		t.Fatalf("stream page %+v", kv)
	}
	kv, _ = c.GetValue(ctx, db, key("x"), kv.Cursor, 1)
	if len(kv.Entries) != 1 || kv.Entries[0].ID != id {
		t.Fatalf("stream page2 %+v", kv)
	}
	c.DeleteItems(ctx, db, key("x"), "stream", []ItemRef{{ID: id}})
	if kv, _ = c.GetValue(ctx, db, key("x"), "", 0); kv.Length != 1 {
		t.Fatalf("stream delete %d", kv.Length)
	}

	// Rename, delete.
	if err := c.RenameKey(ctx, db, key("l"), key("h")); err == nil {
		t.Fatal("rename over an existing key accepted")
	}
	if err := c.RenameKey(ctx, db, key("l"), key("list2")); err != nil {
		t.Fatal(err)
	}
	if n, _ := c.DeleteKeys(ctx, db, []Value{key("list2"), key("nope")}); n != 1 {
		t.Fatalf("deleted %d", n)
	}
	if _, err := c.GetValue(ctx, db, key("list2"), "", 0); err != ErrNoKey {
		t.Fatalf("expected ErrNoKey, got %v", err)
	}

	// Console keeps SELECT state.
	con, err := c.OpenConsole(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer con.Close()
	if out, _ := con.Exec(ctx, `SET "c k" "v 1"`); out != "OK" {
		t.Fatalf("console set %q", out)
	}
	if out, _ := con.Exec(ctx, `GET "c k"`); out != `"v 1"` {
		t.Fatalf("console get %q", out)
	}
	con.Exec(ctx, "SELECT 6")
	if out, _ := con.Exec(ctx, `GET "c k"`); out != "(nil)" || con.DB != 6 {
		t.Fatalf("console select %q db %d", out, con.DB)
	}
	if out, _ := con.Exec(ctx, "NOPE"); !strings.HasPrefix(out, "(error)") {
		t.Fatalf("console error %q", out)
	}

	// Server info.
	info, err := c.Info(ctx)
	if err != nil || len(info) < 5 || info[0].Name != "Server" {
		t.Fatalf("info %v", err)
	}
	cls, err := c.Clients(ctx)
	if err != nil || len(cls) == 0 || cls[0]["id"] == "" {
		t.Fatalf("clients %v %v", cls, err)
	}
	if cmds, err := c.Commands(ctx); err != nil || len(cmds) < 100 {
		t.Fatalf("commands %d %v", len(cmds), err)
	}
}

func fieldNames(fs []Field) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Field.Text)
	}
	sortStrings(out)
	return strings.Join(out, ",")
}

func values(fs []Field) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Value.Text)
	}
	return strings.Join(out, ",")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
