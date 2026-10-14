package sqlite

import (
	"fmt"
	"strings"

	"chloe/internal/design"
)

// SQLite keeps each object's CREATE statement verbatim; the designer reads
// constraint names, CHECKs, collations and AUTOINCREMENT from it, which the
// pragmas don't report.

type tokKind int

const (
	tWord   tokKind = iota // keyword or bare identifier
	tQuoted                // "x", `x` or [x]
	tString                // 'x'
	tNumber
	tPunct
)

type token struct {
	kind       tokKind
	text       string // identifiers unquoted; punctuation as is
	start, end int
}

func (t token) is(words ...string) bool {
	if t.kind != tWord && t.kind != tPunct {
		return false
	}
	for _, w := range words {
		if strings.EqualFold(t.text, w) {
			return true
		}
	}
	return false
}

func lex(s string) []token {
	var out []token
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '-' && i+1 < n && s[i+1] == '-':
			for i < n && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && s[i+1] == '*':
			if j := strings.Index(s[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				i = n
			}
		case c == '\'' || c == '"' || c == '`' || c == '[':
			closer := c
			if c == '[' {
				closer = ']'
			}
			var b strings.Builder
			j := i + 1
			for j < n {
				if s[j] == closer {
					if closer != ']' && j+1 < n && s[j+1] == closer {
						b.WriteByte(closer)
						j += 2
						continue
					}
					break
				}
				b.WriteByte(s[j])
				j++
			}
			kind := tQuoted
			if c == '\'' {
				kind = tString
			}
			out = append(out, token{kind: kind, text: b.String(), start: i, end: min(j+1, n)})
			i = min(j+1, n)
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && s[i+1] >= '0' && s[i+1] <= '9'):
			j := i
			for j < n && (isAlnum(s[j]) || s[j] == '.') {
				j++
			}
			out = append(out, token{kind: tNumber, text: s[i:j], start: i, end: j})
			i = j
		case isAlnum(c) || c >= 0x80 || c == '$':
			j := i
			for j < n && (isAlnum(s[j]) || s[j] >= 0x80 || s[j] == '$') {
				j++
			}
			out = append(out, token{kind: tWord, text: s[i:j], start: i, end: j})
			i = j
		default:
			out = append(out, token{kind: tPunct, text: string(c), start: i, end: i + 1})
			i++
		}
	}
	return out
}

func isAlnum(c byte) bool {
	return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') || (c >= '0' && c <= '9')
}

// parser walks tokens of one statement; src is the statement text.
type parser struct {
	src  string
	toks []token
	pos  int
}

func (p *parser) peek() token {
	if p.pos < len(p.toks) {
		return p.toks[p.pos]
	}
	return token{kind: tPunct, start: len(p.src), end: len(p.src)}
}

func (p *parser) eof() bool { return p.pos >= len(p.toks) }

func (p *parser) next() token {
	t := p.peek()
	p.pos++
	return t
}

func (p *parser) accept(words ...string) bool {
	for i, w := range words {
		if p.pos+i >= len(p.toks) || !p.toks[p.pos+i].is(w) {
			return false
		}
	}
	p.pos += len(words)
	return true
}

func (p *parser) expect(w string) error {
	if !p.accept(w) {
		return fmt.Errorf("expected %s near %q", w, p.peek().text)
	}
	return nil
}

// skipParens consumes a balanced (...) group and returns the text inside it.
func (p *parser) parens() (string, error) {
	if !p.peek().is("(") {
		return "", fmt.Errorf("expected ( near %q", p.peek().text)
	}
	open := p.next()
	depth := 1
	for !p.eof() {
		t := p.next()
		if t.is("(") {
			depth++
		} else if t.is(")") {
			depth--
			if depth == 0 {
				return strings.TrimSpace(p.src[open.end:t.start]), nil
			}
		}
	}
	return "", fmt.Errorf("unbalanced parentheses")
}

// name reads an identifier, optionally schema-qualified; it returns the last part.
func (p *parser) name() string {
	t := p.next()
	if p.peek().is(".") {
		p.next()
		t = p.next()
	}
	return t.text
}

