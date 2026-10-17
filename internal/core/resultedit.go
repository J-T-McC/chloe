package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// ResultEdit says whether a query result can be edited in place and, if so, how its
// columns map onto the one table it came from. Only a plain single-table SELECT whose
// result includes the table's whole primary key qualifies: anything else (joins,
// grouping, DISTINCT, set operations, no primary key) is read-only with a Reason.
type ResultEdit struct {
	Editable bool            `json:"editable"`
	Reason   string          `json:"reason,omitempty"`
	Ref      model.ObjectRef `json:"ref"`
	// Columns holds, per result column, the table column it shows, or "" for a
	// computed column (which stays read-only).
	Columns []string `json:"columns"`
	// Key lists the primary key columns, which identify a row in updates and deletes.
	Key []string `json:"key"`
}

func readOnly(format string, args ...any) ResultEdit {
	return ResultEdit{Reason: fmt.Sprintf(format, args...)}
}

// ResultEditTarget works out whether a buffered query result can be edited.
func (m *Manager) ResultEditTarget(ctx context.Context, resultID string) (ResultEdit, error) {
	m.mu.Lock()
	rs, ok := m.results[resultID]
	m.mu.Unlock()
	if !ok {
		return ResultEdit{}, fmt.Errorf("result %s has been released", resultID)
	}
	if rs.origin.connID == "" {
		return readOnly("not a query result"), nil
	}
	c, err := m.conn(rs.origin.connID)
	if err != nil {
		return ResultEdit{}, err
	}
	d := c.Dialect()

	sel, why := parseSimpleSelect(rs.origin.sql, d.Split)
	if why != "" {
		return readOnly("%s", why), nil
	}

	// Resolve the table against the session's database and schema.
	ref := model.ObjectRef{Database: rs.origin.database, Schema: rs.origin.schema, Kind: model.KindTable}
	fold := func(id ident) string {
		if !id.quoted && d.Name == "postgres" {
			return strings.ToLower(id.text)
		}
		return id.text
	}
	parts := sel.table
	ref.Name = fold(parts[len(parts)-1])
	switch len(parts) {
	case 2:
		if d.HasSchemas {
			ref.Schema = fold(parts[0])
		} else {
			ref.Database = fold(parts[0])
		}
	case 3:
		if !d.HasSchemas || fold(parts[0]) != ref.Database {
			return readOnly("the table is in another database"), nil
		}
		ref.Schema = fold(parts[1])
	}
	if d.HasSchemas && ref.Schema == "" {
		ref.Schema = d.DefaultSchema
	}

	detail, err := c.TableDetail(ctx, ref)
	if err != nil || len(detail.Columns) == 0 {
		return readOnly("%s is not a table", ref.Name), nil
	}
	byName := map[string]string{} // folded name -> actual column name
	exact := map[string]bool{}
	for _, col := range detail.Columns {
		byName[strings.ToLower(col.Name)] = col.Name
		exact[col.Name] = true
	}
	lookup := func(id ident) string {
		if id.quoted || d.Name == "postgres" {
			n := fold(id)
			if exact[n] {
				return n
			}
			return ""
		}
		return byName[strings.ToLower(id.text)]
	}

	// Expand the select list into one entry per result column.
	var mapped []string
	qualifierOK := func(q []ident) bool {
		if len(q) == 0 {
			return true
		}
		last := q[len(q)-1]
		if sel.alias != nil {
			return len(q) == 1 && strings.EqualFold(last.text, sel.alias.text)
		}
		return strings.EqualFold(last.text, parts[len(parts)-1].text)
	}
	for _, it := range sel.items {
		switch {
		case it.star:
			if !qualifierOK(it.qualifier) {
				return readOnly("unrecognised column list"), nil
			}
			for _, col := range detail.Columns {
				mapped = append(mapped, col.Name)
			}
		case it.column != nil:
			name := lookup(*it.column)
			if name == "" || !qualifierOK(it.qualifier) {
				mapped = append(mapped, "") // not a column of this table
			} else {
				mapped = append(mapped, name)
			}
		default:
			mapped = append(mapped, "")
		}
	}
	if len(mapped) != len(rs.columns) {
		return readOnly("unrecognised column list"), nil
	}
	// The driver's column names must agree with the parse, or we refuse to guess.
	for i, rc := range rs.columns {
		it := sel.itemFor(i, len(detail.Columns))
		if mapped[i] == "" || it == nil {
			continue
		}
		want := mapped[i]
		if it.alias != nil {
			want = it.alias.text
		}
		if !strings.EqualFold(rc.Name, want) {
			return readOnly("unrecognised column list"), nil
		}
	}

	var key []string
	for _, col := range detail.Columns {
		if col.PrimaryKey {
			key = append(key, col.Name)
		}
	}
	if len(key) == 0 {
		return readOnly("%s has no primary key", ref.Name), nil
	}
	for _, k := range key {
		found := false
		for _, mc := range mapped {
			found = found || mc == k
		}
		if !found {
			return readOnly("the result doesn't include primary key column %s", k), nil
		}
	}
	return ResultEdit{Editable: true, Ref: ref, Columns: mapped, Key: key}, nil
}

