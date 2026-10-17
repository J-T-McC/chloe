package sqlcommon

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"chloe/internal/sqlsplit"
)

// ErrNotRead is wrapped by OneRead's error for a statement that doesn't start
// with one of the accepted keywords.
var ErrNotRead = errors.New("only runs read statements")

// OneRead is the check run_query and BI query sources share: sql must be exactly
// one statement starting with one of starts and, on MySQL, hold nothing the
// server could read as something else (MySQLReadHazard). The read-only
// transaction the statement then runs in is what really stops writes; this
// keeps out statements that would end that transaction (MySQL DDL commits
// implicitly) or write files. what names the caller in errors.
func OneRead(sql string, d sqlsplit.Dialect, starts []string, what string) (string, error) {
	stmts := sqlsplit.Split(sql, d)
	if len(stmts) != 1 {
		return "", fmt.Errorf("%s takes exactly one statement (got %d)", what, len(stmts))
	}
	s := strings.TrimSpace(stmts[0].SQL)
	if !slices.Contains(starts, LeadingKeyword(s)) {
		return "", fmt.Errorf("%s %w (%s)", what, ErrNotRead, strings.Join(starts, ", "))
	}
	if d == sqlsplit.MySQL {
		if err := MySQLReadHazard(s); err != nil {
			return "", fmt.Errorf("%s: %w", what, err)
		}
	}
	return s, nil
}
