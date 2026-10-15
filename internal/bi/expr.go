package bi

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"chloe/internal/driver"
)

// Calculated fields are written in a small expression language and compiled to
// each dialect's SQL. Field names are checked against the data source and
// quoted; literals and parameters are bound. Nothing the user types is pasted
// into the SQL.
//
//	[Unit Price] * quantity             fields: a plain name, or any name in brackets
//	ROUND(total / 1.2, 2)               + - * / %, ( ), numbers and 'text'
//	IF(status = 'paid', total, 0)       = <> != < > <= >=, AND OR NOT, NULL, TRUE, FALSE
//	first & ' ' & last                  & joins text
//	total * [$rate]                     parameters
//
// Functions: ROUND, ABS, FLOOR, CEIL, COALESCE, NULLIF, IF, UPPER, LOWER, TRIM,
// LENGTH, YEAR, MONTH, DAY. Division by zero gives NULL rather than an error.

var exprFuncs = map[string][2]int{ // name → min, max arguments (-1: any)
	"ROUND": {1, 2}, "ABS": {1, 1}, "FLOOR": {1, 1}, "CEIL": {1, 1}, "COALESCE": {1, -1}, "NULLIF": {2, 2},
	"IF": {3, 3}, "UPPER": {1, 1}, "LOWER": {1, 1}, "TRIM": {1, 1}, "LENGTH": {1, 1},
	"YEAR": {1, 1}, "MONTH": {1, 1}, "DAY": {1, 1},
}

type token struct {
	kind string // num str ident field param op ( ) ,
	text string
	pos  int
}

func lexExpr(s string) ([]token, error) {
	var out []token
	for i := 0; i < len(s); {
		c := rune(s[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '\'':
			var b strings.Builder
			j := i + 1
			for {
				if j >= len(s) {
					return nil, fmt.Errorf("unclosed text starting at %d", i+1)
				}
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						b.WriteByte('\'')
						j += 2
						continue
					}
					break
				}
				b.WriteByte(s[j])
				j++
			}
			out = append(out, token{"str", b.String(), i})
			i = j + 1
		case c == '[':
			j := strings.IndexByte(s[i:], ']')
			if j < 0 {
				return nil, fmt.Errorf("unclosed [ at %d", i+1)
			}
			name := s[i+1 : i+j]
			kind := "field"
			if strings.HasPrefix(name, "$") {
				kind, name = "param", name[1:]
			}
			out = append(out, token{kind, name, i})
			i += j + 1
		case unicode.IsDigit(c) || c == '.' && i+1 < len(s) && unicode.IsDigit(rune(s[i+1])):
			j := i
			for j < len(s) && (unicode.IsDigit(rune(s[j])) || s[j] == '.') {
				j++
			}
			if _, err := strconv.ParseFloat(s[i:j], 64); err != nil {
				return nil, fmt.Errorf("bad number %q at %d", s[i:j], i+1)
			}
			out = append(out, token{"num", s[i:j], i})
			i = j
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(s) && (unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j])) || s[j] == '_') {
				j++
			}
			out = append(out, token{"ident", s[i:j], i})
			i = j
		default:
			two := ""
			if i+1 < len(s) {
				two = s[i : i+2]
			}
			switch {
			case two == "<=" || two == ">=" || two == "<>" || two == "!=":
				out = append(out, token{"op", two, i})
				i += 2
			case strings.ContainsRune("+-*/%&=<>", c):
				out = append(out, token{"op", string(c), i})
				i++
			case c == '(' || c == ')' || c == ',':
				out = append(out, token{string(c), string(c), i})
				i++
			case c == '"':
				return nil, fmt.Errorf("text goes in single quotes ('like this'), not double quotes (at %d)", i+1)
			default:
				return nil, fmt.Errorf("unexpected %q at %d", c, i+1)
			}
		}
	}
	return out, nil
}

// exprNode is a parsed expression.
type exprNode struct {
	op   string // num str null bool field param fn neg not and or cmp + - * / % &
	text string
	args []*exprNode
}

type exprParser struct {
	toks []token
	i    int
}

