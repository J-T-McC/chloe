package mcpserver

import (
	"strings"
	"testing"

	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

var (
	mysqlD = driver.Dialect{Name: "mysql", IdentQuote: "`", Split: sqlsplit.MySQL}
	pgD    = driver.Dialect{Name: "postgres", IdentQuote: `"`, HasSchemas: true, DefaultSchema: "public", Split: sqlsplit.Postgres}
)

func table() model.TableDetail {
	return model.TableDetail{
		ObjectRef: model.ObjectRef{Database: "shop", Schema: "public", Name: "orders"},
		Columns:   []model.Column{{Name: "id", PrimaryKey: true, AutoIncrement: true}, {Name: "status"}, {Name: "total"}, {Name: "it's"}},
	}
}

func TestBuildStatements(t *testing.T) {
	b := newBuilder(pgD, table())
	q, err := b.selectRows([]string{"id"}, []Condition{{Column: "status", Op: "in", Value: []any{"paid", "sent"}}, {Column: "total", Op: "between", Value: []any{1.5, 10.0}}, {Column: "it's", Op: "is_null"}},
		[]Order{{Column: "total", Desc: true}}, 11, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT "id" FROM "public"."orders" WHERE "status" IN ($1, $2) AND "total" BETWEEN $3 AND $4 AND "it's" IS NULL ORDER BY "total" DESC LIMIT 11 OFFSET 20`
	if q.SQL != want || len(q.Args) != 4 || q.Args[2] != "1.5" {
		t.Fatalf("got %s %v", q.SQL, q.Args)
	}
	if d := display(pgD, q); !strings.Contains(d, `IN ('paid', 'sent')`) {
		t.Fatal(d)
	}

	ins, err := b.insert(map[string]any{"status": "new", "total": 3, "it's": true})
	if err != nil {
		t.Fatal(err)
	}
	if ins.SQL != `INSERT INTO "public"."orders" ("it's", "status", "total") VALUES ($1, $2, $3) RETURNING "id"` || ins.Args[0] != "true" {
		t.Fatalf("insert: %s %v", ins.SQL, ins.Args)
	}

	m := newBuilder(mysqlD, table())
	up, err := m.update(map[string]any{"status": "it's done"}, []Condition{{Column: "id", Op: "=", Value: 7.0}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if up.SQL != "UPDATE `shop`.`orders` SET `status` = ? WHERE `id` = ?" {
		t.Fatal(up.SQL)
	}
	// A ? inside a quoted identifier isn't a placeholder.
	odd := stmt{SQL: "SELECT `a?` FROM t WHERE x = ?", Args: []any{"it's"}}
	if d := display(mysqlD, odd); d != "SELECT `a?` FROM t WHERE x = 'it''s'" {
		t.Fatal(d)
	}
	if mi, _ := m.insert(map[string]any{"status": true}); strings.Contains(mi.SQL, "RETURNING") || mi.Args[0] != "1" {
		t.Fatalf("mysql insert: %s %v", mi.SQL, mi.Args)
	}

	for _, bad := range []struct {
		conds []Condition
		want  string
	}{
		{[]Condition{{Column: "status", Op: "=", Value: nil}}, "use is_null"},
		{[]Condition{{Column: "status", Op: "~", Value: "x"}}, "unknown operator"},
		{[]Condition{{Column: "status", Op: "in", Value: []any{}}}, "non-empty list"},
		{[]Condition{{Column: "status", Op: "between", Value: []any{1}}}, "two values"},
		{[]Condition{{Column: `status" OR 1=1 --`, Op: "=", Value: 1}}, "no column"},
	} {
		if _, err := b.delete(bad.conds, nil); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%v: got %v, want %q", bad.conds, err, bad.want)
		}
	}
	if _, err := b.delete(nil, nil); err == nil {
		t.Error("delete without where")
	}
}