// list splits a parenthesised, comma-separated group into token ranges.
func (p *parser) list() ([][2]int, error) {
	if !p.accept("(") {
		return nil, fmt.Errorf("expected ( near %q", p.peek().text)
	}
	var out [][2]int
	start, depth := p.pos, 0
	for !p.eof() {
		t := p.peek()
		switch {
		case t.is("("):
			depth++
		case t.is(")") && depth > 0:
			depth--
		case t.is(")"):
			out = append(out, [2]int{start, p.pos})
			p.next()
			return out, nil
		case t.is(",") && depth == 0:
			out = append(out, [2]int{start, p.pos})
			start = p.pos + 1
		}
		p.next()
	}
	return nil, fmt.Errorf("unbalanced parentheses")
}

func (p *parser) sub(r [2]int) *parser { return &parser{src: p.src, toks: p.toks[r[0]:r[1]]} }

// text is the source between two token positions.
func (p *parser) text(from, to int) string {
	if from >= to || from >= len(p.toks) {
		return ""
	}
	return strings.TrimSpace(p.src[p.toks[from].start:p.toks[to-1].end])
}

func (p *parser) skipConflict() {
	if p.accept("ON", "CONFLICT") {
		p.next()
	}
}

// names parses (a, b COLLATE x DESC, ...) into column names.
func (p *parser) names() ([]string, error) {
	ranges, err := p.list()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(ranges))
	for i, r := range ranges {
		if r[1] > r[0] {
			out[i] = p.toks[r[0]].text
		}
	}
	return out, nil
}

var columnConstraint = map[string]bool{"CONSTRAINT": true, "PRIMARY": true, "NOT": true, "NULL": true, "UNIQUE": true,
	"CHECK": true, "DEFAULT": true, "COLLATE": true, "REFERENCES": true, "GENERATED": true, "AS": true}

var tableConstraint = map[string]bool{"CONSTRAINT": true, "PRIMARY": true, "UNIQUE": true, "CHECK": true, "FOREIGN": true}

// parseCreateTable reads a CREATE TABLE statement into a design table.
func parseCreateTable(src string) (design.Table, error) {
	var t design.Table
	p := &parser{src: src, toks: lex(src)}
	if err := p.expect("CREATE"); err != nil {
		return t, err
	}
	p.accept("TEMP")
	p.accept("TEMPORARY")
	if p.accept("VIRTUAL") {
		return t, fmt.Errorf("virtual tables can't be designed")
	}
	if err := p.expect("TABLE"); err != nil {
		return t, err
	}
	p.accept("IF", "NOT", "EXISTS")
	t.Name = p.name()
	if p.peek().is("AS") {
		return t, fmt.Errorf("table was created from a SELECT; its definition isn't stored")
	}
	defs, err := p.list()
	if err != nil {
		return t, err
	}
	for _, r := range defs {
		d := p.sub(r)
		if d.eof() {
			continue
		}
		if first := d.peek(); first.kind == tWord && tableConstraint[strings.ToUpper(first.text)] {
			if err := parseTableConstraint(d, &t); err != nil {
				return t, err
			}
			continue
		}
		if err := parseColumn(d, &t); err != nil {
			return t, err
		}
	}
	for !p.eof() {
		switch {
		case p.accept("WITHOUT", "ROWID"):
			t.Options.WithoutRowid = true
		case p.accept("STRICT"):
			t.Options.Strict = true
		default:
			p.next()
		}
	}
	return t, nil
}

