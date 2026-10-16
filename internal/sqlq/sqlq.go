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
