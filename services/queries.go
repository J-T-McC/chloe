package services

import (
	"fmt"

	"chloe/internal/store"
)

// Saved queries, the history log and open query tabs (drafts) are kept by Chloe,
// encrypted with the keychain key.

func (s *QueryService) SavedQueries(connID, database, schema string) []store.SavedQuery {
	return s.M.Store.Workspace().Queries(connID, database, schema)
}

func (s *QueryService) SavedQuery(id string) (store.SavedQuery, error) {
	return s.M.Store.Workspace().Query(id)
}

// SavedQueryByName finds a saved query from its tree object.
func (s *QueryService) SavedQueryByName(connID, database, schema, name string) (store.SavedQuery, error) {
	for _, q := range s.M.Store.Workspace().Queries(connID, database, schema) {
		if q.Name == name {
			return q, nil
		}
	}
	return store.SavedQuery{}, fmt.Errorf("saved query %q not found", name)
}

// SaveQuery creates (empty ID) or updates a saved query.
func (s *QueryService) SaveQuery(q store.SavedQuery) (store.SavedQuery, error) {
	return s.M.Store.Workspace().SaveQuery(q)
}

func (s *QueryService) RenameQuery(id, name string) (store.SavedQuery, error) {
	return s.M.Store.Workspace().RenameQuery(id, name)
}

func (s *QueryService) DeleteQuery(id string) error { return s.M.Store.Workspace().DeleteQuery(id) }

// UniqueQueryName suggests a name not yet used in the database.
func (s *QueryService) UniqueQueryName(connID, database, schema, base string) string {
	return s.M.Store.Workspace().UniqueQueryName(connID, database, schema, base)
}

// History returns executed statements, newest first ("" for every connection).
func (s *QueryService) History(connID string) []store.HistoryEntry {
	return s.M.Store.Workspace().History(connID)
}

func (s *QueryService) ClearHistory() error { return s.M.Store.Workspace().ClearHistory() }

func (s *QueryService) Drafts() []store.Draft { return s.M.Store.Workspace().Drafts() }

func (s *QueryService) SetDrafts(ds []store.Draft) error { return s.M.Store.Workspace().SetDrafts(ds) }
