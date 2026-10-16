package sqlcommon

import (
	"errors"
	"regexp"
	"strings"
)

var (
	intoFileRe    = regexp.MustCompile(`(?i)\bINTO\s+(OUTFILE|DUMPFILE)\b`)
	execCommentRe = regexp.MustCompile(`(?i)/\*M?!`)
)

var (
	ErrExecComment  = errors.New("MySQL executable comments (/*! … */) aren't allowed")
	ErrIntoFile     = errors.New("can't write files (INTO OUTFILE or DUMPFILE)")
	ErrUnterminated = errors.New("the statement has an unterminated string, identifier or comment")
)

// MySQLReadHazard reports what in a MySQL statement a keyword check can't see:
// executable comments, which MySQL runs as code (/*!CREATE TABLE t AS*/ SELECT …
// reads as a SELECT), and INTO OUTFILE/DUMPFILE however it is spaced or
// commented. It fails closed: where strings end depends on the server's
// sql_mode (NO_BACKSLASH_ESCAPES, ANSI_QUOTES), so it refuses /*! anywhere, even
// in what looks like a string, checks INTO … FILE under both escaping rules, and
// refuses a statement left unterminated under both.
func MySQLReadHazard(s string) error {
	if execCommentRe.MatchString(s) {
		return ErrExecComment
	}
	// A statement left open under one escaping rule fails to parse on a server
	// using it, so only the readings that close are checked; none closing is an error.
	parsed := false
	for _, backslash := range []bool{true, false} {
		code, ok := mysqlCode(s, backslash)
		if !ok {
			continue
		}
		parsed = true
		if intoFileRe.MatchString(code) {
			return ErrIntoFile
		}
	}
	if !parsed {
		return ErrUnterminated
	}
	return nil
}

// mysqlCode blanks quoted text and comments out of s, reading backslashes in
// strings as escapes or not. ok is false when something is left open.
func mysqlCode(s string, backslash bool) (string, bool) {
	var code strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j, closed := i+1, false
			for j < len(s) {
				if backslash && s[j] == '\\' && c != '`' {
					j += 2
					continue
				}
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					closed = true
					break
				}
				j++
			}
			if !closed {
				return "", false
			}
			code.WriteString(" '' ")
			i = j + 1
		case strings.HasPrefix(s[i:], "/*"):
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				return "", false
			}
			i += j + 4
			code.WriteByte(' ')
		case c == '#' || (strings.HasPrefix(s[i:], "--") && (i+2 == len(s) || s[i+2] <= ' ')):
			j := strings.IndexByte(s[i:], '\n')
			if j < 0 {
				i = len(s)
			} else {
				i += j
			}
			code.WriteByte(' ')
		default:
			code.WriteByte(c)
			i++
		}
	}
	return code.String(), true
}