// ResultPatch brings a buffered result in line with edits that were applied to the
// table, so the grid keeps showing them without re-running the query.
type ResultPatch struct {
	Updated  map[int]map[int]*string `json:"updated"`  // row -> column -> value
	Deleted  []int                   `json:"deleted"`  // rows to drop
	Inserted [][]*string             `json:"inserted"` // rows to append
}

// PatchResult applies a ResultPatch to a complete buffered result.
func (m *Manager) PatchResult(resultID string, p ResultPatch) (model.StatementResult, error) {
	m.mu.Lock()
	rs, ok := m.results[resultID]
	m.mu.Unlock()
	if !ok {
		return model.StatementResult{}, fmt.Errorf("result %s has been released", resultID)
	}
	rs.mu.Lock()
	if !rs.complete {
		rs.mu.Unlock()
		return model.StatementResult{}, errors.New("result is still loading")
	}
	for r, cols := range p.Updated {
		if r < 0 || r >= len(rs.rows) {
			continue
		}
		row := append([]*string(nil), rs.rows[r]...)
		for c, v := range cols {
			if c >= 0 && c < len(row) {
				row[c] = v
			}
		}
		rs.rows[r] = row
	}
	if len(p.Deleted) > 0 {
		drop := map[int]bool{}
		for _, r := range p.Deleted {
			drop[r] = true
		}
		kept := rs.rows[:0]
		for i, row := range rs.rows {
			if !drop[i] {
				kept = append(kept, row)
			}
		}
		rs.rows = kept
	}
	for _, row := range p.Inserted {
		if len(row) == len(rs.columns) {
			rs.rows = append(rs.rows, row)
		}
	}
	rs.mu.Unlock()
	return m.ResultStatus(resultID)
}

// ---- parsing ----------------------------------------------------------------------

type ident struct {
	text   string
	quoted bool
}

type selectItem struct {
	star      bool
	qualifier []ident // t in t.* or t.col
	column    *ident  // a plain column reference
	alias     *ident
}

type simpleSelect struct {
	items []selectItem
	table []ident // [db.][schema.]table
	alias *ident
}

// itemFor returns the select item that produced result column i, or nil for a star.
func (s simpleSelect) itemFor(i, starWidth int) *selectItem {
	pos := 0
	for k := range s.items {
		w := 1
		if s.items[k].star {
			w = starWidth
		}
		if i < pos+w {
			if s.items[k].star {
				return nil
			}
			return &s.items[k]
		}
		pos += w
	}
	return nil
}

type tok struct {
	kind   byte // 'i' identifier/keyword, 'q' quoted identifier, 's' string, 'n' number, 'p' punctuation
	text   string
	quoted bool
}

func (t tok) is(kw string) bool { return t.kind == 'i' && strings.EqualFold(t.text, kw) }

