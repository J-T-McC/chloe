package sqlsplit

import (
	"reflect"
	"strings"
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
		{"sqlite trigger", "CREATE TRIGGER t AFTER INSERT ON a BEGIN UPDATE b SET x = CASE WHEN 1 THEN 2 END; DELETE FROM c; END; select [a;b], `c;d` from t;", SQLite,
			[]string{"CREATE TRIGGER t AFTER INSERT ON a BEGIN UPDATE b SET x = CASE WHEN 1 THEN 2 END; DELETE FROM c; END", "select [a;b], `c;d` from t"}},
		{"sqlite begin", "BEGIN; select 1; END;", SQLite, []string{"BEGIN", "select 1", "END"}},
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
			all := Split(c.in, c.d)
			if got := sqls(all); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("\n got %q\nwant %q", got, c.want)
			}
			scanEqualsSplit(t, c.in, c.d, all)
		})
	}
}

// scanEqualsSplit checks that a Scanner reading in chunks of every small size
// finds the statements Split finds, at the same offsets and lines.
func scanEqualsSplit(t *testing.T, in string, d Dialect, want []Statement) {
	t.Helper()
	for chunk := 1; chunk <= 9; chunk++ {
		s := NewScanner(strings.NewReader(in), d)
		s.chunk = chunk
		var got []Statement
		for st, ok := s.Next(); ok; st, ok = s.Next() {
			got = append(got, st)
		}
		if s.Err() != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("chunk %d: %v\n got %+v\nwant %+v", chunk, s.Err(), got, want)
		}
	}
}

func TestScannerContext(t *testing.T) {
	for _, c := range []struct {
		in string
		d  Dialect
	}{
		{"select 1;DELIMITER $$\nselect 2;", MySQL}, // not at a line start: no DELIMITER
		{"select 1;  \n  DELIMITER $$\nselect 2$$\nDELIMITER ;\nselect 3", MySQL},
		{"select 1;\nDELIMITER //", MySQL},
		{"select a$b; select $x$;$x$; select 2", Postgres},
		{"x;CREATE TRIGGER t BEGIN SELECT 1; END; y;\n\n\nz", SQLite},
		{"\ufeffselect 1;\nselect 2", MySQL},
		{strings.Repeat("insert into t values (1, 'a;b');\n", 200) + "select 'long " + strings.Repeat("x", 500) + "';", MySQL},
	} {
		want := Split(strings.TrimPrefix(c.in, "\ufeff"), c.d)
		scanEqualsSplit(t, c.in, c.d, want)
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
