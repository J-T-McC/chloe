package mdb

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCSVValue(t *testing.T) {
	cases := map[string]any{"12": int32(12), "007": "007", "3000000000": int64(3000000000), "1.5": 1.5, "true": true, "abc": "abc", "": ""}
	for in, want := range cases {
		if got := csvValue(in, true); got != want {
			t.Errorf("%q: %#v", in, got)
		}
	}
	if got := Format(ToVal(csvValue(`{"a":[1,{"$oid":"65a1b2c3d4e5f60718293a4b"}]}`, true)), "", false); got != "{ a: [ 1, ObjectId('65a1b2c3d4e5f60718293a4b') ] }" {
		t.Errorf("json cell %s", got)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	c := open(t)
	ctx := context.Background()
	const db = "chloe_it_transfer"
	_ = c.DropDatabase(ctx, db)
	defer c.DropDatabase(ctx, db)

	for _, format := range []string{"json", "jsonl", "csv"} {
		var buf bytes.Buffer
		n, err := c.Export(ctx, "shop", "orders", ExportOptions{Format: format, Filter: `{status: "paid"}`, Sort: `{placed_at: 1}`}, &buf, nil)
		if err != nil || n != 20 {
			t.Fatalf("%s export: %d %v", format, n, err)
		}
		coll := "orders_" + format
		in := format
		if in == "jsonl" {
			in = "json" // the reader takes an array or one document per line
		}
		res, err := c.Import(ctx, db, coll, ImportOptions{Format: in, Mode: "append", InferTypes: true}, bytes.NewReader(buf.Bytes()), nil)
		if err != nil || res.Read != 20 || res.Inserted != 20 {
			t.Fatalf("%s import: %+v %v\n%s", format, res, err, buf.String()[:min(400, buf.Len())])
		}
		p, err := c.Find(ctx, Query{DB: db, Coll: coll, Sort: `{placed_at: 1}`, Limit: 1}, true)
		if err != nil || p.Total != 20 {
			t.Fatalf("%s find: %v %v", format, p.Total, err)
		}
		got := Format(p.Docs[0], "", false)
		// CSV turns dates and decimals into text and flattens nothing here; JSON keeps every type.
		if format != "csv" && (!strings.Contains(got, "placed_at: ISODate(") || !strings.Contains(got, "total: NumberDecimal(")) {
			t.Errorf("%s types lost: %s", format, got)
		}
		if format == "csv" && !strings.Contains(got, "customer_id: 2") {
			t.Errorf("csv: %s", got)
		}
	}

	// upsert replaces documents with the same _id; append rejects them.
	in := `[{"_id": 1, "a": "one"}, {"_id": 2, "a": "two"}]`
	if _, err := c.Import(ctx, db, "u", ImportOptions{Format: "json", Mode: "append"}, strings.NewReader(in), nil); err != nil {
		t.Fatal(err)
	}
	res, err := c.Import(ctx, db, "u", ImportOptions{Format: "json", Mode: "append"}, strings.NewReader(`{"_id": 2, "a": "dup"}`+"\n"+`{"_id": 3, "a": "three"}`), nil)
	if err != nil || res.Inserted != 1 || res.Failed != 1 || len(res.Errors) != 1 {
		t.Errorf("append with a duplicate: %+v %v", res, err)
	}
	res, err = c.Import(ctx, db, "u", ImportOptions{Format: "json", Mode: "upsert"}, strings.NewReader(`[{"_id": 2, "a": "TWO"}, {"_id": 4, "a": "four"}]`), nil)
	if err != nil || res.Updated != 1 || res.Inserted != 1 {
		t.Errorf("upsert: %+v %v", res, err)
	}
	res, err = c.Import(ctx, db, "u", ImportOptions{Format: "csv", Mode: "copy", InferTypes: true, SkipEmpty: true}, strings.NewReader("_id,a,addr.city,n\n9,x,Paris,\n"), nil)
	if err != nil || res.Inserted != 1 {
		t.Fatalf("copy csv: %+v %v", res, err)
	}
	p, _ := c.Find(ctx, Query{DB: db, Coll: "u"}, true)
	if p.Total != 1 || Format(p.Docs[0], "", false) != "{ _id: 9, a: 'x', addr: { city: 'Paris' } }" {
		t.Errorf("after copy: %d %s", p.Total, Format(p.Docs[0], "", false))
	}
	if _, err := c.Import(ctx, db, "u", ImportOptions{Format: "json"}, strings.NewReader(`[{"a": 1}, 7]`), nil); err == nil {
		t.Error("a non-document was accepted")
	}
}
