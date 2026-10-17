package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// ChangeKind is the type of a grid edit.
type ChangeKind string

const (
	ChangeInsert ChangeKind = "insert"
	ChangeUpdate ChangeKind = "update"
	ChangeDelete ChangeKind = "delete"
)

// RowChange is one pending grid edit. Key holds the row's original values used to
// identify it (primary key columns when the table has one, else every column).
// Values holds the new values for inserted/updated columns. nil means NULL.
type RowChange struct {
	Kind   ChangeKind         `json:"kind"`
	Key    map[string]*string `json:"key"`
	Values map[string]*string `json:"values"`
}

// ChangeResult reports the outcome of one change.
type ChangeResult struct {
	SQL          string `json:"sql"`
	RowsAffected int64  `json:"rowsAffected"`
	Error        string `json:"error,omitempty"`
}

// ApplyChanges writes grid edits in a single transaction. If any change fails or
// would touch more than one row, everything is rolled back.
func (m *Manager) ApplyChanges(ctx context.Context, connID string, ref model.ObjectRef, changes []RowChange) ([]ChangeResult, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	d := c.Dialect()
	ds, err := c.NewSession(ctx, ref.Database, ref.Schema)
	if err != nil {
		return nil, err
	}
	defer ds.Close()

	results := make([]ChangeResult, len(changes))
	rolledBack := errors.New("changes rolled back")
	err = driver.InTx(ctx, ds, func() error {
		for i, ch := range changes {
			q, args, err := buildChange(d, ref, ch)
			results[i].SQL = q
			if err != nil {
				results[i].Error = err.Error()
				return rolledBack
			}
			n, err := ds.Exec(ctx, q, args...)
			results[i].RowsAffected = n
			if err != nil {
				results[i].Error = err.Error()
				return rolledBack
			}
			if ch.Kind != ChangeInsert && n > 1 {
				results[i].Error = fmt.Sprintf("statement would affect %d rows; the table needs a primary key to edit safely", n)
				return rolledBack
			}
		}
		return nil
	})
	return results, err
}

func buildChange(d driver.Dialect, ref model.ObjectRef, ch RowChange) (string, []any, error) {
	table := d.Qualified(ref)
	var args []any
	arg := func(v *string) string {
		if v == nil {
			args = append(args, nil)
		} else {
			args = append(args, *v)
		}
		return d.Placeholder(len(args))
	}
	where := func() (string, error) {
		if len(ch.Key) == 0 {
			return "", errors.New("row has no key values")
		}
		var conds []string
		for _, col := range sortedKeys(ch.Key) {
			v := ch.Key[col]
			if v == nil {
				conds = append(conds, d.QuoteIdent(col)+" IS NULL")
			} else {
				conds = append(conds, d.QuoteIdent(col)+" = "+arg(v))
			}
		}
		return strings.Join(conds, " AND "), nil
	}

	switch ch.Kind {
	case ChangeInsert:
		if len(ch.Values) == 0 {
			if d.Name != "mysql" {
				return "INSERT INTO " + table + " DEFAULT VALUES", nil, nil
			}
			return "INSERT INTO " + table + " () VALUES ()", nil, nil
		}
		cols := sortedKeys(ch.Values)
		quoted := make([]string, len(cols))
		ph := make([]string, len(cols))
		for i, col := range cols {
			quoted[i] = d.QuoteIdent(col)
			ph[i] = arg(ch.Values[col])
		}
		return fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(quoted, ", "), strings.Join(ph, ", ")), args, nil
	case ChangeUpdate:
		if len(ch.Values) == 0 {
			return "", nil, errors.New("update has no values")
		}
		var sets []string
		for _, col := range sortedKeys(ch.Values) {
			sets = append(sets, d.QuoteIdent(col)+" = "+arg(ch.Values[col]))
		}
		w, err := where()
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("UPDATE %s SET %s WHERE %s", table, strings.Join(sets, ", "), w), args, nil
	case ChangeDelete:
		w, err := where()
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("DELETE FROM %s WHERE %s", table, w), args, nil
	}
	return "", nil, fmt.Errorf("unknown change kind %q", ch.Kind)
}

func sortedKeys(m map[string]*string) []string { return slices.Sorted(maps.Keys(m)) }

// statementOf wraps generated SQL as a script statement.
func statementOf(sql string) sqlsplit.Statement {
	return sqlsplit.Statement{SQL: sql, StartLine: 1}
}
