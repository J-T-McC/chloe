package sqlsplit

import "strings"

// Kind is what a piece of SQL text is.
type Kind uint8

const (
	Code         Kind = iota
	String            // '…', and "…" on MySQL
	QuotedIdent       // "…", `…` and […] identifiers
	DollarString      // PostgreSQL $tag$…$tag$
	Comment           // -- and # (MySQL) to the end of the line, /* … */
)

// Piece is a span of SQL, s[Start:End].
type Piece struct {
	Kind       Kind
	Start, End int
}

// Pieces cuts sql into code, quoted text and comments by the dialect's rules:
// the same reading Split uses to find statement ends, so a caller looking
// for something in the code sees what the splitter sees. Quoted text or a
// comment left open runs to the end.
//
// It reads one way, so it is not a security check: MySQL runs /*! … */
// "comments" as code, and where its strings end depends on sql_mode. Read-only
// checks go through sqlcommon.OneRead, whose MySQLReadHazard refuses /*!
// outright and reads strings under both escaping rules.
func Pieces(sql string, d Dialect) []Piece {
	var out []Piece
	code := 0
	for i := 0; i < len(sql); {
		kind, end, ok := quoted(sql, i, d)
		if !ok {
			i++
			continue
		}
		if code < i {
			out = append(out, Piece{Code, code, i})
		}
		out = append(out, Piece{kind, i, end})
		i, code = end, end
	}
	if code < len(sql) {
		out = append(out, Piece{Code, code, len(sql)})
	}
	return out
}

// quoted reads the quoted text or comment starting at s[i], returning its
// kind and end; ok is false when s[i] starts neither. A […] identifier needs
// its ]: a lone [ is code.
func quoted(s string, i int, d Dialect) (kind Kind, end int, ok bool) {
	n := len(s)
	switch c := s[i]; {
	case c == '\'':
		return String, skipQuoted(s, i, c, d), true
	case c == '"':
		if d == MySQL {
			return String, skipQuoted(s, i, c, d), true
		}
		return QuotedIdent, skipQuoted(s, i, c, d), true
	case c == '`' && (d == MySQL || d == SQLite):
		return QuotedIdent, skipQuoted(s, i, c, d), true
	case c == '-' && i+1 < n && s[i+1] == '-', c == '#' && d == MySQL:
		return Comment, skipLine(s, i), true
	case c == '/' && i+1 < n && s[i+1] == '*':
		return Comment, skipBlockComment(s, i, d == Postgres), true
	case c == '$' && d == Postgres:
		if tag, ok := dollarTag(s, i); ok {
			if e := strings.Index(s[i+len(tag):], tag); e >= 0 {
				return DollarString, i + len(tag) + e + len(tag), true
			}
			return DollarString, n, true
		}
	case c == '[' && (d == Generic || d == SQLite):
		if j := strings.IndexByte(s[i:], ']'); j >= 0 {
			return QuotedIdent, i + j + 1, true
		}
	}
	return 0, 0, false
}
