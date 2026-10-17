package sqlsplit

import (
	"strings"
	"testing"
)

func TestPieces(t *testing.T) {
	// Each case shows the pieces as kind:text, code as plain text.
	show := func(s string, d Dialect) string {
		var b []string
		for _, p := range Pieces(s, d) {
			text := s[p.Start:p.End]
			b = append(b, map[Kind]string{Code: "", String: "s:", QuotedIdent: "q:", DollarString: "d:", Comment: "c:"}[p.Kind]+text)
		}
		return strings.Join(b, "|")
	}
	for _, c := range []struct {
		in   string
		d    Dialect
		want string
	}{
		{"a 'b''c' d", Postgres, "a |s:'b''c'| d"},
		{`x E'\'' y`, Postgres, `x E|s:'\''| y`},
		{`x '\' y`, Postgres, `x |s:'\'| y`},
		{`x 'it\'s' y`, MySQL, `x |s:'it\'s'| y`},
		{`"a" "b"`, MySQL, `s:"a"| |s:"b"`},
		{`"a" "b"`, Postgres, `q:"a"| |q:"b"`},
		{"`a` b", MySQL, "q:`a`| b"},
		{"`a` b", Postgres, "`a` b"},
		{"[a] [b", SQLite, "q:[a]| [b"},
		{"$$ x ' $$ y", Postgres, "d:$$ x ' $$| y"},
		{"a$b$ $t$x$t$", Postgres, "a$b$ |d:$t$x$t$"},
		{"$1 + $2", Postgres, "$1 + $2"},
		{"/* a /* b */ c */ d", Postgres, "c:/* a /* b */ c */| d"},
		{"/* a /* b */ c */ d", MySQL, "c:/* a /* b */| c */ d"},
		{"a # b\nc", MySQL, "a |c:# b\n|c"},
		{"a # b\nc", Postgres, "a # b\nc"},
		{"a -- b\nc 'open", Postgres, "a |c:-- b\n|c |s:'open"},
		{"/*!CREATE TABLE t*/ SELECT 1", MySQL, "c:/*!CREATE TABLE t*/| SELECT 1"},
	} {
		if got := show(c.in, c.d); got != c.want {
			t.Errorf("Pieces(%q, %v)\n got %s\nwant %s", c.in, c.d, got, c.want)
		}
	}
}
