package sqlite

import (
	"slices"
	"testing"
)

func TestParseCreateTable(t *testing.T) {
	tb, err := parseCreateTable(`CREATE TABLE IF NOT EXISTS "main"."line items" (
  [order id] INTEGER NOT NULL,
  ` + "`sku`" + ` UNSIGNED BIG INT,
  qty INT DEFAULT -1 CHECK (qty <> 0),
  price NUMERIC(10, 2) DEFAULT (0.5 * 2),
  total REAL GENERATED ALWAYS AS (qty * price) STORED,
  label TEXT AS (upper(sku)),
  note, -- typeless
  CONSTRAINT pk PRIMARY KEY ("order id", sku),
  FOREIGN KEY ("order id") REFERENCES orders ON DELETE SET NULL DEFERRABLE INITIALLY DEFERRED,
  UNIQUE (sku, qty) ON CONFLICT REPLACE
) WITHOUT ROWID, STRICT`)
	if err != nil {
		t.Fatal(err)
	}
	if tb.Name != "line items" || len(tb.Columns) != 7 || !tb.Options.WithoutRowid || !tb.Options.Strict {
		t.Fatalf("%+v", tb)
	}
	c := tb.Columns
	if c[0].Name != "order id" || c[0].Nullable || c[1].Type != "UNSIGNED BIG INT" || *c[2].Default != "-1" ||
		c[3].Type != "NUMERIC" || c[3].Length != "10" || c[3].Scale != "2" || *c[3].Default != "(0.5 * 2)" ||
		c[4].GeneratedExpr != "qty * price" || !c[4].GeneratedStored || c[5].GeneratedExpr != "upper(sku)" || c[5].GeneratedStored || c[6].Type != "" {
		t.Fatalf("columns %+v", c)
	}
	if tb.PrimaryKey.Name != "pk" || !slices.Equal(tb.PrimaryKey.Columns, []string{"order id", "sku"}) {
		t.Fatalf("pk %+v", tb.PrimaryKey)
	}
	if len(tb.ForeignKeys) != 1 || tb.ForeignKeys[0].RefTable != "orders" || tb.ForeignKeys[0].OnDelete != "SET NULL" || len(tb.ForeignKeys[0].RefColumns) != 0 {
		t.Fatalf("fks %+v", tb.ForeignKeys)
	}
	if len(tb.Uniques) != 1 || len(tb.Checks) != 1 || tb.Checks[0].Expr != "qty <> 0" {
		t.Fatalf("uniques %+v checks %+v", tb.Uniques, tb.Checks)
	}
}

func TestParseTriggerAndView(t *testing.T) {
	tr, err := parseCreateTrigger(`CREATE TEMP TRIGGER IF NOT EXISTS t1 INSTEAD OF UPDATE OF a, b ON v WHEN (old.a IS NOT new.a) BEGIN SELECT 1; END`)
	if err != nil || tr.Timing != "INSTEAD OF" || tr.Events[0] != "UPDATE OF a, b" || tr.When != "(old.a IS NOT new.a)" || tr.Body != "BEGIN SELECT 1; END" {
		t.Fatalf("%+v %v", tr, err)
	}
	def, err := parseCreateView(`CREATE VIEW v (x, y) AS SELECT a AS x, b AS y FROM t`)
	if err != nil || def != "SELECT a AS x, b AS y FROM t" {
		t.Fatalf("%q %v", def, err)
	}
}