func (p *exprParser) peek() *token {
	if p.i < len(p.toks) {
		return &p.toks[p.i]
	}
	return nil
}

func (p *exprParser) word(w string) bool {
	t := p.peek()
	if t != nil && t.kind == "ident" && strings.EqualFold(t.text, w) {
		p.i++
		return true
	}
	return false
}

func (p *exprParser) sym(kind, text string) bool {
	t := p.peek()
	if t != nil && t.kind == kind && (text == "" || t.text == text) {
		p.i++
		return true
	}
	return false
}

func (p *exprParser) or() (*exprNode, error) {
	l, err := p.and()
	for err == nil && p.word("OR") {
		var r *exprNode
		if r, err = p.and(); err == nil {
			l = &exprNode{op: "or", args: []*exprNode{l, r}}
		}
	}
	return l, err
}

func (p *exprParser) and() (*exprNode, error) {
	l, err := p.not()
	for err == nil && p.word("AND") {
		var r *exprNode
		if r, err = p.not(); err == nil {
			l = &exprNode{op: "and", args: []*exprNode{l, r}}
		}
	}
	return l, err
}

func (p *exprParser) not() (*exprNode, error) {
	if p.word("NOT") {
		x, err := p.not()
		return &exprNode{op: "not", args: []*exprNode{x}}, err
	}
	return p.cmp()
}

func (p *exprParser) cmp() (*exprNode, error) {
	l, err := p.add()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t != nil && t.kind == "op" && slices.Contains([]string{"=", "<>", "!=", "<", ">", "<=", ">="}, t.text) {
		p.i++
		r, err := p.add()
		op := t.text
		if op == "!=" {
			op = "<>"
		}
		return &exprNode{op: "cmp", text: op, args: []*exprNode{l, r}}, err
	}
	return l, nil
}

func (p *exprParser) add() (*exprNode, error) {
	l, err := p.mul()
	for err == nil {
		t := p.peek()
		if t == nil || t.kind != "op" || !strings.Contains("+-&", t.text) {
			break
		}
		p.i++
		var r *exprNode
		if r, err = p.mul(); err == nil {
			l = &exprNode{op: t.text, args: []*exprNode{l, r}}
		}
	}
	return l, err
}

func (p *exprParser) mul() (*exprNode, error) {
	l, err := p.unary()
	for err == nil {
		t := p.peek()
		if t == nil || t.kind != "op" || !strings.Contains("*/%", t.text) {
			break
		}
		p.i++
		var r *exprNode
		if r, err = p.unary(); err == nil {
			l = &exprNode{op: t.text, args: []*exprNode{l, r}}
		}
	}
	return l, err
}

func (p *exprParser) unary() (*exprNode, error) {
	if p.sym("op", "-") {
		x, err := p.unary()
		return &exprNode{op: "neg", args: []*exprNode{x}}, err
	}
	return p.primary()
}

func (p *exprParser) primary() (*exprNode, error) {
	t := p.peek()
	if t == nil {
		return nil, fmt.Errorf("the expression ends too soon")
	}
	p.i++
	switch t.kind {
	case "num":
		return &exprNode{op: "num", text: t.text}, nil
	case "str":
		return &exprNode{op: "str", text: t.text}, nil
	case "field":
		return &exprNode{op: "field", text: t.text}, nil
	case "param":
		return &exprNode{op: "param", text: t.text}, nil
	case "(":
		x, err := p.or()
		if err != nil {
			return nil, err
		}
		if !p.sym(")", "") {
			return nil, fmt.Errorf("missing ) for the ( at %d", t.pos+1)
		}
		return x, nil
	case "ident":
		up := strings.ToUpper(t.text)
		switch up {
		case "NULL":
			return &exprNode{op: "null"}, nil
		case "TRUE", "FALSE":
			return &exprNode{op: "bool", text: up}, nil
		}
		if p.sym("(", "") {
			lim, ok := exprFuncs[up]
			if !ok {
				names := make([]string, 0, len(exprFuncs))
				for k := range exprFuncs {
					names = append(names, k)
				}
				slices.Sort(names)
				return nil, fmt.Errorf("unknown function %s (functions: %s)", t.text, strings.Join(names, ", "))
			}
			n := &exprNode{op: "fn", text: up}
			if !p.sym(")", "") {
				for {
					a, err := p.or()
					if err != nil {
						return nil, err
					}
					n.args = append(n.args, a)
					if p.sym(")", "") {
						break
					}
					if !p.sym(",", "") {
						return nil, fmt.Errorf("expected , or ) in %s(…)", up)
					}
				}
			}
			if len(n.args) < lim[0] || lim[1] >= 0 && len(n.args) > lim[1] {
				return nil, fmt.Errorf("%s takes %s argument(s), not %d", up, argCount(lim), len(n.args))
			}
			return n, nil
		}
		return &exprNode{op: "field", text: t.text}, nil
	}
	return nil, fmt.Errorf("unexpected %q at %d", t.text, t.pos+1)
}

