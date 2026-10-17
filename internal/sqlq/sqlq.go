// Package sqlq quotes values into SQL text. It has no dependencies so every layer can use it.
package sqlq

import "strings"

// PGString quotes s as a Postgres string literal that reads the same whatever
// standard_conforming_strings is set to: text with a backslash becomes E'…' with the
// backslashes doubled, so a backslash can never escape the closing quote.
func PGString(s string) string {
	q := strings.ReplaceAll(s, "'", "''")
	if strings.Contains(s, `\`) {
		return "E'" + strings.ReplaceAll(q, `\`, `\\`) + "'"
	}
	return "'" + q + "'"
}

// Balanced reports whether the parentheses in an SQL expression pair up,
// ignoring any inside '…', "…" or `…` (a doubled quote closes and reopens, so
// it needs no case of its own). Backslash escapes aren't followed: MySQL
// writes the quotes of a stored CHECK as \', which then read as plain quotes.
// When unsure it says false, so a caller keeps the parentheses it would strip.
func Balanced(s string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '\'' || ch == '"' || ch == '`':
			quote = ch
		case ch == '(':
			depth++
		case ch == ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0 && quote == 0
}
