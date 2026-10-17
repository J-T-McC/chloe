package core

import (
	"context"
	"fmt"
	"strings"

	"chloe/internal/driver"
	"chloe/internal/model"
)

// SortSpec orders a table browse by one column.
type SortSpec struct {
	Column string `json:"column"`
	Desc   bool   `json:"desc"`
}

// BrowseRequest describes one page of a table's data, as the table viewer requests it.
type BrowseRequest struct {
	Ref    model.ObjectRef `json:"ref"`
	Where  string          `json:"where"` // raw SQL condition from the filter bar
	Sort   []SortSpec      `json:"sort"`
	Offset int             `json:"offset"`
	Limit  int             `json:"limit"`
}

// BrowseTable opens a table page in its own short-lived session. The page is read
// fully (it is bounded by Limit) and stays available via FetchRows until released.
func (m *Manager) BrowseTable(ctx context.Context, connID string, req BrowseRequest) (model.StatementResult, error) {
	c, err := m.conn(connID)
	if err != nil {
		return model.StatementResult{}, err
	}
	q := buildSelect(c.Dialect(), req)
	sid, err := m.OpenSession(ctx, connID, req.Ref.Database, req.Ref.Schema)
	if err != nil {
		return model.StatementResult{}, err
	}
	defer m.CloseSession(sid)
	s, err := m.session(sid)
	if err != nil {
		return model.StatementResult{}, err
	}
	s.run.Lock()
	res := m.runStatement(ctx, s, statementOf(q), false)
	s.run.Unlock()
	// Detach the result from the session so closing the session keeps it alive.
	m.mu.Lock()
	s.results = nil
	m.mu.Unlock()
	return res, nil
}

// CountRows counts the rows matching a browse filter (shown in the status bar).
func (m *Manager) CountRows(ctx context.Context, connID string, ref model.ObjectRef, where string) (int64, error) {
	c, err := m.conn(connID)
	if err != nil {
		return 0, err
	}
	d := c.Dialect()
	q := "SELECT COUNT(*) FROM " + d.Qualified(ref)
	if strings.TrimSpace(where) != "" {
		q += " WHERE " + where
	}
	var n int64
	err = m.WithReadSession(ctx, connID, ref.Database, ref.Schema, func(ds driver.Session, _ driver.Dialect) error {
		rows, err := ds.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		cell := make([]*string, 1)
		if !rows.Next(cell) {
			return rows.Err()
		}
		if cell[0] != nil {
			fmt.Sscan(*cell[0], &n)
		}
		return nil
	})
	return n, err
}

func buildSelect(d driver.Dialect, req BrowseRequest) string {
	var b strings.Builder
	b.WriteString("SELECT * FROM ")
	b.WriteString(d.Qualified(req.Ref))
	if w := strings.TrimSpace(req.Where); w != "" {
		b.WriteString(" WHERE ")
		b.WriteString(w)
	}
	if len(req.Sort) > 0 {
		parts := make([]string, len(req.Sort))
		for i, s := range req.Sort {
			parts[i] = d.QuoteIdent(s.Column)
			if s.Desc {
				parts[i] += " DESC"
			}
		}
		b.WriteString(" ORDER BY ")
		b.WriteString(strings.Join(parts, ", "))
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 1000
	}
	fmt.Fprintf(&b, " LIMIT %d", limit)
	if req.Offset > 0 {
		fmt.Fprintf(&b, " OFFSET %d", req.Offset)
	}
	return b.String()
}

// SampleColumn returns up to limit non-null values of one column (JSON key
// completion builds its key tree from these).
func (m *Manager) SampleColumn(ctx context.Context, connID string, ref model.ObjectRef, column string, limit int) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	d := c.Dialect()
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	col := d.QuoteIdent(column)
	q := fmt.Sprintf("SELECT %s FROM %s WHERE %s IS NOT NULL LIMIT %d", col, d.Qualified(ref), col, limit)
	var out []string
	err = m.WithReadSession(ctx, connID, ref.Database, ref.Schema, func(ds driver.Session, _ driver.Dialect) error {
		rows, err := ds.Query(ctx, q)
		if err != nil {
			return err
		}
		defer rows.Close()
		cell := make([]*string, 1)
		for rows.Next(cell) {
			if cell[0] != nil {
				out = append(out, *cell[0])
			}
		}
		return rows.Err()
	})
	return out, err
}
