package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// SavedQuery is a named query kept under a connection's database (and schema),
// shown in its Queries folder.
type SavedQuery struct {
	ID       string `json:"id"`
	ConnID   string `json:"connId"`
	Database string `json:"database"`
	Schema   string `json:"schema,omitempty"`
	Name     string `json:"name"`
	SQL      string `json:"sql"`
	Created  string `json:"created"`
	Modified string `json:"modified"`
}

// HistoryEntry is one executed statement.
type HistoryEntry struct {
	Time       string `json:"time"`
	ConnID     string `json:"connId"`
	Database   string `json:"database,omitempty"`
	Schema     string `json:"schema,omitempty"`
	SQL        string `json:"sql"`
	DurationMs int64  `json:"durationMs"`
	// Rows returned or affected.
	Rows  int64  `json:"rows"`
	Error string `json:"error,omitempty"`
}

// Draft is the text of an open query tab, kept so it survives a quit or crash.
type Draft struct {
	TabID    string `json:"tabId"`
	ConnID   string `json:"connId"`
	Database string `json:"database,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Title    string `json:"title"`
	// QueryID is the saved query the tab edits ("" for a new one).
	QueryID string `json:"queryId,omitempty"`
	SQL     string `json:"sql"`
	// Kind is the tab kind ("query" or "mongoquery").
	Kind string `json:"kind"`
}

// MaxHistory bounds the history log; the oldest entries go first.
const MaxHistory = 5000

// Workspace holds saved queries, the history log and drafts. Like the
// connections file, each is encrypted with the keychain key.
type Workspace struct {
	s  *Store
	mu sync.Mutex

	queries []SavedQuery
	history []HistoryEntry // oldest first
	drafts  []Draft

	historyTimer *time.Timer
	historyDirty bool
	err          error // why the workspace couldn't be read; it then never writes
}

// Workspace returns the store's saved queries, history and drafts, reading them on first use.
func (s *Store) Workspace() *Workspace {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.work == nil {
		w := &Workspace{s: s}
		if s.locked != "" {
			w.err = errors.New(s.locked)
		} else {
			for name, v := range map[string]any{"queries.json": &w.queries, "history.json": &w.history, "drafts.json": &w.drafts} {
				if err := s.readDoc(name, v); err != nil && w.err == nil {
					w.err = fmt.Errorf("%s: %w", name, err)
				}
			}
		}
		s.work = w
	}
	return s.work
}

func (s *Store) docPath(name string) string { return filepath.Join(filepath.Dir(s.path), name) }

func (s *Store) readDoc(name string, v any) error {
	data, err := os.ReadFile(s.docPath(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if isSealed(data) {
		if data, err = s.openFile(data); err != nil {
			return err
		}
	}
	return json.Unmarshal(data, v)
}

func (s *Store) writeDoc(name string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if s.encrypts() {
		if data, err = s.sealFile(data); err != nil {
			return err
		}
	}
	return writeAtomic(s.docPath(name), data)
}

func (w *Workspace) writeLocked(name string, v any) error {
	if w.err != nil {
		return fmt.Errorf("saved queries are unavailable: %w", w.err)
	}
	return w.s.writeDoc(name, v)
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ---- saved queries -------------------------------------------------------------------

// Queries lists the saved queries of a database (and schema), by name.
func (w *Workspace) Queries(connID, database, schema string) []SavedQuery {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := []SavedQuery{}
	for _, q := range w.queries {
		if q.ConnID == connID && q.Database == database && q.Schema == schema {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Query returns a saved query by ID.
func (w *Workspace) Query(id string) (SavedQuery, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, q := range w.queries {
		if q.ID == id {
			return q, nil
		}
	}
	return SavedQuery{}, errors.New("saved query not found")
}

func (w *Workspace) nameTakenLocked(q SavedQuery) bool {
	for _, o := range w.queries {
		if o.ID != q.ID && o.ConnID == q.ConnID && o.Database == q.Database && o.Schema == q.Schema && strings.EqualFold(o.Name, q.Name) {
			return true
		}
	}
	return false
}

// SaveQuery creates a query (empty ID) or updates one. Names are unique within a database.
func (w *Workspace) SaveQuery(q SavedQuery) (SavedQuery, error) {
	q.Name = strings.TrimSpace(q.Name)
	if q.Name == "" {
		return q, errors.New("a query needs a name")
	}
	if q.ConnID == "" {
		return q, errors.New("a query needs a connection")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.nameTakenLocked(q) {
		return q, fmt.Errorf("a query named %q already exists here", q.Name)
	}
	q.Modified = now()
	i := -1
	for j, o := range w.queries {
		if o.ID == q.ID {
			i = j
		}
	}
	switch {
	case q.ID == "":
		q.ID, q.Created = uuid.NewString(), q.Modified
		w.queries = append(w.queries, q)
	case i < 0:
		return q, errors.New("saved query not found")
	default:
		q.Created = w.queries[i].Created
		w.queries[i] = q
	}
	return q, w.writeLocked("queries.json", w.queries)
}

// RenameQuery renames a saved query.
func (w *Workspace) RenameQuery(id, name string) (SavedQuery, error) {
	q, err := w.Query(id)
	if err != nil {
		return q, err
	}
	q.Name = name
	return w.SaveQuery(q)
}

// DeleteQuery removes a saved query.
func (w *Workspace) DeleteQuery(id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(w.queries)
	w.queries = deleteWhere(w.queries, func(q SavedQuery) bool { return q.ID == id })
	if len(w.queries) == n {
		return errors.New("saved query not found")
	}
	return w.writeLocked("queries.json", w.queries)
}

// UniqueQueryName returns base, or base with a number, unused in the database.
func (w *Workspace) UniqueQueryName(connID, database, schema, base string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	q := SavedQuery{ConnID: connID, Database: database, Schema: schema, Name: base}
	for i := 2; w.nameTakenLocked(q); i++ {
		q.Name = fmt.Sprintf("%s %d", base, i)
	}
	return q.Name
}

func deleteWhere[T any](xs []T, del func(T) bool) []T {
	out := xs[:0]
	for _, x := range xs {
		if !del(x) {
			out = append(out, x)
		}
	}
	return out
}

// forgetConnection drops a deleted connection's queries, history and drafts.
func (w *Workspace) forgetConnection(connID string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.queries = deleteWhere(w.queries, func(q SavedQuery) bool { return q.ConnID == connID })
	w.history = deleteWhere(w.history, func(h HistoryEntry) bool { return h.ConnID == connID })
	w.drafts = deleteWhere(w.drafts, func(d Draft) bool { return d.ConnID == connID })
	_ = w.writeLocked("queries.json", w.queries)
	_ = w.writeLocked("history.json", w.history)
	_ = w.writeLocked("drafts.json", w.drafts)
}

// ---- history ---------------------------------------------------------------------------

// historyDelay batches history writes: a script of many statements writes once.
var historyDelay = 2 * time.Second

// AddHistory records executed statements.
func (w *Workspace) AddHistory(entries ...HistoryEntry) {
	if len(entries) == 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, e := range entries {
		if e.Time == "" {
			e.Time = time.Now().UTC().Format(time.RFC3339Nano)
		}
		w.history = append(w.history, e)
	}
	if over := len(w.history) - MaxHistory; over > 0 {
		w.history = append([]HistoryEntry(nil), w.history[over:]...)
	}
	w.historyDirty = true
	if w.historyTimer == nil {
		w.historyTimer = time.AfterFunc(historyDelay, func() { _ = w.Flush() })
	}
}

// History returns the log, newest first, optionally only one connection's.
func (w *Workspace) History(connID string) []HistoryEntry {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]HistoryEntry, 0, len(w.history))
	for i := len(w.history) - 1; i >= 0; i-- {
		if connID == "" || w.history[i].ConnID == connID {
			out = append(out, w.history[i])
		}
	}
	return out
}

// ClearHistory empties the log.
func (w *Workspace) ClearHistory() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.history = nil
	w.historyDirty = false
	return w.writeLocked("history.json", []HistoryEntry{})
}

// Flush writes pending history.
func (w *Workspace) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.historyTimer != nil {
		w.historyTimer.Stop()
		w.historyTimer = nil
	}
	if !w.historyDirty {
		return nil
	}
	w.historyDirty = false
	return w.writeLocked("history.json", w.history)
}

// ---- drafts -----------------------------------------------------------------------------

// Drafts returns the open query tabs saved by SetDrafts.
func (w *Workspace) Drafts() []Draft {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Draft{}, w.drafts...)
}

// SetDrafts replaces the saved open query tabs.
func (w *Workspace) SetDrafts(ds []Draft) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if ds == nil {
		ds = []Draft{}
	}
	w.drafts = ds
	return w.writeLocked("drafts.json", ds)
}

// Problem explains why saved queries and history couldn't be read ("" when they could).
func (w *Workspace) Problem() string {
	if w.err == nil {
		return ""
	}
	return w.err.Error()
}
