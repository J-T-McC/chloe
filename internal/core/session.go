package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// session is one dedicated server connection, typically owned by a query editor tab.
type session struct {
	id       string
	connID   string
	database string
	schema   string

	run     sync.Mutex // serialises statement execution
	ds      driver.Session
	filling sync.WaitGroup // background fills still reading from ds
	results []string       // result IDs produced by the last Execute
}

// OpenSession reserves a server connection for an editor tab.
func (m *Manager) OpenSession(ctx context.Context, connID, database, schema string) (string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return "", err
	}
	ds, err := c.NewSession(ctx, database, schema)
	if err != nil {
		return "", err
	}
	s := &session{id: newID(), connID: connID, database: database, schema: schema, ds: ds}
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
	return s.id, nil
}

func (m *Manager) session(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %s is closed", id)
	}
	return s, nil
}

func (m *Manager) CloseSession(id string) {
	m.mu.Lock()
	s, ok := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if ok {
		m.closeSession(s)
	}
}

func (m *Manager) closeSession(s *session) {
	m.releaseResults(s)
	s.filling.Wait()
	s.ds.Close()
}

// UseDatabase switches the session's default database/schema. For engines where a
// connection is bound to one database (PostgreSQL), the session is transparently reopened.
func (m *Manager) UseDatabase(ctx context.Context, sessionID, database, schema string) error {
	s, err := m.session(sessionID)
	if err != nil {
		return err
	}
	s.run.Lock()
	defer s.run.Unlock()
	c, err := m.conn(s.connID)
	if err != nil {
		return err
	}
	if c.Dialect().HasSchemas && database != s.database {
		ds, err := c.NewSession(ctx, database, schema)
		if err != nil {
			return err
		}
		m.releaseResults(s)
		s.filling.Wait()
		s.ds.Close()
		s.ds = ds
	} else if err := s.ds.Use(ctx, database, schema); err != nil {
		return err
	}
	s.database, s.schema = database, schema
	return nil
}

// releaseResults frees the result sets from a session's previous run.
func (m *Manager) releaseResults(s *session) {
	m.mu.Lock()
	ids := s.results
	s.results = nil
	var sets []*resultSet
	for _, id := range ids {
		if rs, ok := m.results[id]; ok {
			sets = append(sets, rs)
			delete(m.results, id)
		}
	}
	m.mu.Unlock()
	for _, rs := range sets {
		rs.close()
	}
}

// ExecOptions control script execution.
type ExecOptions struct {
	ContinueOnError bool `json:"continueOnError"`
}

// Execute runs a script statement by statement on a session. Rows of the final
// statement keep streaming in the background after Execute returns; earlier
// statements are read fully because the connection must be free for the next one.
func (m *Manager) Execute(ctx context.Context, sessionID, script string, opts ExecOptions) ([]model.StatementResult, error) {
	s, err := m.session(sessionID)
	if err != nil {
		return nil, err
	}
	c, err := m.conn(s.connID)
	if err != nil {
		return nil, err
	}
	stmts := sqlsplit.Split(script, c.Dialect().Split)
	if len(stmts) == 0 {
		return nil, errors.New("nothing to execute")
	}

	s.run.Lock()
	defer s.run.Unlock()
	m.releaseResults(s)
	s.filling.Wait()

	var out []model.StatementResult
	for i, st := range stmts {
		last := i == len(stmts)-1
		res := m.runStatement(ctx, s, st, last)
		out = append(out, res)
		if res.Error != "" && !opts.ContinueOnError {
			break
		}
	}
	m.recordHistory(s, out)
	return out, nil
}

func (m *Manager) runStatement(ctx context.Context, s *session, st sqlsplit.Statement, last bool) model.StatementResult {
	res := model.StatementResult{SQL: st.SQL}
	start := time.Now()
	rows, err := s.ds.Query(ctx, st.SQL)
	if err != nil {
		res.Error = err.Error()
		res.ErrorLine = st.StartLine
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}
	if !rows.HasRows() {
		res.RowsAffected = rows.RowsAffected()
		res.Complete = true
		rows.Close()
		res.DurationMs = time.Since(start).Milliseconds()
		return res
	}

	id := newID()
	ds := s.ds
	rs := newResultSet(id, rows.Columns(), func() { _ = ds.Cancel(context.Background()) })
	rs.origin = resultOrigin{connID: s.connID, sql: st.SQL, database: s.database, schema: s.schema}
	m.mu.Lock()
	m.results[id] = rs
	s.results = append(s.results, id)
	m.mu.Unlock()

	s.filling.Add(1)
	go func() {
		defer s.filling.Done()
		rs.fill(rows, m.settings.MaxResultRows, func(n int, done bool) { m.events.ResultProgress(id, n, done) })
	}()
	if last {
		rs.waitFor(m.settings.FirstPageRows)
	} else {
		s.filling.Wait()
	}
	n, complete, truncated, ferr := rs.snapshot()
	res.ResultID = id
	res.Columns = rs.columns
	res.RowCount, res.Complete, res.Truncated = n, complete, truncated
	if ferr != nil {
		res.Error = ferr.Error()
		res.ErrorLine = st.StartLine
	}
	res.DurationMs = time.Since(start).Milliseconds()
	return res
}

// Cancel aborts whatever is running on a session.
func (m *Manager) Cancel(ctx context.Context, sessionID string) error {
	s, err := m.session(sessionID)
	if err != nil {
		return err
	}
	return s.ds.Cancel(ctx)
}

// FetchRows returns a page of a buffered result, waiting for rows still streaming in.
// maxChars > 0 cuts longer values (the grid shows the start of each); 0 returns them whole.
func (m *Manager) FetchRows(resultID string, offset, limit, maxChars int) (model.RowPage, error) {
	m.mu.Lock()
	rs, ok := m.results[resultID]
	m.mu.Unlock()
	if !ok {
		return model.RowPage{}, fmt.Errorf("result %s has been released", resultID)
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	return rs.page(offset, limit, maxChars), nil
}

// ResultStatus reports the buffered row count of a result.
func (m *Manager) ResultStatus(resultID string) (model.StatementResult, error) {
	m.mu.Lock()
	rs, ok := m.results[resultID]
	m.mu.Unlock()
	if !ok {
		return model.StatementResult{}, fmt.Errorf("result %s has been released", resultID)
	}
	n, complete, truncated, err := rs.snapshot()
	r := model.StatementResult{ResultID: resultID, Columns: rs.columns, RowCount: n, Complete: complete, Truncated: truncated}
	if err != nil {
		r.Error = err.Error()
	}
	return r, nil
}

// ReleaseResult frees a single result set.
func (m *Manager) ReleaseResult(resultID string) {
	m.mu.Lock()
	rs, ok := m.results[resultID]
	delete(m.results, resultID)
	m.mu.Unlock()
	if ok {
		rs.close()
	}
}
