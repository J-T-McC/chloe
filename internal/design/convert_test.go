package design

import (
	"strings"
	"testing"
)

func col(t *testing.T, tb Table, name string) Column {
	t.Helper()
	for _, c := range tb.Columns {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no column %s", name)
	return Column{}
}

func def(c Column) string {
	if c.Default == nil {
		return "<none>"
	}
	return *c.Default
}

func TestConvertMySQLToPostgres(t *testing.T) {
	src := Table{Name: "orders", Database: "shop",
		Columns: []Column{
			{Name: "id", Type: "int", Unsigned: true, AutoIncrement: true},
			{Name: "flag", Type: "tinyint", Length: "1", Default: new("1")},
			{Name: "qty", Type: "smallint", Default: new("0")},
			{Name: "total", Type: "decimal", Length: "10", Scale: "2", Default: new("0.00")},
			{Name: "note", Type: "varchar", Length: "200", Default: new(`'it''s a \\ path'`), Charset: "utf8mb4", Collation: "utf8mb4_bin"},
			{Name: "body", Type: "longtext"},
			{Name: "status", Type: "enum", Length: "'new','paid','shipped'", Default: new("'new'")},
			{Name: "created", Type: "datetime", Length: "6", Default: new("CURRENT_TIMESTAMP(6)"), OnUpdateNow: true},
			{Name: "photo", Type: "blob"},
			{Name: "meta", Type: "json"},
			{Name: "big", Type: "bigint", Unsigned: true},
			{Name: "bits", Type: "bit", Length: "1", Default: new("b'0'")},
		},
		PrimaryKey:  &PrimaryKey{Columns: []string{"id"}},
		Indexes:     []Index{{Name: "ft", Kind: "fulltext", Columns: []IndexColumn{{Name: "body"}}}, {Name: "by_note", Columns: []IndexColumn{{Name: "note", Length: "20"}}, Method: "BTREE"}},
		ForeignKeys: []ForeignKey{{Name: "fk_c", Columns: []string{"qty"}, RefDatabase: "shop", RefTable: "customers", RefColumns: []string{"id"}}},
		Triggers:    []Trigger{{Name: "tr", Timing: "BEFORE", Events: []string{"INSERT"}, Body: "SET NEW.qty = 1"}},
		Options:     Options{Engine: "InnoDB", Charset: "utf8mb4"},
	}
	out, notes := Convert(src, "mysql", "postgres")
	want := map[string]string{
		"id": "bigint", "flag": "boolean", "qty": "smallint", "total": "numeric(10,2)", "note": "character varying(200)", "body": "text",
		"status": "character varying(7)", "created": "timestamp without time zone", "photo": "bytea", "meta": "jsonb", "big": "bigint", "bits": "boolean",
	}
	for name, typ := range want {
		if got := TypeSQL(col(t, out, name)); got != typ {
			t.Errorf("%s: type %q, want %q", name, got, typ)
		}
	}
	if c := col(t, out, "id"); c.Identity != "by default" || c.AutoIncrement || c.Default != nil {
		t.Errorf("id: %+v", c)
	}
	for name, d := range map[string]string{"flag": "true", "qty": "0", "total": "0.00", "note": `'it''s a \ path'`, "status": "'new'", "created": "CURRENT_TIMESTAMP", "bits": "false"} {
		if got := def(col(t, out, name)); got != d {
			t.Errorf("%s: default %s, want %s", name, got, d)
		}
	}
	if c := col(t, out, "note"); c.Collation != "" || c.Charset != "" {
		t.Error("collation kept")
	}
	if len(out.Indexes) != 1 || out.Indexes[0].Columns[0].Length != "" || out.Indexes[0].Method != "" {
		t.Errorf("indexes %+v", out.Indexes)
	}
	if out.ForeignKeys[0].RefDatabase != "" || len(out.Triggers) != 0 || out.Options.Engine != "" {
		t.Errorf("fk/trigger/options %+v %+v %+v", out.ForeignKeys, out.Triggers, out.Options)
	}
	joined := strings.Join(notes, "\n")
	for _, n := range []string{"fulltext", "trigger tr", "ON UPDATE", "collation"} {
		if !strings.Contains(joined, n) {
			t.Errorf("no note about %s in:\n%s", n, joined)
		}
	}
	if _, err := (Postgres{}).Create(out); err != nil {
		t.Fatal(err)
	}
}

func TestConvertPostgresToMySQL(t *testing.T) {
	src := Table{Name: "customers", Schema: "public",
		Columns: []Column{
			{Name: "id", Type: "integer", Default: new("nextval('customers_id_seq'::regclass)")},
			{Name: "email", Type: "text"},
			{Name: "name", Type: "character varying", Default: new("'anon'::character varying")},
			{Name: "active", Type: "boolean", Default: new("true")},
			{Name: "joined", Type: "timestamp with time zone", Default: new("now()")},
			{Name: "tags", Type: "text[]", Default: new("'{}'::text[]")},
			{Name: "uid", Type: "uuid", Default: new("gen_random_uuid()")},
			{Name: "score", Type: "numeric"},
			{Name: "raw", Type: "bytea"},
		},
		PrimaryKey: &PrimaryKey{Name: "customers_pkey", Columns: []string{"id"}},
		Uniques:    []Unique{{Name: "customers_email_key", Columns: []string{"email"}}},
		Indexes:    []Index{{Name: "by_lower", Columns: []IndexColumn{{Expr: "lower(email)"}}}, {Name: "active_only", Columns: []IndexColumn{{Name: "joined"}}, Where: "active", Method: "btree"}},
	}
	out, notes := Convert(src, "postgres", "mysql")
	want := map[string]string{"id": "int", "email": "varchar(255)", "name": "longtext", "active": "tinyint(1)", "joined": "datetime(6)", "tags": "longtext", "uid": "char(36)", "score": "decimal(65,30)", "raw": "longblob"}
	for name, typ := range want {
		if got := TypeSQL(col(t, out, name)); got != typ {
			t.Errorf("%s: type %q, want %q", name, got, typ)
		}
	}
	if c := col(t, out, "id"); !c.AutoIncrement || c.Default != nil {
		t.Errorf("serial id: %+v", c)
	}
	for name, d := range map[string]string{"name": "('anon')", "active": "1", "joined": "CURRENT_TIMESTAMP(6)", "tags": "('{}')", "uid": "<none>"} {
		if got := def(col(t, out, name)); got != d {
			t.Errorf("%s: default %s, want %s", name, got, d)
		}
	}
	if out.PrimaryKey.Name != "" || src.PrimaryKey.Name == "" {
		t.Error("primary key name should be dropped from the copy only")
	}
	if len(out.Uniques) != 0 || len(out.Indexes) != 2 || out.Indexes[1].Kind != "unique" {
		t.Errorf("uniques should become unique indexes: %+v", out.Indexes)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "expression index") || !strings.Contains(strings.Join(notes, "\n"), "gen_random_uuid") {
		t.Errorf("notes:\n%s", strings.Join(notes, "\n"))
	}
	if _, err := (MySQL{}).Create(out); err != nil {
		t.Fatal(err)
	}
}

func TestConvertSQLite(t *testing.T) {
	src := Table{Name: "items",
		Columns: []Column{
			{Name: "id", Type: "INTEGER"},
			{Name: "name", Type: "VARCHAR", Length: "40", Default: new("'x'")},
			{Name: "price", Type: "REAL"},
			{Name: "added", Type: "DATETIME", Default: new("CURRENT_TIMESTAMP")},
			{Name: "blobby", Type: ""},
			{Name: "n", Type: "NUMERIC"},
			{Name: "ok", Type: "BOOLEAN", Default: new("1")},
		},
		PrimaryKey: &PrimaryKey{Columns: []string{"id"}},
	}
	pg, _ := Convert(src, "sqlite", "postgres")
	if c := col(t, pg, "id"); c.Identity != "by default" || c.Type != "bigint" {
		t.Errorf("INTEGER PRIMARY KEY: %+v", c)
	}
	for name, typ := range map[string]string{"name": "character varying(40)", "price": "double precision", "added": "timestamp without time zone", "blobby": "bytea", "n": "numeric", "ok": "boolean"} {
		if got := TypeSQL(col(t, pg, name)); got != typ {
			t.Errorf("%s: %q want %q", name, got, typ)
		}
	}
	if def(col(t, pg, "ok")) != "true" {
		t.Errorf("bool default %s", def(col(t, pg, "ok")))
	}

	// and back into SQLite from MySQL: auto increment stays on the INTEGER PRIMARY KEY
	my := Table{Name: "t", Columns: []Column{{Name: "id", Type: "bigint", AutoIncrement: true}, {Name: "v", Type: "varchar", Length: "10"}}, PrimaryKey: &PrimaryKey{Columns: []string{"id"}}}
	lite, _ := Convert(my, "mysql", "sqlite")
	if c := col(t, lite, "id"); c.Type != "INTEGER" || !c.AutoIncrement {
		t.Errorf("sqlite id %+v", c)
	}
	stmts, err := (SQLite{}).Create(lite)
	if err != nil || !strings.Contains(strings.Join(stmts, ";"), "INTEGER PRIMARY KEY AUTOINCREMENT") {
		t.Fatalf("%v %v", stmts, err)
	}
}

func TestConvertSameDialectKeepsEverything(t *testing.T) {
	src := Table{Name: "t", Columns: []Column{{Name: "a", Type: "int", Unsigned: true, Collation: "x"}}, Options: Options{Engine: "InnoDB"}, Triggers: []Trigger{{Name: "tr"}}}
	out, notes := Convert(src, "mysql", "mysql")
	if !out.Columns[0].Unsigned || out.Columns[0].Collation != "x" || out.Options.Engine != "InnoDB" || len(out.Triggers) != 1 || len(notes) != 0 {
		t.Fatalf("%+v %v", out, notes)
	}
}
