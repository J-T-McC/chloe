package design

import (
	"strings"
	"testing"

	"chloe/internal/sqlsplit"
)

func gens() map[string]Generator {
	return map[string]Generator{"mysql": MySQL{}, "postgres": Postgres{}, "sqlite": SQLite{}}
}

func strp(s string) *string { return &s }

func base() Table {
	return Table{Name: "t", Columns: []Column{{Name: "id", Type: "integer"}, {Name: "v", Type: "varchar", Length: "20", Nullable: true}}}
}

func TestRejectsSmuggledSQL(t *testing.T) {
	cases := map[string]func(*Table){
		"default":       func(t *Table) { t.Columns[0].Default = strp("0; DROP TABLE users") },
		"default quote": func(t *Table) { t.Columns[0].Default = strp("'x") },
		"default comment": func(t *Table) {
			t.Columns[0].Default = strp("0 /*")
		},
		"default line comment": func(t *Table) { t.Columns[0].Default = strp("0 --") },
		"default paren":        func(t *Table) { t.Columns[0].Default = strp("0)") },
		"type":                 func(t *Table) { t.Columns[0].Type = "int); DROP TABLE users; --" },
		"type space":           func(t *Table) { t.Columns[0].Type = "int DEFAULT 1" + ";" },
		"length":               func(t *Table) { t.Columns[1].Length = "20); DROP TABLE users; --" },
		"scale":                func(t *Table) { t.Columns[1].Scale = "2)" },
		"charset":              func(t *Table) { t.Columns[1].Charset = "utf8mb4; DROP TABLE users" },
		"collation":            func(t *Table) { t.Columns[1].Collation = "x; DROP TABLE users" },
		"generated":            func(t *Table) { t.Columns[1].GeneratedExpr = "1); DROP TABLE users; --" },
		"check": func(t *Table) {
			t.Checks = []Check{{Name: "c", Expr: "1); DROP TABLE users; --"}}
		},
		"index expr": func(t *Table) {
			t.Indexes = []Index{{Name: "i", Columns: []IndexColumn{{Expr: "1); DROP TABLE users; --"}}}}
		},
		"index method": func(t *Table) {
			t.Indexes = []Index{{Name: "i", Columns: []IndexColumn{{Name: "id"}}, Method: "btree; DROP TABLE users"}}
		},
		"index where": func(t *Table) {
			t.Indexes = []Index{{Name: "i", Columns: []IndexColumn{{Name: "id"}}, Where: "true; DROP TABLE users"}}
		},
		"fk action": func(t *Table) {
			t.ForeignKeys = []ForeignKey{{Name: "f", Columns: []string{"id"}, RefTable: "u", RefColumns: []string{"id"}, OnDelete: "CASCADE; DROP TABLE users"}}
		},
		"engine":         func(t *Table) { t.Options.Engine = "InnoDB; DROP TABLE users" },
		"auto increment": func(t *Table) { t.Options.AutoIncrement = "1; DROP TABLE users" },
		"identity":       func(t *Table) { t.Columns[0].IdentityOpts = &IdentityOpts{Start: "1) ; DROP TABLE users; --"} },
		"trigger timing": func(t *Table) {
			t.Triggers = []Trigger{{Name: "tr", Timing: "BEFORE; DROP TABLE users;", Events: []string{"INSERT"}, Body: "SELECT 1", Function: "f()"}}
		},
		"trigger function": func(t *Table) {
			t.Triggers = []Trigger{{Name: "tr", Timing: "BEFORE", Events: []string{"INSERT"}, Body: "SELECT 1", Function: "f(); DROP TABLE users"}}
		},
		// Two fields that are each "one piece" only because a quote stays open.
		"split across fields": func(t *Table) {
			t.Columns[0].Default = strp("'a")
			t.Checks = []Check{{Name: "c", Expr: "x'; DROP TABLE users; --"}}
		},
	}
	for name, mod := range cases {
		for dn, g := range gens() {
			if name == "collation" && dn == "postgres" {
				continue // quoted as a name
			}
			tb := base()
			mod(&tb)
			if stmts, err := g.Create(tb); err == nil {
				t.Errorf("%s/%s: accepted: %q", dn, name, stmts)
			}
			old := base()
			old.OrigName = "t"
			for i := range old.Columns {
				old.Columns[i].OrigName = old.Columns[i].Name
			}
			nw := old
			nw.Columns = append([]Column(nil), old.Columns...)
			nw.Name = "t"
			mod(&nw)
			if stmts, err := g.Alter(old, nw); err == nil && len(stmts) > 0 {
				t.Errorf("%s/%s alter: accepted: %q", dn, name, stmts)
			}
		}
	}
}