func parseColumn(p *parser, t *design.Table) error {
	col := design.Column{Name: p.next().text, Nullable: true}
	// The type is every token up to the first constraint keyword.
	typeStart := p.pos
	for !p.eof() {
		tk := p.peek()
		if tk.kind == tWord && columnConstraint[strings.ToUpper(tk.text)] {
			break
		}
		if tk.is("(") {
			if _, err := p.parens(); err != nil {
				return err
			}
			continue
		}
		p.next()
	}
	setType(&col, p.text(typeStart, p.pos))
	name := ""
	for !p.eof() {
		switch {
		case p.accept("CONSTRAINT"):
			name = p.next().text
			continue
		case p.accept("PRIMARY", "KEY"):
			p.accept("ASC")
			p.accept("DESC")
			p.skipConflict()
			if p.accept("AUTOINCREMENT") {
				col.AutoIncrement = true
			}
			t.PrimaryKey = &design.PrimaryKey{Name: name, Columns: []string{col.Name}}
		case p.accept("NOT", "NULL"):
			col.Nullable = false
			p.skipConflict()
		case p.accept("NULL"):
			col.Nullable = true
		case p.accept("UNIQUE"):
			p.skipConflict()
			t.Uniques = append(t.Uniques, design.Unique{Name: name, OrigName: name, Columns: []string{col.Name}})
		case p.accept("CHECK"):
			e, err := p.parens()
			if err != nil {
				return err
			}
			t.Checks = append(t.Checks, design.Check{Name: name, OrigName: name, Expr: e})
		case p.accept("DEFAULT"):
			def, err := defaultExpr(p)
			if err != nil {
				return err
			}
			col.Default = &def
		case p.accept("COLLATE"):
			col.Collation = p.next().text
		case p.peek().is("REFERENCES"):
			fk, err := parseReferences(p)
			if err != nil {
				return err
			}
			fk.Name, fk.OrigName, fk.Columns = name, name, []string{col.Name}
			t.ForeignKeys = append(t.ForeignKeys, fk)
		case p.peek().is("GENERATED") || p.peek().is("AS"):
			p.accept("GENERATED", "ALWAYS")
			if err := p.expect("AS"); err != nil {
				return err
			}
			e, err := p.parens()
			if err != nil {
				return err
			}
			col.GeneratedExpr = e
			if p.accept("STORED") {
				col.GeneratedStored = true
			} else {
				p.accept("VIRTUAL")
			}
		default:
			p.next()
		}
		name = ""
	}
	t.Columns = append(t.Columns, col)
	return nil
}

// setType splits "VARCHAR(255)" or "DECIMAL(10, 2)" into type, length and scale.
func setType(c *design.Column, typ string) {
	open := strings.IndexByte(typ, '(')
	if open < 0 || !strings.HasSuffix(typ, ")") {
		c.Type = typ
		return
	}
	c.Type = strings.TrimSpace(typ[:open])
	args := strings.Split(typ[open+1:len(typ)-1], ",")
	c.Length = strings.TrimSpace(args[0])
	if len(args) > 1 {
		c.Scale = strings.TrimSpace(args[1])
	}
}

// defaultExpr reads DEFAULT's value: (expr), a literal, a signed number or a keyword.
func defaultExpr(p *parser) (string, error) {
	if p.peek().is("(") {
		e, err := p.parens()
		return "(" + e + ")", err
	}
	start := p.pos
	if p.peek().is("+") || p.peek().is("-") {
		p.next()
	}
	if p.eof() {
		return "", fmt.Errorf("DEFAULT without a value")
	}
	p.next()
	return p.text(start, p.pos), nil
}

func parseReferences(p *parser) (design.ForeignKey, error) {
	var fk design.ForeignKey
	if err := p.expect("REFERENCES"); err != nil {
		return fk, err
	}
	fk.RefTable = p.name()
	if p.peek().is("(") {
		cols, err := p.names()
		if err != nil {
			return fk, err
		}
		fk.RefColumns = cols
	}
	for !p.eof() {
		switch {
		case p.accept("ON", "DELETE"):
			fk.OnDelete = action(p)
		case p.accept("ON", "UPDATE"):
			fk.OnUpdate = action(p)
		case p.accept("MATCH"):
			p.next()
		case p.accept("NOT", "DEFERRABLE"), p.accept("DEFERRABLE"):
			if p.accept("INITIALLY") {
				p.next()
			}
		default:
			return fk, nil
		}
	}
	return fk, nil
}

func action(p *parser) string {
	switch {
	case p.accept("SET", "NULL"):
		return "SET NULL"
	case p.accept("SET", "DEFAULT"):
		return "SET DEFAULT"
	case p.accept("NO", "ACTION"):
		return "NO ACTION"
	}
	return strings.ToUpper(p.next().text)
}

func parseTableConstraint(p *parser, t *design.Table) error {
	name := ""
	if p.accept("CONSTRAINT") {
		name = p.next().text
	}
	switch {
	case p.accept("PRIMARY", "KEY"):
		cols, err := p.names()
		if err != nil {
			return err
		}
		t.PrimaryKey = &design.PrimaryKey{Name: name, Columns: cols}
		p.skipConflict()
		// AUTOINCREMENT only exists on a column-level INTEGER PRIMARY KEY.
	case p.accept("UNIQUE"):
		cols, err := p.names()
		if err != nil {
			return err
		}
		t.Uniques = append(t.Uniques, design.Unique{Name: name, OrigName: name, Columns: cols})
	case p.accept("CHECK"):
		e, err := p.parens()
		if err != nil {
			return err
		}
		t.Checks = append(t.Checks, design.Check{Name: name, OrigName: name, Expr: e})
	case p.accept("FOREIGN", "KEY"):
		cols, err := p.names()
		if err != nil {
			return err
		}
		fk, err := parseReferences(p)
		if err != nil {
			return err
		}
		fk.Name, fk.OrigName, fk.Columns = name, name, cols
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}
	return nil
}

