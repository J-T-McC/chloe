// Package services exposes the core to the frontend as Wails-bound services.
// Each method is a thin wrapper: all behaviour lives in internal/core.
package services

import (
	"context"
	"fmt"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/connurl"
	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/transfer"
)

// Event names emitted to the frontend.
const (
	EventResultProgress   = "result:progress"
	EventConnectionClosed = "connection:closed"
	EventJobProgress      = "job:progress"
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
	application.RegisterEvent[core.Job](EventJobProgress)
}

func (Emitter) JobProgress(j core.Job) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventJobProgress, j)
	}
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

func (s *ConnectionService) Disconnect(id string)           { s.M.Disconnect(id) }
func (s *ConnectionService) IsConnected(id string) bool     { return s.M.IsConnected(id) }
func (s *ConnectionService) DefaultPort(t model.DBType) int { return model.DefaultPort(t) }

// Duplicate copies a connection, including its saved passwords.
func (s *ConnectionService) Duplicate(id string) (model.Connection, error) {
	return s.M.Store.Duplicate(id)
}

// Info summarises an open connection (server version, sessions, route).
func (s *ConnectionService) Info(id string) (model.ServerInfo, error) { return s.M.Info(id) }

func (s *ConnectionService) Groups() []string           { return s.M.Store.Groups() }
func (s *ConnectionService) AddGroup(name string) error { return s.M.Store.AddGroup(name) }
func (s *ConnectionService) RenameGroup(old, name string) error {
	return s.M.Store.RenameGroup(old, name)
}
func (s *ConnectionService) DeleteGroup(name string) error { return s.M.Store.DeleteGroup(name) }
func (s *ConnectionService) MoveToGroup(id, group string) error {
	return s.M.Store.MoveToGroup(id, group)
}

// Presets lists hosted-service presets (Supabase, Neon, ...).
func (s *ConnectionService) Presets() []connurl.Preset { return connurl.Presets() }

// ParseURL turns a pasted connection string into an unsaved profile.
func (s *ConnectionService) ParseURL(raw string) (model.Connection, error) { return connurl.Parse(raw) }

// URL renders a saved connection as a connection string without its password.
func (s *ConnectionService) URL(id string) (string, error) {
	c, err := s.M.Store.Get(id)
	if err != nil {
		return "", err
	}
	return connurl.Format(c), nil
}

// ExportToFile asks for a destination and writes the selected connections there.
// Returns the chosen path, or "" if the user cancelled.
func (s *ConnectionService) ExportToFile(ids []string, withSecrets bool) (string, error) {
	data, err := s.M.Store.Export(ids, withSecrets)
	if err != nil {
		return "", err
	}
	path, err := application.Get().Dialog.SaveFile().
		SetFilename("connections.chloe.json").
		AddFilter("Chloe connections", "*.json").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return "", err
	}
	return path, os.WriteFile(path, data, 0o600)
}