// MySQL strings are read with backslash escapes; PostgreSQL values must be safe
// under both readings of standard_conforming_strings.
func TestBackslashReadings(t *testing.T) {
	bad := map[sqlsplit.Dialect][]string{
		sqlsplit.MySQL:    {`'a\\'; DROP TABLE users; -- '`, `"a\\"; DROP TABLE users; -- "`, `'a\'`},
		sqlsplit.Postgres: {`'a\'; DROP TABLE users; -- '`, `'a\'`, `$$x`, `$a$ x $b$`},
	}
	for d, vs := range bad {
		for _, v := range vs {
			if oneExpr(v, d) == nil {
				t.Errorf("%v: accepted %q", d, v)
			}
		}
	}
}

func TestAcceptsRealValues(t *testing.T) {
	ok := map[sqlsplit.Dialect][]string{
		sqlsplit.MySQL: {"0", "'it''s'", `'a\\b'`, "CURRENT_TIMESTAMP", "(now() + interval 1 day)", "`a` > 0 and (`b` in ('x','y'))", "_utf8mb4'x'",
			// As information_schema hands them back.
			`(title <> _utf8mb4'(') or (title <> _utf8mb4')') or (title <> _utf8mb4'it\'s')`, `_utf8mb4'C:\\dir\\'`},
		sqlsplit.Postgres: {"nextval('t_id_seq'::regclass)", "'{}'::text[]", `E'a\\b'`, "$$x;y$$", "$1", `"Weird ""name""" > 0`, "(a # b) > 0", `E'a\'; b'`},
		sqlsplit.SQLite:   {"0", "'x;y'", "[a b] > 0", "(datetime('now'))", "`a` + 1"},
	}
	for d, vs := range ok {
		for _, v := range vs {
			if err := oneExpr(v, d); err != nil {
				t.Errorf("%v: rejected %q: %v", d, v, err)
			}
		}
	}
	for _, c := range []Column{
		{Name: "a", Type: "character varying", Length: "20"},
		{Name: "b", Type: "timestamp with time zone", Length: "3"},
		{Name: "c", Type: `public."My Type"`},
		{Name: "d", Type: "int[]"},
		{Name: "e", Type: "numeric", Length: "10", Scale: "-2"},
		{Name: "f", Type: "geometry", Length: "Point", Scale: "4326"},
		{Name: "g", Type: "UNSIGNED BIG INT"},
	} {
		if err := checkFields(Table{Columns: []Column{c}}, sqlsplit.Postgres, nil); err != nil {
			t.Errorf("%s: %v", c.Type, err)
		}
	}
	my := Column{Name: "e", Type: "enum", Length: `'a','it''s', 'b\\'`, Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci"}
	if err := checkFields(Table{Columns: []Column{my}, Options: Options{Engine: "InnoDB", AutoIncrement: "42"}}, sqlsplit.MySQL, nil); err != nil {
		t.Error(err)
	}
	for _, a := range []string{"", "set null", "SET  DEFAULT", "Cascade"} {
		tb := base()
		tb.ForeignKeys = []ForeignKey{{Name: "f", Columns: []string{"id"}, RefTable: "u", RefColumns: []string{"id"}, OnDelete: a}}
		if err := checkFields(tb, sqlsplit.MySQL, nil); err != nil {
			t.Errorf("%q: %v", a, err)
		}
	}
	// Trigger bodies keep their semicolons.
	tb := base()
	tb.Triggers = []Trigger{{Name: "tr", Timing: "AFTER", Events: []string{"UPDATE OF v"}, Body: "BEGIN UPDATE t SET id = 1; SELECT 2; END"}}
	for _, dn := range []string{"mysql", "sqlite"} {
		if _, err := gens()[dn].Create(tb); err != nil && !strings.Contains(err.Error(), "one event") {
			t.Errorf("%s trigger: %v", dn, err)
		}
	}
}

func TestCheckedSplitsOutput(t *testing.T) {
	gen := func() ([]string, error) {
		return []string{"CREATE TABLE t (id int DEFAULT 'a'); DROP TABLE users; --')"}, nil
	}
	if _, err := checked(base(), nil, sqlsplit.Postgres, gen); err == nil || !strings.Contains(err.Error(), "2 statements") {
		t.Fatalf("err = %v", err)
	}
}

// Expressions read from the database are the server's own text: an ALTER keeps
// them even where the stricter check would refuse them.
func TestLoadedExpressionsKept(t *testing.T) {
	old := base()
	old.OrigName = "t"
	for i := range old.Columns {
		old.Columns[i].OrigName = old.Columns[i].Name
	}
	old.Checks = []Check{{Name: "c", OrigName: "c", Expr: `v <> 'a\'`}}
	nw := old
	nw.Columns = append([]Column(nil), old.Columns...)
	nw.Columns[1].Comment = "changed"
	if _, err := (Postgres{}).Alter(old, nw); err != nil {
		t.Fatalf("unchanged check refused: %v", err)
	}
	if _, err := (Postgres{}).Create(nw); err == nil {
		t.Fatal("create accepted an ambiguous literal")
	}
	nw.Checks = []Check{{Name: "c", OrigName: "c", Expr: `v <> 'b\'`}}
	if _, err := (Postgres{}).Alter(old, nw); err == nil {
		t.Fatal("alter accepted a new ambiguous literal")
	}
}
