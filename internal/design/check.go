package design

import (
	"fmt"
	"regexp"
	"strings"

	"chloe/internal/sqlsplit"
)

// The generators write types, defaults, expressions and options into the DDL as
// given. A table can come from an ER model file or an MCP agent rather than the
// designer, so before generating, every such field must be one well-formed piece
// of SQL: a type, a number, a word, or an expression with its quotes, comments and
// parentheses closed and no statement delimiter. The generated statements are then
// split again and must each be a single statement.

var (
	word = `[A-Za-z_][A-Za-z0-9_$]*`
	// A type: words or quoted names, dotted or separated by spaces (double
	// precision, character varying, public."My Type"), then array suffixes.
	typeRe = regexp.MustCompile(`^(?:` + word + `|"(?:[^"]|"")+")(?:[ .](?:` + word + `|"(?:[^"]|"")+"))*(?:\[\d*\])*$`)
	intRe  = regexp.MustCompile(`^-?\d+$`)
	wordRe = regexp.MustCompile(`^` + word + `$`)
	// A MySQL enum/set value list, matched with and without backslash escapes so
	// it reads the same whatever sql_mode the server has.
	enumEsc   = regexp.MustCompile(`^'(?:[^'\\]|''|\\.)*'(?:\s*,\s*'(?:[^'\\]|''|\\.)*')*$`)
	enumPlain = regexp.MustCompile(`^'(?:[^']|'')*'(?:\s*,\s*'(?:[^']|'')*')*$`)
)

var (
	fkActions      = map[string]bool{"": true, "NO ACTION": true, "RESTRICT": true, "CASCADE": true, "SET NULL": true, "SET DEFAULT": true}
	triggerTimings = map[string]bool{"BEFORE": true, "AFTER": true, "INSTEAD OF": true}
	triggerEvents  = map[string]bool{"INSERT": true, "UPDATE": true, "DELETE": true, "TRUNCATE": true}
)

func normWords(s string) string { return strings.ToUpper(strings.Join(strings.Fields(s), " ")) }

