package sqlcommon

import (
	"context"
	"database/sql"
	"regexp"
	"strings"
)

var returningRe = regexp.MustCompile(`(?i)\bRETURNING\b`)

// LeadingKeyword returns the first SQL keyword of a statement, upper-cased,
// skipping whitespace, comments and opening parentheses.
func LeadingKeyword(s string) string {
	i := 0
	for i < len(s) {
		switch {
		case s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r' || s[i] == '(':
			i++
		case strings.HasPrefix(s[i:], "--") || s[i] == '#':
			if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
				i += j + 1
			} else {
				return ""
			}
		case strings.HasPrefix(s[i:], "/*"):
			if j := strings.Index(s[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				return ""
			}
		default:
			j := i
			for j < len(s) && (s[j] == '_' || (s[j]|0x20 >= 'a' && s[j]|0x20 <= 'z')) {
				j++
			}
			return strings.ToUpper(s[i:j])
		}
	}
	return ""
}

// ReturnsRows guesses whether a statement produces a result set, so it can be run
// with Query (rows) or Exec (rows affected).
func ReturnsRows(stmt string) bool {
	switch LeadingKeyword(stmt) {
	case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN", "WITH", "VALUES", "TABLE",
		"CALL", "PRAGMA", "HELP", "ANALYZE", "CHECK", "CHECKSUM", "OPTIMIZE", "REPAIR", "FETCH":
		return true
	case "INSERT", "UPDATE", "DELETE", "MERGE":
		return returningRe.MatchString(stmt)
	}
	return false
}

// Querier is satisfied by *sql.Conn and *sql.DB.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Run executes a statement choosing Query or Exec.
func Run(ctx context.Context, q Querier, stmt string, f Formatter, args ...any) (*Rows, error) {
	if ReturnsRows(stmt) {
		rows, err := q.QueryContext(ctx, stmt, args...)
		if err != nil {
			return nil, err
		}
		return NewRows(rows, 0, f)
	}
	res, err := q.ExecContext(ctx, stmt, args...)
	if err != nil {
		return nil, err
	}
	n, _ := res.RowsAffected()
	return NewRows(nil, n, f)
}

// QueryStrings runs a query and scans every column as a nullable string.
func QueryStrings(ctx context.Context, q Querier, query string, args ...any) ([][]sql.NullString, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out [][]sql.NullString
	for rows.Next() {
		rec := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range rec {
			ptrs[i] = &rec[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}
