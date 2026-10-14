package core

import (
	"fmt"
	"strings"
	"time"

	"chloe/internal/model"
	"chloe/internal/store"
)

// savedQueryObjects lists a database's saved queries as tree objects. They are
// Chloe's, so this works without a connection to the server.
func (m *Manager) savedQueryObjects(connID, database, schema string) []model.ObjectInfo {
	qs := m.Store.Workspace().Queries(connID, database, schema)
	out := make([]model.ObjectInfo, 0, len(qs))
	for _, q := range qs {
		out = append(out, model.ObjectInfo{
			ObjectRef: model.ObjectRef{Database: q.Database, Schema: q.Schema, Name: q.Name, Kind: model.KindQuery},
			Comment:   firstLine(q.SQL, 120),
			Created:   localTime(q.Created),
			Modified:  localTime(q.Modified),
		})
	}
	return out
}

// firstLine is the first non-blank line of text, cut to max runes.
func firstLine(text string, max int) string {
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > max {
				return string(r[:max]) + "…"
			}
			return l
		}
	}
	return ""
}

// recordHistory logs the statements a query tab ran.
func (m *Manager) recordHistory(s *session, results []model.StatementResult) {
	entries := make([]store.HistoryEntry, 0, len(results))
	for _, r := range results {
		rows := r.RowsAffected
		if r.ResultID != "" {
			rows = -1 // a result set, still being read
		}
		entries = append(entries, store.HistoryEntry{ConnID: s.connID, Database: s.database, Schema: s.schema, SQL: r.SQL, DurationMs: r.DurationMs, Rows: rows, Error: r.Error})
	}
	m.Store.Workspace().AddHistory(entries...)
}

func (m *Manager) savedQuerySQL(connID string, ref model.ObjectRef) (string, error) {
	for _, q := range m.Store.Workspace().Queries(connID, ref.Database, ref.Schema) {
		if q.Name == ref.Name {
			return q.SQL, nil
		}
	}
	return "", fmt.Errorf("saved query %q not found", ref.Name)
}

// localTime shows a stored UTC time the way the server's object times read.
func localTime(rfc3339 string) string {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return rfc3339
	}
	return t.Local().Format("2006-01-02 15:04:05")
}