// checkFields rejects any field the generators write raw that isn't one piece.
// Expressions in loaded (the table as read from the database, for an ALTER) are
// the server's own text and are taken as they are.
func checkFields(t Table, d sqlsplit.Dialect, loaded *Table) error {
	known := map[string]bool{}
	if loaded != nil {
		for _, s := range expressions(*loaded) {
			known[s] = true
		}
	}
	wordOpt := func(what, v string) error {
		if v != "" && !wordRe.MatchString(v) {
			return fmt.Errorf("%s %q is not a valid name", what, v)
		}
		return nil
	}
	num := func(what, v string) error {
		if v != "" && !intRe.MatchString(strings.TrimSpace(v)) {
			return fmt.Errorf("%s %q is not a number", what, v)
		}
		return nil
	}
	ex := func(what, v string) error {
		if known[v] {
			return nil
		}
		if err := oneExpr(v, d); err != nil {
			return fmt.Errorf("%s: %w", what, err)
		}
		return nil
	}
	for _, c := range t.Columns {
		col := fmt.Sprintf("column %q", c.Name)
		if !typeRe.MatchString(strings.TrimSpace(c.Type)) {
			return fmt.Errorf("%s: %q is not a type name", col, c.Type)
		}
		if l := strings.TrimSpace(c.Length); l != "" && !intRe.MatchString(l) && !wordRe.MatchString(l) &&
			!(d == sqlsplit.MySQL && enumEsc.MatchString(l) && enumPlain.MatchString(l)) {
			return fmt.Errorf("%s: length %q is not a number", col, c.Length)
		}
		if err := num(col+" scale", c.Scale); err != nil {
			return err
		}
		if c.Default != nil {
			if err := ex(col+" default", *c.Default); err != nil {
				return err
			}
		}
		if err := ex(col+" expression", c.GeneratedExpr); err != nil {
			return err
		}
		if d != sqlsplit.Postgres { // PostgreSQL collations are quoted
			if err := wordOpt(col+" collation", c.Collation); err != nil {
				return err
			}
		}
		if err := wordOpt(col+" character set", c.Charset); err != nil {
			return err
		}
		if o := c.IdentityOpts; o != nil {
			for _, v := range []string{o.Start, o.Increment, o.Min, o.Max, o.Cache} {
				if err := num(col+" identity option", v); err != nil {
					return err
				}
			}
		}
	}
	for _, ix := range t.Indexes {
		what := fmt.Sprintf("index %q", ix.Name)
		if err := wordOpt(what+" method", ix.Method); err != nil {
			return err
		}
		if err := ex(what+" condition", ix.Where); err != nil {
			return err
		}
		for _, c := range ix.Columns {
			if err := ex(what+" expression", c.Expr); err != nil {
				return err
			}
			if err := num(what+" prefix length", c.Length); err != nil {
				return err
			}
		}
	}
	for _, fk := range t.ForeignKeys {
		for _, a := range []string{fk.OnDelete, fk.OnUpdate} {
			if !fkActions[normWords(a)] {
				return fmt.Errorf("foreign key %q: %q is not a referential action", fk.Name, a)
			}
		}
	}
	for _, c := range t.Checks {
		if err := ex(fmt.Sprintf("check %q", c.Name), c.Expr); err != nil {
			return err
		}
	}
	for _, tr := range t.Triggers {
		what := fmt.Sprintf("trigger %q", tr.Name)
		if !triggerTimings[normWords(tr.Timing)] {
			return fmt.Errorf("%s: %q is not a trigger time", what, tr.Timing)
		}
		for _, ev := range tr.Events {
			kw, cols, of := strings.Cut(strings.TrimSpace(ev), " ")
			if !triggerEvents[strings.ToUpper(kw)] {
				return fmt.Errorf("%s: %q is not a trigger event", what, ev)
			}
			if of {
				rest, ok := strings.CutPrefix(strings.ToUpper(strings.TrimSpace(cols)), "OF ")
				if !ok || strings.TrimSpace(rest) == "" {
					return fmt.Errorf("%s: %q is not a trigger event", what, ev)
				}
				if err := ex(what+" event", cols); err != nil {
					return err
				}
			}
		}
		if err := ex(what+" condition", tr.When); err != nil {
			return err
		}
		if err := ex(what+" function", tr.Function); err != nil {
			return err
		}
	}
	o := t.Options
	for _, f := range []struct{ what, v string }{{"engine", o.Engine}, {"character set", o.Charset}, {"collation", o.Collation}, {"row format", o.RowFormat}} {
		if err := wordOpt("table "+f.what, f.v); err != nil {
			return err
		}
	}
	return num("table auto increment", o.AutoIncrement)
}

// expressions lists the expression fields of t that checkFields checks.
func expressions(t Table) []string {
	var out []string
	for _, c := range t.Columns {
		if c.Default != nil {
			out = append(out, *c.Default)
		}
		out = append(out, c.GeneratedExpr)
	}
	for _, ix := range t.Indexes {
		out = append(out, ix.Where)
		for _, c := range ix.Columns {
			out = append(out, c.Expr)
		}
	}
	for _, c := range t.Checks {
		out = append(out, c.Expr)
	}
	for _, tr := range t.Triggers {
		out = append(out, tr.When, tr.Function)
	}
	return out
}

// oneExpr checks that s is a single expression for dialect d: its strings,
// quoted names and dollar quotes are closed, its parentheses balance, and it has
// no comment or statement delimiter.
//
// MySQL strings are read with backslash escapes: that is the default sql_mode and
// how information_schema hands back defaults, checks and generated expressions
// ('it\'s'). Chloe's MySQL connections run one statement per call, so a server
// with NO_BACKSLASH_ESCAPES still can't be given a second statement. PostgreSQL
// connections run several, so there s must pass both readings of
// standard_conforming_strings.
func oneExpr(s string, d sqlsplit.Dialect) error {
	switch d {
	case sqlsplit.SQLite:
		return scanExpr(s, d, false)
	case sqlsplit.MySQL:
		return scanExpr(s, d, true)
	}
	if err := scanExpr(s, d, false); err != nil {
		return err
	}
	return scanExpr(s, d, true)
}

