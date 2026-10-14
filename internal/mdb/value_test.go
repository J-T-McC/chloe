package mdb

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestValRoundTrip(t *testing.T) {
	oid := bson.NewObjectID()
	dec, _ := bson.ParseDecimal128("12.3400")
	doc := bson.D{
		{Key: "_id", Value: oid},
		{Key: "s", Value: "héllo"},
		{Key: "i", Value: int32(-7)},
		{Key: "l", Value: int64(1) << 60},
		{Key: "d", Value: 2.0},
		{Key: "nan", Value: math.NaN()},
		{Key: "dec", Value: dec},
		{Key: "b", Value: true},
		{Key: "n", Value: nil},
		{Key: "at", Value: bson.NewDateTimeFromTime(time.Date(2024, 2, 29, 13, 4, 5, 123e6, time.UTC))},
		{Key: "bin", Value: bson.Binary{Subtype: 0, Data: []byte{0, 1, 255}}},
		{Key: "uuid", Value: bson.Binary{Subtype: 4, Data: []byte("0123456789abcdef")}},
		{Key: "re", Value: bson.Regex{Pattern: "^a.c$", Options: "i"}},
		{Key: "ts", Value: bson.Timestamp{T: 1700000000, I: 3}},
		{Key: "min", Value: bson.MinKey{}},
		{Key: "code", Value: bson.JavaScript("function(){}")},
		{Key: "sub", Value: bson.D{{Key: "a", Value: bson.A{int32(1), "x", bson.D{{Key: "z", Value: false}}}}}},
	}
	back, err := FromVal(ToVal(doc))
	if err != nil {
		t.Fatal(err)
	}
	got := back.(bson.D)
	for i, e := range doc {
		if e.Key == "nan" {
			if f, ok := got[i].Value.(float64); !ok || !math.IsNaN(f) {
				t.Errorf("nan: %#v", got[i].Value)
			}
			continue
		}
		if got[i].Key != e.Key || !reflect.DeepEqual(got[i].Value, e.Value) {
			t.Errorf("%s: got %#v want %#v", e.Key, got[i].Value, e.Value)
		}
	}
}

func TestFormat(t *testing.T) {
	v := ToVal(bson.D{
		{Key: "_id", Value: mustOID("65a1b2c3d4e5f60718293a4b")},
		{Key: "name", Value: "it's"},
		{Key: "n", Value: int64(5)},
		{Key: "x", Value: 1.0},
		{Key: "tags", Value: bson.A{"a"}},
		{Key: "odd key", Value: bson.D{}},
	})
	want := `{ _id: ObjectId('65a1b2c3d4e5f60718293a4b'), name: 'it\'s', n: NumberLong('5'), x: 1.0, tags: [ 'a' ], 'odd key': {} }`
	if got := Format(v, "", false); got != want {
		t.Errorf("mongosh style:\n got %s\nwant %s", got, want)
	}
	if got := Format(v, "  ", true); !strings.Contains(got, "\n  \"_id\": ObjectId(\"65a1b2c3d4e5f60718293a4b\"),\n") {
		t.Errorf("json style:\n%s", got)
	}
}

func mustOID(h string) bson.ObjectID {
	id, err := bson.ObjectIDFromHex(h)
	if err != nil {
		panic(err)
	}
	return id
}

func TestEvalShell(t *testing.T) {
	d, err := EvalDoc(`{
		_id: ObjectId("65a1b2c3d4e5f60718293a4b"), at: ISODate("2024-01-02T03:04:05Z"),
		n: 1, big: 3000000000, f: 1.5, l: NumberLong("9007199254740993"), i: NumberInt(7),
		dec: NumberDecimal("0.1"), re: /ab+c/i, u: UUID("01234567-89ab-cdef-0123-456789abcdef"),
		arr: [1, "two", null], nested: { "a.b": true }, ts: Timestamp(5, 6), dbl: Double(2),
	}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"_id": mustOID("65a1b2c3d4e5f60718293a4b"),
		"at":  bson.NewDateTimeFromTime(time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)),
		"n":   int32(1), "big": 3e9, "f": 1.5, "l": int64(9007199254740993), "i": int32(7),
		"re":  bson.Regex{Pattern: "ab+c", Options: "i"},
		"u":   bson.Binary{Subtype: 4, Data: []byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef}},
		"arr": bson.A{int32(1), "two", nil}, "nested": bson.D{{Key: "a.b", Value: true}},
		"ts": bson.Timestamp{T: 5, I: 6}, "dbl": 2.0,
	}
	for _, e := range d {
		if w, ok := want[e.Key]; ok && !reflect.DeepEqual(e.Value, w) {
			t.Errorf("%s: got %#v want %#v", e.Key, e.Value, w)
		}
	}
	if dec, ok := d[7].Value.(bson.Decimal128); !ok || dec.String() != "0.1" {
		t.Errorf("dec: %#v", d[7].Value)
	}
	for _, bad := range []string{`{a: ObjectId("zz")}`, `{a: }`, `[1, 2]`, `{a: NumberLong("x")}`} {
		if _, err := EvalDoc(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// Literals are evaluated with a time limit.
	defer func(d time.Duration) { evalTimeout = d }(evalTimeout)
	evalTimeout = 100 * time.Millisecond
	if _, err := EvalShell(`(function(){ while(true){} })()`); err == nil {
		t.Error("endless loop not interrupted")
	}
}

func TestRewrite(t *testing.T) {
	src := "use shop\nvar x = 1;\ndb.orders.find({a: x})\nif (x) { db.a.drop() }\nshow collections;\nprint('é'); db.b.count()"
	out, stmts, err := rewrite(src)
	if err != nil {
		t.Fatal(err)
	}
	wantStmts := []string{"use shop", "db.orders.find({a: x})", "show collections", "print('é')", "db.b.count()"}
	if !reflect.DeepEqual(stmts, wantStmts) {
		t.Errorf("statements %q", stmts)
	}
	for _, frag := range []string{`__result((__use("shop")), 0)`, `__result((db.orders.find({a: x})), 1)`, "{ db.a.drop() }", `__result((db.b.count()), 4)`} {
		if !strings.Contains(out, frag) {
			t.Errorf("missing %q in\n%s", frag, out)
		}
	}
	if _, _, err := rewrite("db.a.find({"); err == nil {
		t.Error("syntax error not reported")
	}
}

func TestPageOf(t *testing.T) {
	p := PageOf([]bson.D{
		{{Key: "a", Value: int32(1)}, {Key: "_id", Value: int32(1)}},
		{{Key: "_id", Value: int32(2)}, {Key: "a", Value: "x"}, {Key: "b", Value: true}},
		{{Key: "_id", Value: int32(3)}, {Key: "a", Value: "y"}},
	})
	var names []string
	for _, f := range p.Fields {
		names = append(names, f.Name+":"+f.Type)
	}
	if strings.Join(names, ",") != "_id:int,a:string,b:bool" {
		t.Errorf("fields %v", names)
	}
}

func TestIndexDoc(t *testing.T) {
	d, err := IndexDoc(IndexSpec{Keys: []IndexKey{{Field: "a", Type: "1"}, {Field: "b", Type: "-1"}}, Unique: true, TTLSet: true, TTL: 60, Partial: `{a: {$gt: 5}}`})
	if err != nil {
		t.Fatal(err)
	}
	got := Format(ToVal(d), "", false)
	want := `{ key: { a: 1, b: -1 }, name: 'a_1_b_-1', unique: true, expireAfterSeconds: 60, partialFilterExpression: { a: { $gt: 5 } } }`
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
