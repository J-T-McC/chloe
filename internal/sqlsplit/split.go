// Package sqlsplit splits a SQL script into individual statements, honouring each
// dialect's quoting and comment rules, MySQL's DELIMITER command and PostgreSQL
// dollar-quoted bodies.
package sqlsplit

import (
	"strings"
	"unicode"
)

type Dialect int

const (
	MySQL Dialect = iota
	Postgres
	Generic
	// SQLite: "", `` and [] identifiers; trigger bodies (BEGIN … END) hold semicolons.
	SQLite
)

// Statement is one statement with its location in the original script.
type Statement struct {
	SQL       string `json:"sql"`
	Start     int    `json:"start"`     // byte offset of first non-space char
	End       int    `json:"end"`       // byte offset just past the statement (excluding delimiter)
	StartLine int    `json:"startLine"` // 1-based
}

// Split returns the non-empty statements in script.
func Split(script string, d Dialect) []Statement {
	var out []Statement
	delim := ";"
	stmtStart := 0
	i := 0
	n := len(script)

	// SQLite trigger bodies: words seen in the statement and BEGIN/CASE … END depth.
	words, depth := 0, 0
	create, trigger := false, false
	emit := func(end int) {
		words, depth, create, trigger = 0, 0, false, false
		raw := script[stmtStart:end]
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" && !onlyComments(trimmed, d) {
			lead := len(raw) - len(strings.TrimLeftFunc(raw, unicode.IsSpace))
			start := stmtStart + lead
			out = append(out, Statement{
				SQL:       strings.TrimRightFunc(script[start:end], unicode.IsSpace),
				Start:     start,
				End:       end,
				StartLine: 1 + strings.Count(script[:start], "\n"),
			})
		}
	}

	atLineStart := func(pos int) bool {
		for j := pos - 1; j >= 0; j-- {
			if script[j] == '\n' {
				return true
			}
			if script[j] != ' ' && script[j] != '\t' && script[j] != '\r' {
				return false
			}
		}
		return true
	}

	for i < n {
		c := script[i]

		// MySQL client-side DELIMITER command (must start a line).
		if d == MySQL && (c == 'D' || c == 'd') && atLineStart(i) && hasPrefixFold(script[i:], "delimiter") &&
			i+9 < n && (script[i+9] == ' ' || script[i+9] == '\t') {
			emit(i)
			lineEnd := strings.IndexByte(script[i:], '\n')
			if lineEnd < 0 {
				lineEnd = n - i
			}
			arg := strings.TrimSpace(script[i+9 : i+lineEnd])
			if arg != "" {
				delim = strings.Fields(arg)[0]
			}
			i += lineEnd
			stmtStart = i
			continue
		}

		switch {
		case c == '\'' || c == '"' || (c == '`' && d == MySQL):
			i = skipQuoted(script, i, c, d)
			continue
		case c == '-' && i+1 < n && script[i+1] == '-':
			i = skipLine(script, i)
			continue
		case c == '#' && d == MySQL:
			i = skipLine(script, i)
			continue
		case c == '/' && i+1 < n && script[i+1] == '*':
			i = skipBlockComment(script, i, d == Postgres)
			continue
		case c == '$' && d == Postgres:
			if tag, ok := dollarTag(script, i); ok {
				end := strings.Index(script[i+len(tag):], tag)
				if end < 0 {
					i = n
				} else {
					i += len(tag) + end + len(tag)
				}
				continue
			}
		case d == SQLite && isWordStart(c) && (i == 0 || !isWordChar(script[i-1])):
			j := i + 1
			for j < n && isWordChar(script[j]) {
				j++
			}
			w := strings.ToUpper(script[i:j])
			words++
			switch {
			case words == 1:
				create = w == "CREATE"
			case create && words <= 3 && w == "TRIGGER":
				trigger = true
			case trigger && (w == "BEGIN" || (w == "CASE" && depth > 0)):
				depth++
			case trigger && w == "END" && depth > 0:
				depth--
			}
			i = j
			continue
		case c == '`' && d == SQLite:
			i = skipQuoted(script, i, c, d)
			continue
		case c == '[' && (d == Generic || d == SQLite):
			if j := strings.IndexByte(script[i:], ']'); j >= 0 {
				i += j + 1
				continue
			}
		}

		if strings.HasPrefix(script[i:], delim) && depth == 0 {
			emit(i)
			i += len(delim)
			stmtStart = i
			continue
		}
		i++
	}
	emit(n)
	return out
}

// StatementAt returns the statement containing byte offset pos (or the nearest one
// before it when pos is in whitespace between statements). ok is false for an empty script.
func StatementAt(script string, d Dialect, pos int) (Statement, bool) {
	stmts := Split(script, d)
	if len(stmts) == 0 {
		return Statement{}, false
	}
	best := stmts[0]
	for _, s := range stmts {
		if s.Start <= pos {
			best = s
		}
		if pos >= s.Start && pos <= s.End {
			return s, true
		}
	}
	return best, true
}

func skipQuoted(s string, i int, q byte, d Dialect) int {
	n := len(s)
	// PostgreSQL E'' strings allow backslash escapes; MySQL strings always do (by default).
	backslash := d == MySQL || (d == Postgres && i > 0 && (s[i-1] == 'E' || s[i-1] == 'e') && q == '\'')
	j := i + 1
	for j < n {
		ch := s[j]
		if backslash && ch == '\\' && q != '`' {
			j += 2
			continue
		}
		if ch == q {
			if j+1 < n && s[j+1] == q { // doubled quote escape
				j += 2
				continue
			}
			return j + 1
		}
		j++
	}
	return n
}

func skipLine(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

func skipBlockComment(s string, i int, nested bool) int {
	depth := 0
	n := len(s)
	for i < n {
		if i+1 < n && s[i] == '/' && s[i+1] == '*' {
			depth++
			i += 2
			if !nested && depth > 1 {
				depth = 1
			}
			continue
		}
		if i+1 < n && s[i] == '*' && s[i+1] == '/' {
			depth--
			i += 2
			if depth == 0 {
				return i
			}
			continue
		}
		i++
	}
	return n
}

// dollarTag recognises $tag$ or $$ at position i.
func dollarTag(s string, i int) (string, bool) {
	if i > 0 {
		p := s[i-1]
		if p == '_' || unicode.IsLetter(rune(p)) || unicode.IsDigit(rune(p)) {
			return "", false // part of an identifier like a$b
		}
	}
	j := i + 1
	for j < len(s) {
		ch := s[j]
		if ch == '$' {
			return s[i : j+1], true
		}
		if !(ch == '_' || unicode.IsLetter(rune(ch)) || (j > i+1 && unicode.IsDigit(rune(ch)))) {
			return "", false
		}
		j++
	}
	return "", false
}

func isWordStart(c byte) bool { return c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z') }
func isWordChar(c byte) bool  { return isWordStart(c) || (c >= '0' && c <= '9') || c == '$' }

func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

func onlyComments(s string, d Dialect) bool {
	i := 0
	for i < len(s) {
		switch {
		case unicode.IsSpace(rune(s[i])):
			i++
		case strings.HasPrefix(s[i:], "--") || (d == MySQL && s[i] == '#'):
			i = skipLine(s, i)
		case strings.HasPrefix(s[i:], "/*"):
			i = skipBlockComment(s, i, d == Postgres)
		default:
			return false
		}
	}
	return true
}