func scanExpr(s string, d sqlsplit.Dialect, backslash bool) error {
	depth := 0
	isWord := func(c byte) bool {
		return c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`' && d != sqlsplit.Postgres:
			// PostgreSQL E'…' strings always take backslash escapes; MySQL strings
			// (both quote kinds) take them unless NO_BACKSLASH_ESCAPES or ANSI_QUOTES.
			esc := false
			switch d {
			case sqlsplit.MySQL:
				esc = backslash && c != '`'
			case sqlsplit.Postgres:
				esc = c == '\'' && (backslash || i > 0 && (s[i-1] == 'e' || s[i-1] == 'E') && (i == 1 || !isWord(s[i-2])))
			}
			j := i + 1
			for ; j < len(s); j++ {
				if esc && s[j] == '\\' {
					j++
					continue
				}
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j++
						continue
					}
					break
				}
			}
			if j >= len(s) {
				return fmt.Errorf("unterminated %c quote", c)
			}
			i = j
		case c == '[' && d == sqlsplit.SQLite:
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return fmt.Errorf("unterminated [ quote")
			}
			i += j
		case c == '$' && d == sqlsplit.Postgres && (i == 0 || !isWord(s[i-1])):
			j := i + 1
			for j < len(s) && isWord(s[j]) && s[j] != '$' {
				j++
			}
			if j >= len(s) || s[j] != '$' || j > i+1 && s[i+1] >= '0' && s[i+1] <= '9' {
				continue // $1 parameter or a plain $
			}
			tag := s[i : j+1]
			end := strings.Index(s[j+1:], tag)
			if end < 0 {
				return fmt.Errorf("unterminated %s quote", tag)
			}
			i = j + end + len(tag)
		case c == '-' && i+1 < len(s) && s[i+1] == '-', c == '/' && i+1 < len(s) && s[i+1] == '*', c == '#' && d == sqlsplit.MySQL:
			return fmt.Errorf("comments are not allowed here")
		case c == ';':
			return fmt.Errorf("must be a single expression (no ;)")
		case c == '\\' && d == sqlsplit.MySQL:
			// mysql client commands (\g, \G) end statements.
			return fmt.Errorf("unexpected \\ outside a string")
		case c == '(':
			depth++
		case c == ')':
			if depth--; depth < 0 {
				return fmt.Errorf("unbalanced parentheses")
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unbalanced parentheses")
	}
	if d == sqlsplit.MySQL && delimiterLine.MatchString(s) {
		return fmt.Errorf("DELIMITER is not allowed here")
	}
	return nil
}

var delimiterLine = regexp.MustCompile(`(?im)^[ \t]*delimiter[ \t]`)

// checked validates t's raw fields for dialect d, runs gen, and makes sure each
// statement it returns is a single statement. MySQL trigger bodies hold their own
// semicolons; the server takes one statement per call, so they're left to it.
func checked(t Table, loaded *Table, d sqlsplit.Dialect, gen func() ([]string, error)) ([]string, error) {
	if err := Validate(t); err != nil {
		return nil, err
	}
	if err := checkFields(t, d, loaded); err != nil {
		return nil, err
	}
	stmts, err := gen()
	if err != nil {
		return nil, err
	}
	for _, s := range stmts {
		if d == sqlsplit.MySQL && strings.HasPrefix(s, "CREATE TRIGGER ") {
			continue
		}
		if n := len(sqlsplit.Split(s, d)); n != 1 {
			return nil, fmt.Errorf("generated SQL is %d statements where one was expected: %.80s", n, s)
		}
	}
	return stmts, nil
}