func argCount(l [2]int) string {
	switch {
	case l[1] < 0:
		return fmt.Sprintf("%d or more", l[0])
	case l[0] == l[1]:
		return strconv.Itoa(l[0])
	}
	return fmt.Sprintf("%d to %d", l[0], l[1])
}

// parseExpr parses a calculated field's expression.
func parseExpr(s string) (*exprNode, error) {
	if strings.TrimSpace(s) == "" {
		return nil, fmt.Errorf("the expression is empty")
	}
	toks, err := lexExpr(s)
	if err != nil {
		return nil, err
	}
	p := &exprParser{toks: toks}
	n, err := p.or()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t != nil {
		return nil, fmt.Errorf("unexpected %q at %d", t.text, t.pos+1)
	}
	return n, nil
}

// exprCompiler renders expressions for one dialect, binding values through bind.
type exprCompiler struct {
	d      driver.Dialect
	fields func(string) (Field, bool)
	params Values
	bind   func(any) string
}

func (c *exprCompiler) text(x string) string {
	if c.d.Name == "mysql" {
		return "CAST(" + x + " AS CHAR)"
	}
	return "CAST(" + x + " AS TEXT)"
}

func (c *exprCompiler) num(x string) string {
	switch c.d.Name {
	case "postgres":
		return "CAST(" + x + " AS numeric)"
	case "mysql":
		return "(" + x + " + 0.0)"
	}
	return "CAST(" + x + " AS REAL)"
}

// compile returns the SQL for n and its type.
func (c *exprCompiler) compile(n *exprNode) (string, string, error) {
	sub := func(i int) (string, string, error) { return c.compile(n.args[i]) }
	switch n.op {
	case "num":
		x, _ := strconv.ParseFloat(n.text, 64)
		return c.bind(x), TypeNumber, nil
	case "str":
		return c.bind(n.text), TypeString, nil
	case "null":
		return "NULL", TypeString, nil
	case "bool":
		if c.d.Name == "postgres" {
			return n.text, TypeNumber, nil
		}
		return map[string]string{"TRUE": "1", "FALSE": "0"}[n.text], TypeNumber, nil
	case "field":
		f, ok := c.fields(n.text)
		if !ok {
			return "", "", fmt.Errorf("no field %q", n.text)
		}
		return c.d.QuoteIdent(f.Name), f.Type, nil
	case "param":
		if c.params == nil { // typing only
			return c.bind(""), TypeString, nil
		}
		v, err := c.params.get(n.text)
		if err != nil {
			return "", "", err
		}
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			return c.bind(x), TypeNumber, nil
		}
		return c.bind(v), TypeString, nil
	case "neg":
		x, _, err := sub(0)
		return "(-" + x + ")", TypeNumber, err
	case "not":
		x, _, err := sub(0)
		return "(NOT " + x + ")", TypeNumber, err
	case "and", "or", "cmp":
		a, _, err := sub(0)
		if err != nil {
			return "", "", err
		}
		b, _, err := sub(1)
		op := strings.ToUpper(n.op)
		if n.op == "cmp" {
			op = n.text
		}
		return "(" + a + " " + op + " " + b + ")", TypeNumber, err
	case "+", "-", "*", "%":
		a, _, err := sub(0)
		if err != nil {
			return "", "", err
		}
		b, _, err := sub(1)
		if n.op == "%" && c.d.Name == "postgres" {
			return "MOD(" + c.num(a) + ", " + c.num(b) + ")", TypeNumber, err
		}
		return "(" + c.num(a) + " " + n.op + " " + c.num(b) + ")", TypeNumber, err
	case "/":
		a, _, err := sub(0)
		if err != nil {
			return "", "", err
		}
		b, _, err := sub(1)
		// Division by zero gives NULL, and never truncates to an integer.
		return "(" + c.num(a) + " / NULLIF(" + c.num(b) + ", 0))", TypeNumber, err
	case "&":
		a, _, err := sub(0)
		if err != nil {
			return "", "", err
		}
		b, _, err := sub(1)
		if c.d.Name == "mysql" {
			return "CONCAT(" + c.text(a) + ", " + c.text(b) + ")", TypeString, err
		}
		return "(" + c.text(a) + " || " + c.text(b) + ")", TypeString, err
	case "fn":
		return c.fn(n)
	}
	return "", "", fmt.Errorf("can't compile %s", n.op)
}

