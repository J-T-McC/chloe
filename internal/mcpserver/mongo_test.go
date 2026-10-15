package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func doc(t *testing.T, s string) bson.D {
	t.Helper()
	var d Doc
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		t.Fatal(err)
	}
	v, err := d.bson()
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDocKeepsKeyOrder(t *testing.T) {
	d := doc(t, `{"z": 1, "a": {"y": 2, "b": 3}, "m": {"$oid": "64b7f0c2a1b2c3d4e5f60718"}}`)
	if d[0].Key != "z" || d[1].Key != "a" || d[1].Value.(bson.D)[0].Key != "y" {
		t.Fatalf("order lost: %v", d)
	}
	if _, ok := d[2].Value.(bson.ObjectID); !ok {
		t.Fatalf("$oid not parsed: %T", d[2].Value)
	}
	var bad Doc
	if json.Unmarshal([]byte(`[1]`), &bad) == nil {
		t.Fatal("array accepted as a document")
	}
}

func TestCheckMongo(t *testing.T) {
	for _, ok := range []string{
		`{"status": "open", "total": {"$gt": 5}, "$expr": {"$eq": ["$a", "$b"]}}`,
		`{"p": [{"$match": {"a": 1}}, {"$lookup": {"from": "orders", "localField": "_id", "foreignField": "cid", "as": "o"}}, {"$unionWith": "archive"}, {"$unionWith": {"coll": "x", "pipeline": []}}]}`,
		`{"note": "$where is just text here"}`,
		// Server-side JavaScript and system collections are the database user's business.
		`{"$where": "this.a > 1"}`,
		`{"$expr": {"$function": {"body": "return 1", "args": [], "lang": "js"}}}`,
		`{"p": [{"$lookup": {"from": "system.users", "as": "u"}}, {"$unionWith": "system.js"}]}`,
	} {
		if err := checkMongo(doc(t, ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for s, want := range map[string]string{
		`{"p": [{"$out": "copy"}]}`:                                                          "writes",
		`{"p": [{"$facet": {"a": [{"$merge": {"into": "x"}}]}}]}`:                            "writes",
		`{"p": [{"$lookup": {"from": "o", "as": "o", "pipeline": [{"$out": "x"}]}}]}`:        "writes",
		`{"p": [{"$currentOp": {}}]}`:                                                        "isn't allowed",
		`{"p": [{"$changeStream": {}}]}`:                                                     "isn't allowed",
		`{"p": [{"$lookup": {"from": {"db": "admin", "coll": "system.users"}, "as": "u"}}]}`: "same database",
		`{"p": [{"$unionWith": {"db": "other", "coll": "x"}}]}`:                              "same database",
		`{"p": [{"$graphLookup": {"from": {"db": "x", "coll": "y"}}}]}`:                      "same database",
	} {
		err := checkMongo(doc(t, s))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestCheckUpdate(t *testing.T) {
	for _, ok := range []string{
		`{"$set": {"a": 1, "b": {"c": [1, {"d": 2}]}}, "$inc": {"n": 2}}`,
		`{"$push": {"tags": {"$each": ["a", "b"], "$slice": -5}}}`,
		`{"$addToSet": {"tags": {"$each": ["x"]}}}`,
		`{"$pull": {"scores": {"$lt": 5}}}`,
		`{"$currentDate": {"seen": {"$type": "date"}}}`,
		`{"$set": {"when": {"$date": "2026-01-01T00:00:00Z"}}}`,
	} {
		if err := checkUpdate(doc(t, ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for s, want := range map[string]string{
		`{"$set": {"n": {"$add": [1, 2]}}}`:                   "not evaluated",
		`{"$set": {"a": {"b": [{"$concat": ["x"]}]}}}`:        "not evaluated",
		`{"$push": {"tags": {"$each": [{"$toUpper": "a"}]}}}`: "not evaluated",
		`{"$set": 5}`: "takes a document of fields",
	} {
		err := checkUpdate(doc(t, s))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", s, err)
		}
	}
	for s, want := range map[string]string{
		`{}`:                   "at least one operator",
		`{"name": "replaced"}`: "not a replacement document",
		`{"$where": "1"}`:      "isn't supported",
	} {
		err := checkUpdate(doc(t, s))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", s, err)
		}
	}
}

func TestOnlyIDs(t *testing.T) {
	f := onlyIDs(bson.D{{Key: "a", Value: 1}}, bson.A{int32(1), int32(2)})
	if got := extJSON(f); got != `{"$and":[{"a":1},{"_id":{"$in":[1,2]}}]}` {
		t.Fatal(got)
	}
	if got := extJSON(bson.NewObjectIDFromTimestamp(time0())); !strings.HasPrefix(got, `{"$oid":"`) {
		t.Fatal(got)
	}
	if got := idList(bson.A{1, 2, 3, 4, 5, 6, 7}); got != "\n// _id: [1, 2, 3, 4, 5] and 2 more" {
		t.Fatal(got)
	}
	cut := false
	if v := cutDoc(bson.D{{Key: "s", Value: strings.Repeat("x", 5000)}}, &cut).(bson.D); !cut || !strings.HasSuffix(v[0].Value.(string), "…[904 more bytes]") {
		t.Fatal("long string not cut")
	}
}

func time0() time.Time { return time.Unix(0, 0) }