// parseCreateIndex reads CREATE [UNIQUE] INDEX name ON table (cols) [WHERE expr].
func parseCreateIndex(src string) (design.Index, error) {
	var ix design.Index
	p := &parser{src: src, toks: lex(src)}
	if err := p.expect("CREATE"); err != nil {
		return ix, err
	}
	if p.accept("UNIQUE") {
		ix.Kind = "unique"
	}
	if err := p.expect("INDEX"); err != nil {
		return ix, err
	}
	p.accept("IF", "NOT", "EXISTS")
	ix.Name = p.name()
	ix.OrigName = ix.Name
	if err := p.expect("ON"); err != nil {
		return ix, err
	}
	p.name()
	cols, err := p.list()
	if err != nil {
		return ix, err
	}
	for _, r := range cols {
		c := p.sub(r)
		end := len(c.toks)
		ic := design.IndexColumn{}
		if end > 0 && c.toks[end-1].is("ASC", "DESC") {
			ic.Desc = c.toks[end-1].is("DESC")
			end--
		}
		if end == 1 && (c.toks[0].kind == tWord || c.toks[0].kind == tQuoted) {
			ic.Name = c.toks[0].text
		} else {
			ic.Expr = c.text(0, end)
		}
		ix.Columns = append(ix.Columns, ic)
	}
	if p.accept("WHERE") {
		ix.Where = p.text(p.pos, len(p.toks))
	}
	return ix, nil
}

// parseCreateTrigger reads a CREATE TRIGGER statement.
func parseCreateTrigger(src string) (design.Trigger, error) {
	tr := design.Trigger{Timing: "BEFORE", ForEachRow: true}
	p := &parser{src: src, toks: lex(src)}
	if err := p.expect("CREATE"); err != nil {
		return tr, err
	}
	p.accept("TEMP")
	p.accept("TEMPORARY")
	if err := p.expect("TRIGGER"); err != nil {
		return tr, err
	}
	p.accept("IF", "NOT", "EXISTS")
	tr.Name = p.name()
	tr.OrigName = tr.Name
	switch {
	case p.accept("BEFORE"):
	case p.accept("AFTER"):
		tr.Timing = "AFTER"
	case p.accept("INSTEAD", "OF"):
		tr.Timing = "INSTEAD OF"
	}
	evStart := p.pos
	for !p.eof() && !p.peek().is("ON") {
		p.next()
	}
	ev := p.text(evStart, p.pos)
	if kw, rest, ok := strings.Cut(ev, " "); ok {
		// UPDATE OF a, b: keywords upper-cased, column names as written.
		if of, cols, ok := strings.Cut(strings.TrimSpace(rest), " "); ok && strings.EqualFold(of, "OF") {
			ev = strings.ToUpper(kw) + " OF " + strings.TrimSpace(cols)
		}
	} else {
		ev = strings.ToUpper(ev)
	}
	tr.Events = []string{ev}
	if err := p.expect("ON"); err != nil {
		return tr, err
	}
	p.name()
	p.accept("FOR", "EACH", "ROW")
	if p.accept("WHEN") {
		start := p.pos
		for !p.eof() && !p.peek().is("BEGIN") {
			if p.peek().is("(") {
				p.parens()
				continue
			}
			p.next()
		}
		tr.When = p.text(start, p.pos)
	}
	if p.eof() {
		return tr, fmt.Errorf("trigger %s has no body", tr.Name)
	}
	tr.Body = strings.TrimSpace(p.src[p.peek().start:])
	return tr, nil
}

// parseCreateView returns a view's SELECT.
func parseCreateView(src string) (string, error) {
	p := &parser{src: src, toks: lex(src)}
	for !p.eof() {
		if p.peek().is("(") {
			p.parens()
			continue
		}
		if p.next().is("AS") {
			return strings.TrimSpace(p.src[p.peek().start:]), nil
		}
	}
	return "", fmt.Errorf("view definition not found")
}
