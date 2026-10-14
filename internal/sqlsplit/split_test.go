package sqlsplit

import (
	"reflect"
	"testing"
)

func sqls(st []Statement) []string {
	out := make([]string, len(st))
	for i, s := range st {
		out[i] = s.SQL
	}
	return out
}

func TestSplit(t *testing.T) {
	cases := []struct {
		name string
		in   string
		d    Dialect
		want []string
	}{
		{"basic", "select 1; select 2;", MySQL, []string{"select 1", "select 2"}},
		{"no trailing", "select 1;\nselect 2", MySQL, []string{"select 1", "select 2"}},
		{"semicolon in string", "select 'a;b'; select \"c;d\"", MySQL, []string{"select 'a;b'", "select \"c;d\""}},
		{"escaped quote mysql", `select 'it\'s;'; select 2`, MySQL, []string{`select 'it\'s;'`, "select 2"}},
		{"doubled quote", "select 'it''s;'; select 2", Postgres, []string{"select 'it''s;'", "select 2"}},
		{"comments", "-- a;b\nselect 1; /* c; */ select 2; # x;y\n", MySQL, []string{"-- a;b\nselect 1", "/* c; */ select 2"}},
		{"only comment dropped", "select 1; -- trailing", MySQL, []string{"select 1"}},
		{"backtick", "select `a;b` from t; select 2", MySQL, []string{"select `a;b` from t", "select 2"}},
		{"delimiter", "DELIMITER $$\nCREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END$$\nDELIMITER ;\nselect 3;", MySQL,
			[]string{"CREATE PROCEDURE p() BEGIN SELECT 1; SELECT 2; END", "select 3"}},
		{"dollar quote", "create function f() returns int as $$ begin return 1; end $$ language plpgsql; select 2", Postgres,
			[]string{"create function f() returns int as $$ begin return 1; end $$ language plpgsql", "select 2"}},
		{"tagged dollar", "do $body$ begin perform 1; end $body$; select 2", Postgres,
			[]string{"do $body$ begin perform 1; end $body$", "select 2"}},
		{"pg param not dollar", "select $1; select 2", Postgres, []string{"select $1", "select 2"}},
		{"nested comment pg", "/* a /* b; */ c; */ select 1; select 2", Postgres, []string{"/* a /* b; */ c; */ select 1", "select 2"}},
		{"E string", `select E'a\';b'; select 2`, Postgres, []string{`select E'a\';b'`, "select 2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := sqls(Split(c.in, c.d))
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("\n got %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestStartLineAndStatementAt(t *testing.T) {
	script := "select 1;\n\n  select 2\n  from t;\nselect 3"
	st := Split(script, MySQL)
	if st[1].StartLine != 3 || st[2].StartLine != 5 {
		t.Fatalf("lines: %+v", st)
	}
	s, _ := StatementAt(script, MySQL, len("select 1;\n\n  sel"))
	if s.SQL != "select 2\n  from t" {
		t.Fatalf("StatementAt got %q", s.SQL)
	}
}