func TestBindArrays(t *testing.T) {
	td := model.TableDetail{ObjectRef: model.ObjectRef{Schema: "shop", Name: "products"}, Columns: []model.Column{
		{Name: "id", PrimaryKey: true}, {Name: "tags", Type: "text[]", DataType: "_text"}, {Name: "grid", Type: "integer[]", DataType: "_int4"},
		{Name: "attrs", Type: "jsonb", DataType: "jsonb"}}}
	b := newBuilder(pgD, td)
	ins, err := b.insert(map[string]any{"tags": []any{"usb", `say "hi"`, `back\slash`, nil}, "grid": []any{[]any{1.0, 2.0}, []any{3.0, 4.0}},
		"attrs": []any{"a", 1.0}})
	if err != nil {
		t.Fatal(err)
	}
	// Args follow the sorted column names: attrs, grid, tags.
	if ins.Args[0] != `["a",1]` || ins.Args[1] != `{{"1","2"},{"3","4"}}` || ins.Args[2] != `{"usb","say \"hi\"","back\\slash",NULL}` {
		t.Fatalf("args: %q", ins.Args)
	}
	up, err := b.update(map[string]any{"tags": []any{}}, []Condition{{Column: "tags", Op: "=", Value: []any{"usb"}}}, nil)
	if err != nil || up.Args[0] != "{}" || up.Args[1] != `{"usb"}` {
		t.Fatalf("update: %v %q", err, up.Args)
	}
	// MySQL has no arrays: a list stays JSON.
	m := newBuilder(mysqlD, td)
	if mi, err := m.insert(map[string]any{"tags": []any{"usb"}}); err != nil || mi.Args[0] != `["usb"]` {
		t.Fatalf("mysql: %v %q", err, mi.Args)
	}
}

func TestCheckReadOnly(t *testing.T) {
	for _, c := range []struct {
		d    driver.Dialect
		sql  string
		want string // "" = accepted
	}{
		{mysqlD, "select * from t", ""},
		{mysqlD, "  (SELECT 1)", ""},
		{mysqlD, "SHOW TABLES", ""},
		{mysqlD, "/* hi */ WITH x AS (SELECT 1) SELECT * FROM x;", ""},
		{mysqlD, "SELECT * FROM t INTO OUTFILE '/tmp/x'", "write files"},
		{mysqlD, "SELECT * FROM t INTO/**/OUTFILE '/tmp/x'", "write files"},
		{mysqlD, "/*!CREATE TABLE pwned AS*/ SELECT * FROM t", "executable comments"},
		{mysqlD, "DROP TABLE t", "only runs read"},
		{mysqlD, "SELECT 1; SELECT 2", "exactly one"},
		{pgD, "SHOW search_path", ""},
		{pgD, "TRUNCATE t", "only runs read"},
		{pgD, "", "exactly one"},
	} {
		_, err := checkReadOnly(c.d, c.sql)
		if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s %q: got %v, want %q", c.d.Name, c.sql, err, c.want)
		}
	}
}

func TestLimitRead(t *testing.T) {
	my, pg, lite := driver.Dialect{Name: "mysql"}, driver.Dialect{Name: "postgres"}, driver.Dialect{Name: "sqlite"}
	for _, c := range []struct {
		d       driver.Dialect
		in, out string
	}{
		{my, "SELECT a.id, b.id FROM a JOIN b", "SELECT /*+ SET_VAR(sql_select_limit=11) */ a.id, b.id FROM a JOIN b"},
		{my, "select\n/*+ MAX_EXECUTION_TIME(5) */ 1", "select /*+ SET_VAR(sql_select_limit=11) MAX_EXECUTION_TIME(5) */ 1"},
		{my, "WITH x AS (SELECT 1) SELECT * FROM x", "WITH x AS (SELECT 1) SELECT * FROM x"},
		{my, "SHOW TABLES", "SHOW TABLES"},
		{pg, "WITH x AS (SELECT 1) SELECT * FROM x -- note", "SELECT * FROM (\nWITH x AS (SELECT 1) SELECT * FROM x -- note\n) q LIMIT 11"},
		{pg, "EXPLAIN SELECT 1", "EXPLAIN SELECT 1"},
		{lite, "SELECT 1", "SELECT 1"},
	} {
		if got := limitRead(c.d, c.in, 11); got != c.out {
			t.Errorf("%s %q:\n got %q\nwant %q", c.d.Name, c.in, got, c.out)
		}
	}
}