// lexSQL tokenises enough SQL to recognise a simple SELECT. Quoting and
// comments are read by the statement splitter's rules (sqlsplit.Pieces);
// comments are dropped.
func lexSQL(s string, d sqlsplit.Dialect) []tok {
	var out []tok
	for _, p := range sqlsplit.Pieces(s, d) {
		text := s[p.Start:p.End]
		switch p.Kind {
		case sqlsplit.Comment:
		case sqlsplit.String, sqlsplit.DollarString:
			// MySQL reads "..." as a string unless ANSI_QUOTES; either way it isn't a column.
			out = append(out, tok{kind: 's', text: text, quoted: text[0] == '"'})
		case sqlsplit.QuotedIdent:
			out = append(out, tok{kind: 'q', text: unquoteIdent(text), quoted: true})
		default:
			out = lexCode(out, text)
		}
	}
	return out
}

// unquoteIdent strips an identifier's quotes and undoes doubled quote characters.
func unquoteIdent(q string) string {
	open, inner := q[0], q[1:]
	end := open
	if open == '[' {
		end = ']'
	}
	inner = strings.TrimSuffix(inner, string(end)) // an unclosed one runs to the end
	if end == ']' {
		return inner
	}
	return strings.ReplaceAll(inner, string(end)+string(end), string(end))
}

