// Package services exposes the core to the frontend as Wails-bound services.
// Each method is a thin wrapper: all behaviour lives in internal/core.
package services

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/core"
	"chloe/internal/driver"
	"chloe/internal/model"
)

// Event names emitted to the frontend.
const (
	EventResultProgress   = "result:progress"
	EventConnectionClosed = "connection:closed"
)

// ResultProgress is the payload of EventResultProgress.
type ResultProgress struct {
	ResultID string `json:"resultId"`
	Rows     int    `json:"rows"`
	Complete bool   `json:"complete"`
}

func init() {
	application.RegisterEvent[ResultProgress](EventResultProgress)
	application.RegisterEvent[string](EventConnectionClosed)
}

// Emitter forwards core events to the frontend.
type Emitter struct{}

func (Emitter) ResultProgress(id string, rows int, complete bool) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventResultProgress, ResultProgress{ResultID: id, Rows: rows, Complete: complete})
	}
}

func (Emitter) ConnectionClosed(id string) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventConnectionClosed, id)
	}
}

// ConnectionService manages saved connection profiles and their open state.
type ConnectionService struct{ M *core.Manager }

func (s *ConnectionService) List() []model.Connection { return s.M.Store.List() }

// Get returns a profile for editing. Saved secrets are not sent to the UI; a
// non-empty placeholder tells the form a password is stored.
func (s *ConnectionService) Get(id string) (model.Connection, error) {
	c, err := s.M.Store.Get(id)
	if err != nil {
		return c, err
	}
	mask := func(v string) string {
		if v != "" {
			return SecretPlaceholder
		}
		return ""
	}
	c.Password, c.SSH.Password, c.SSH.Passphrase = mask(c.Password), mask(c.SSH.Password), mask(c.SSH.Passphrase)
	return c, nil
}

// SecretPlaceholder stands in for a stored secret the UI hasn't changed.
const SecretPlaceholder = "\u0000saved\u0000"

// resolveSecrets swaps placeholders back for the stored values.
func (s *ConnectionService) resolveSecrets(c model.Connection) model.Connection {
	if c.ID == "" {
		return c
	}
	stored, err := s.M.Store.Get(c.ID)
	if err != nil {
		return c
	}
	if c.Password == SecretPlaceholder {
		c.Password = stored.Password
	}
	if c.SSH.Password == SecretPlaceholder {
		c.SSH.Password = stored.SSH.Password
	}
	if c.SSH.Passphrase == SecretPlaceholder {
		c.SSH.Passphrase = stored.SSH.Passphrase
	}
	return c
}

func (s *ConnectionService) Save(c model.Connection) (model.Connection, error) {
	c = s.resolveSecrets(c)
	// Saving changes how the connection is opened; drop any live one.
	if c.ID != "" {
		s.M.Disconnect(c.ID)
	}
	return s.M.Store.Save(c)
}

func (s *ConnectionService) Delete(id string) error {
	s.M.Disconnect(id)
	return s.M.Store.Delete(id)
}

// Test tries a connection from the form and returns the server version.
func (s *ConnectionService) Test(ctx context.Context, c model.Connection) (string, error) {
	return s.M.TestConnection(ctx, s.resolveSecrets(c))
}

// Connect opens a saved connection. password is used when none is saved.
func (s *ConnectionService) Connect(ctx context.Context, id, password string) (string, error) {
	return s.M.Connect(ctx, id, password)
}

func (s *ConnectionService) Disconnect(id string)          { s.M.Disconnect(id) }
func (s *ConnectionService) IsConnected(id string) bool    { return s.M.IsConnected(id) }
func (s *ConnectionService) DefaultPort(t model.DBType) int { return model.DefaultPort(t) }

// ExplorerService reads database metadata for the object tree, designers and autocomplete.
type ExplorerService struct{ M *core.Manager }

func (s *ExplorerService) Dialect(connID string) (driver.Dialect, error) { return s.M.Dialect(connID) }

func (s *ExplorerService) ListDatabases(ctx context.Context, connID string) ([]string, error) {
	return s.M.ListDatabases(ctx, connID)
}

func (s *ExplorerService) ListSchemas(ctx context.Context, connID, database string) ([]string, error) {
	return s.M.ListSchemas(ctx, connID, database)
}

func (s *ExplorerService) ListObjects(ctx context.Context, connID, database, schema string, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	return s.M.ListObjects(ctx, connID, database, schema, kind)
}

func (s *ExplorerService) TableDetail(ctx context.Context, connID string, ref model.ObjectRef) (model.TableDetail, error) {
	return s.M.TableDetail(ctx, connID, ref)
}

func (s *ExplorerService) Catalog(ctx context.Context, connID, database string) (model.Catalog, error) {
	return s.M.Catalog(ctx, connID, database)
}

func (s *ExplorerService) DDL(ctx context.Context, connID string, ref model.ObjectRef) (string, error) {
	return s.M.DDL(ctx, connID, ref)
}

func (s *ExplorerService) BuiltinFunctions(connID string) ([]string, error) {
	return s.M.BuiltinFunctions(connID)
}

// QueryService runs SQL and serves result pages.
type QueryService struct{ M *core.Manager }

func (s *QueryService) OpenSession(ctx context.Context, connID, database, schema string) (string, error) {
	return s.M.OpenSession(ctx, connID, database, schema)
}

func (s *QueryService) CloseSession(sessionID string) { s.M.CloseSession(sessionID) }

func (s *QueryService) UseDatabase(ctx context.Context, sessionID, database, schema string) error {
	return s.M.UseDatabase(ctx, sessionID, database, schema)
}

// Execute runs a script. It deliberately ignores the call's context: cancelling a
// query goes through Cancel so the server-side statement is stopped too.
func (s *QueryService) Execute(sessionID, script string, opts core.ExecOptions) ([]model.StatementResult, error) {
	return s.M.Execute(context.Background(), sessionID, script, opts)
}

func (s *QueryService) Cancel(ctx context.Context, sessionID string) error {
	return s.M.Cancel(ctx, sessionID)
}

func (s *QueryService) FetchRows(resultID string, offset, limit int) (model.RowPage, error) {
	return s.M.FetchRows(resultID, offset, limit)
}

func (s *QueryService) ResultStatus(resultID string) (model.StatementResult, error) {
	return s.M.ResultStatus(resultID)
}

func (s *QueryService) ReleaseResult(resultID string) { s.M.ReleaseResult(resultID) }

func (s *QueryService) BrowseTable(ctx context.Context, connID string, req core.BrowseRequest) (model.StatementResult, error) {
	return s.M.BrowseTable(ctx, connID, req)
}

func (s *QueryService) CountRows(ctx context.Context, connID string, ref model.ObjectRef, where string) (int64, error) {
	return s.M.CountRows(ctx, connID, ref, where)
}

// ApplyResult carries per-change outcomes even when the batch was rolled back,
// so the grid can point at the offending row.
type ApplyResult struct {
	Results []core.ChangeResult `json:"results"`
	Error   string              `json:"error,omitempty"`
}

func (s *QueryService) ApplyChanges(ctx context.Context, connID string, ref model.ObjectRef, changes []core.RowChange) ApplyResult {
	res, err := s.M.ApplyChanges(ctx, connID, ref, changes)
	out := ApplyResult{Results: res}
	if err != nil {
		out.Error = err.Error()
	}
	return out
}