func (c *exprCompiler) fn(n *exprNode) (string, string, error) {
	args := make([]string, len(n.args))
	types := make([]string, len(n.args))
	for i := range n.args {
		var err error
		if args[i], types[i], err = c.compile(n.args[i]); err != nil {
			return "", "", err
		}
	}
	date := func(x, part string) string {
		switch c.d.Name {
		case "postgres":
			return "CAST(EXTRACT(" + part + " FROM CAST(" + x + " AS timestamp)) AS integer)"
		case "mysql":
			return part + "(" + x + ")"
		}
		f := map[string]string{"YEAR": "%Y", "MONTH": "%m", "DAY": "%d"}[part]
		return "CAST(strftime('" + f + "', " + x + ") AS INTEGER)"
	}
	switch n.text {
	case "ROUND":
		if len(args) == 2 {
			if c.d.Name == "postgres" {
				return "ROUND(" + c.num(args[0]) + ", CAST(" + args[1] + " AS integer))", TypeNumber, nil
			}
			return "ROUND(" + c.num(args[0]) + ", " + args[1] + ")", TypeNumber, nil
		}
		return "ROUND(" + c.num(args[0]) + ")", TypeNumber, nil
	case "ABS":
		return "ABS(" + c.num(args[0]) + ")", TypeNumber, nil
	case "FLOOR", "CEIL":
		x := c.num(args[0])
		if c.d.Name == "sqlite" {
			// SQLite may be built without math functions.
			if n.text == "FLOOR" {
				return "(CAST(" + x + " AS INTEGER) - (" + x + " < CAST(" + x + " AS INTEGER)))", TypeNumber, nil
			}
			return "(CAST(" + x + " AS INTEGER) + (" + x + " > CAST(" + x + " AS INTEGER)))", TypeNumber, nil
		}
		fn := n.text
		if fn == "CEIL" && c.d.Name == "mysql" {
			fn = "CEILING"
		}
		return fn + "(" + x + ")", TypeNumber, nil
	case "COALESCE":
		return "COALESCE(" + strings.Join(args, ", ") + ")", types[0], nil
	case "NULLIF":
		return "NULLIF(" + args[0] + ", " + args[1] + ")", types[0], nil
	case "IF":
		t := types[1]
		if n.args[1].op == "null" {
			t = types[2]
		}
		return "(CASE WHEN " + args[0] + " THEN " + args[1] + " ELSE " + args[2] + " END)", t, nil
	case "UPPER", "LOWER", "TRIM":
		return n.text + "(" + c.text(args[0]) + ")", TypeString, nil
	case "LENGTH":
		if c.d.Name == "mysql" {
			return "CHAR_LENGTH(" + c.text(args[0]) + ")", TypeNumber, nil
		}
		return "LENGTH(" + c.text(args[0]) + ")", TypeNumber, nil
	case "YEAR", "MONTH", "DAY":
		return date(args[0], n.text), TypeNumber, nil
	}
	return "", "", fmt.Errorf("unknown function %s", n.text)
}