// ImportFromFile asks for an export file and imports it. Returns the number imported.
func (s *ConnectionService) ImportFromFile() (int, error) {
	path, err := application.Get().Dialog.OpenFile().
		AddFilter("Chloe connections", "*.json").
		PromptForSingleSelection()
	if err != nil || path == "" {
		return 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	n, err := s.M.Store.Import(data)
	if err != nil {
		return n, fmt.Errorf("%s: %w", path, err)
	}
	return n, nil
}

// PickFile opens a file chooser (for key and certificate fields). Returns "" on cancel.
func (s *ConnectionService) PickFile(title string) (string, error) {
	return application.Get().Dialog.OpenFile().SetTitle(title).ShowHiddenFiles(true).PromptForSingleSelection()
}

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

// CloseDatabase ends editor sessions on a database and releases its server connections.
func (s *ExplorerService) CloseDatabase(connID, database string) error {
	return s.M.CloseDatabase(connID, database)
}

// MonitorService backs the server monitor: sessions, variables and status.
type MonitorService struct{ M *core.Manager }

func (s *MonitorService) Processes(ctx context.Context, connID string) ([]model.Process, error) {
	return s.M.Processes(ctx, connID)
}

// KillProcess ends a session, or only its current query when queryOnly is set.
func (s *MonitorService) KillProcess(ctx context.Context, connID, id string, queryOnly bool) error {
	return s.M.KillProcess(ctx, connID, id, queryOnly)
}

func (s *MonitorService) Variables(ctx context.Context, connID string) ([]model.ServerVar, error) {
	return s.M.Variables(ctx, connID)
}

func (s *MonitorService) Status(ctx context.Context, connID string) ([]model.ServerVar, error) {
	return s.M.ServerStatus(ctx, connID)
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

// DesignService backs the table designer and table actions.
type DesignService struct{ M *core.Manager }

func (s *DesignService) Load(ctx context.Context, connID string, ref model.ObjectRef) (design.Table, error) {
	return s.M.LoadDesign(ctx, connID, ref)
}

func (s *DesignService) Meta(ctx context.Context, connID, database string) (design.Meta, error) {
	return s.M.DesignMeta(ctx, connID, database)
}

// Preview returns the SQL a save would run. old is nil for a new table.
func (s *DesignService) Preview(connID string, old *design.Table, table design.Table) (core.DesignPreview, error) {
	return s.M.PreviewDesign(connID, old, table)
}

// Apply saves a design and returns the reloaded table.
func (s *DesignService) Apply(ctx context.Context, connID string, old *design.Table, table design.Table) (core.ApplyResult, error) {
	return s.M.ApplyDesign(ctx, connID, old, table)
}

func (s *DesignService) TableActionSQL(connID string, ref model.ObjectRef, action core.TableAction, newName string) ([]string, error) {
	return s.M.TableActionSQL(connID, ref, action, newName)
}

func (s *DesignService) RunTableAction(ctx context.Context, connID string, ref model.ObjectRef, action core.TableAction, newName string) core.ApplyResult {
	return s.M.RunTableAction(ctx, connID, ref, action, newName)
}

func (s *DesignService) Maintain(ctx context.Context, connID string, refs []model.ObjectRef, op core.MaintainOp) ([]core.MaintainResult, error) {
	return s.M.Maintain(ctx, connID, refs, op)
}

// TransferService backs import/export wizards, dumps and SQL file execution.
type TransferService struct{ M *core.Manager }

func (s *TransferService) Export(connID string, req core.ExportRequest) (core.Job, error) {
	return s.M.Export(connID, req)
}

func (s *TransferService) PreviewImport(connID, path string, opts transfer.ImportOptions) (core.ImportPreview, error) {
	return s.M.PreviewImport(connID, path, opts)
}

func (s *TransferService) Import(ctx context.Context, connID string, req core.ImportRequest) (core.Job, error) {
	return s.M.Import(ctx, connID, req)
}

func (s *TransferService) Dump(connID string, req core.DumpRequest) (core.Job, error) {
	return s.M.Dump(connID, req)
}

func (s *TransferService) ExecuteFile(connID string, req core.ExecuteFileRequest) (core.Job, error) {
	return s.M.ExecuteFile(connID, req)
}

func (s *TransferService) Jobs() []core.Job    { return s.M.Jobs() }
func (s *TransferService) CancelJob(id string) { s.M.CancelJob(id) }
func (s *TransferService) ClearJobs()          { s.M.ClearJobs() }
func (s *TransferService) Encodings() []string { return transfer.Encodings() }

// SaveFileDialog asks where to save; returns "" on cancel.
func (s *TransferService) SaveFileDialog(defaultName, filterName, pattern string) (string, error) {
	d := application.Get().Dialog.SaveFile().SetFilename(defaultName).CanCreateDirectories(true)
	if pattern != "" {
		d = d.AddFilter(filterName, pattern)
	}
	return d.PromptForSingleSelection()
}

// OpenFileDialog asks for a file to read; returns "" on cancel.
func (s *TransferService) OpenFileDialog(title, filterName, pattern string) (string, error) {
	d := application.Get().Dialog.OpenFile().SetTitle(title)
	if pattern != "" {
		d = d.AddFilter(filterName, pattern)
	}
	return d.PromptForSingleSelection()
}

// ChooseFolder asks for a directory (exporting several tables at once).
func (s *TransferService) ChooseFolder(title string) (string, error) {
	return application.Get().Dialog.OpenFile().SetTitle(title).CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true).PromptForSingleSelection()
}