// lexCode splits code (no quoting or comments) into words, numbers and punctuation.
func lexCode(out []tok, s string) []tok {
	i, n := 0, len(s)
	for i < n {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '_' || unicode.IsLetter(rune(c)) || c >= 0x80:
			j := i
			for j < n && (s[j] == '_' || s[j] == '$' || s[j] >= 0x80 || unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			out = append(out, tok{kind: 'i', text: s[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i
			for j < n && (s[j] == '.' || s[j] == '_' || unicode.IsLetter(rune(s[j])) || unicode.IsDigit(rune(s[j]))) {
				j++
			}
			out = append(out, tok{kind: 'n', text: s[i:j]})
			i = j
		default:
			out = append(out, tok{kind: 'p', text: string(c)})
			i++
		}
	}
	return out
}

// Words that end a FROM clause's table reference or can't be an alias.
var clauseWords = map[string]bool{
	"where": true, "order": true, "limit": true, "offset": true, "fetch": true, "for": true,
	"group": true, "having": true, "window": true, "union": true, "intersect": true, "except": true,
	"join": true, "inner": true, "left": true, "right": true, "full": true, "cross": true, "natural": true,
	"straight_join": true, "lock": true, "into": true, "on": true, "using": true, "as": true, "from": true,
	"select": true, "tablesample": true, "partition": true, "use": true, "force": true, "ignore": true,
}

// parseSimpleSelect recognises SELECT <items> FROM <table> [alias] [WHERE …] [ORDER BY …]
// [LIMIT …]. It returns a reason when the statement is anything more complex.
func parseSimpleSelect(sql string, dialect sqlsplit.Dialect) (simpleSelect, string) {
	const complex = "the query isn't a simple single-table SELECT"
	ts := lexSQL(sql, dialect)
	for len(ts) > 0 && ts[len(ts)-1].kind == 'p' && ts[len(ts)-1].text == ";" {
		ts = ts[:len(ts)-1]
	}
	if len(ts) == 0 || !ts[0].is("select") {
		return simpleSelect{}, complex
	}

	// Split the statement at depth 0 and reject anything that combines or groups rows.
	from := -1
	depth := 0
	for i, t := range ts {
		if t.kind == 'p' {
			switch t.text {
			case "(":
				depth++
			case ")":
				depth--
			case ";":
				return simpleSelect{}, complex
			}
			continue
		}
		if depth != 0 {
			continue
		}
		switch {
		case t.is("from") && from < 0:
			from = i
		case t.is("union") || t.is("intersect") || t.is("except"):
			return simpleSelect{}, "the query combines results (UNION/INTERSECT/EXCEPT)"
		case t.is("join") || t.is("straight_join"):
			return simpleSelect{}, "the query joins tables"
		case t.is("group") || t.is("having"):
			return simpleSelect{}, "the query groups rows"
		case t.is("distinct") && i == 1:
			return simpleSelect{}, "the query uses DISTINCT"
		case t.is("into"):
			return simpleSelect{}, complex
		}
	}
	if from < 0 {
		return simpleSelect{}, "the query has no table"
	}

	var out simpleSelect
	// Select list: items separated by top-level commas.
	start := 1
	if ts[1].is("all") {
		start = 2
	}
	var cur []tok
	depth = 0
	flush := func() bool {
		if len(cur) == 0 {
			return false
		}
		out.items = append(out.items, parseItem(cur))
		cur = nil
		return true
	}
	for _, t := range ts[start:from] {
		if t.kind == 'p' && t.text == "(" {
			depth++
		} else if t.kind == 'p' && t.text == ")" {
			depth--
		}
		if depth == 0 && t.kind == 'p' && t.text == "," {
			if !flush() {
				return simpleSelect{}, complex
			}
			continue
		}
		cur = append(cur, t)
	}
	if !flush() {
		return simpleSelect{}, complex
	}

	// Table reference: name[.name[.name]] [AS] [alias], then end or a clause keyword.
	i := from + 1
	for {
		if i >= len(ts) || !isName(ts[i]) {
			return simpleSelect{}, complex
		}
		out.table = append(out.table, ident{text: ts[i].text, quoted: ts[i].quoted})
		i++
		if i < len(ts) && ts[i].kind == 'p' && ts[i].text == "." && len(out.table) < 3 {
			i++
			continue
		}
		break
	}
	if i < len(ts) && ts[i].is("as") {
		i++
		if i >= len(ts) || !isName(ts[i]) {
			return simpleSelect{}, complex
		}
	}
	if i < len(ts) && isName(ts[i]) && !(ts[i].kind == 'i' && clauseWords[strings.ToLower(ts[i].text)]) {
		out.alias = &ident{text: ts[i].text, quoted: ts[i].quoted}
		i++
	}
	if i < len(ts) {
		t := ts[i]
		if t.kind == 'p' && t.text == "," {
			return simpleSelect{}, "the query joins tables"
		}
		if !(t.is("where") || t.is("order") || t.is("limit") || t.is("offset") || t.is("fetch") || t.is("for")) {
			return simpleSelect{}, complex
		}
	}
	return out, ""
}

func isName(t tok) bool { return t.kind == 'i' || t.kind == 'q' }

// parseItem classifies one select-list item: *, t.*, a column (optionally qualified),
// each optionally aliased; anything else is an expression.
func parseItem(ts []tok) selectItem {
	var it selectItem
	// Peel a trailing alias: "… AS name" or "… name" after a complete reference.
	if n := len(ts); n >= 3 && ts[n-2].is("as") && isName(ts[n-1]) {
		it.alias = &ident{text: ts[n-1].text, quoted: ts[n-1].quoted}
		ts = ts[:n-2]
	} else if n >= 2 && isName(ts[n-1]) && (isName(ts[n-2]) || (ts[n-2].kind == 'p' && ts[n-2].text == ")")) {
		it.alias = &ident{text: ts[n-1].text, quoted: ts[n-1].quoted}
		ts = ts[:n-1]
	}
	// name(.name)* with an optional final *.
	var names []ident
	for k := 0; k < len(ts); k++ {
		t := ts[k]
		if k%2 == 1 {
			if t.kind != 'p' || t.text != "." {
				return selectItem{alias: it.alias}
			}
			continue
		}
		if t.kind == 'p' && t.text == "*" && k == len(ts)-1 {
			if it.alias != nil {
				return selectItem{alias: it.alias}
			}
			it.star = true
			it.qualifier = names
			return it
		}
		if !isName(t) {
			return selectItem{alias: it.alias}
		}
		names = append(names, ident{text: t.text, quoted: t.quoted})
	}
	if len(names) == 0 || len(ts)%2 == 0 {
		return selectItem{alias: it.alias}
	}
	col := names[len(names)-1]
	it.column = &col
	it.qualifier = names[:len(names)-1]
	return it
}
