package sqlcommon

import (
	"errors"
	"regexp"
	"strings"
)

var intoFileRe = regexp.MustCompile(`(?i)\bINTO\s+(OUTFILE|DUMPFILE)\b`)

var (
	ErrExecComment = errors.New("MySQL executable comments (/*! … */) aren't allowed")
	ErrIntoFile    = errors.New("can't write files (INTO OUTFILE or DUMPFILE)")
)

// MySQLReadHazard reports what in a MySQL statement a keyword check can't see:
// executable comments, which MySQL runs as code (/*!CREATE TABLE t AS*/ SELECT …
// reads as a SELECT), and INTO OUTFILE/DUMPFILE however it is spaced or
// commented. Quoted strings and identifiers are skipped.
func MySQLReadHazard(s string) error {
	var code strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			for j < len(s) {
				if s[j] == '\\' && c != '`' {
					j += 2
					continue
				}
				if s[j] == c {
					if j+1 < len(s) && s[j+1] == c {
						j += 2
						continue
					}
					break
				}
				j++
			}
			code.WriteString(" '' ")
			i = j + 1
		case strings.HasPrefix(s[i:], "/*"):
			if strings.HasPrefix(s[i+2:], "!") || strings.HasPrefix(s[i+2:], "M!") {
				return ErrExecComment
			}
			j := strings.Index(s[i+2:], "*/")
			if j < 0 {
				i = len(s)
			} else {
				i += j + 4
			}
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
	if intoFileRe.MatchString(code.String()) {
		return ErrIntoFile
	}
	return nil
}
