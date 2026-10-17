// Package services exposes the core to the frontend as Wails-bound services.
// Each method is a thin wrapper: all behaviour lives in internal/core.
package services

import (
	"context"
	"encoding/base64"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/connurl"
	"chloe/internal/core"
	"chloe/internal/design"
	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/safefile"
	"chloe/internal/store"
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

// Locked explains why saved connections couldn't be decrypted ("" when they were).
func (s *ConnectionService) Locked() string { return s.M.Store.Locked() }

// Get returns a profile for editing. Saved secrets are not sent to the UI; a
// non-empty placeholder tells the form a password is stored.
func (s *ConnectionService) Get(id string) (model.Connection, error) {
	c, err := s.M.Store.Get(id)
	if err != nil {
		return c, err
	}
	for _, sec := range c.Secrets() {
		if *sec.Value != "" {
			*sec.Value = SecretPlaceholder
		}
	}
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
	have := stored.Secrets()
	for i, sec := range c.Secrets() {
		if *sec.Value == SecretPlaceholder {
			*sec.Value = *have[i].Value
		}
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

// Move puts a connection in a group ("" = top level) before another connection ("" = last).
func (s *ConnectionService) Move(id, group, before string) error {
	return s.M.Store.Move(id, group, before)
}

// MoveGroup puts a group before another group ("" = last).
func (s *ConnectionService) MoveGroup(name, before string) error {
	return s.M.Store.MoveGroup(name, before)
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

// Export writes the selected connections (all when ids is empty) to path. With a
// passphrase, saved passwords are included, encrypted with it.
func (s *ConnectionService) Export(ids []string, path, passphrase string) error {
	data, err := s.M.Store.Export(ids, passphrase)
	if err != nil {
		return err
	}
	return safefile.Write(path, data)
}

// InspectExport reads an export file's header: how many connections, and
// whether its passwords need a passphrase.
func (s *ConnectionService) InspectExport(path string) (store.ExportSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return store.ExportSummary{}, err
	}
	return store.Inspect(data)
}

// Import adds the connections in an export file. Returns the number imported.
func (s *ConnectionService) Import(path, passphrase string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return s.M.Store.Import(data, passphrase)
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

// SampleColumn returns up to limit non-null values of a column.
func (s *ExplorerService) SampleColumn(ctx context.Context, connID string, ref model.ObjectRef, column string, limit int) ([]string, error) {
	return s.M.SampleColumn(ctx, connID, ref, column, limit)
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

func (s *QueryService) FetchRows(resultID string, offset, limit, maxChars int) (model.RowPage, error) {
	return s.M.FetchRows(resultID, offset, limit, maxChars)
}

func (s *QueryService) ResultStatus(resultID string) (model.StatementResult, error) {
	return s.M.ResultStatus(resultID)
}

func (s *QueryService) ReleaseResult(resultID string) { s.M.ReleaseResult(resultID) }

// ResultEditTarget says whether a query result can be edited in place, and how.
func (s *QueryService) ResultEditTarget(ctx context.Context, resultID string) (core.ResultEdit, error) {
	return s.M.ResultEditTarget(ctx, resultID)
}

// PatchResult updates a buffered result after its edits were applied.
func (s *QueryService) PatchResult(resultID string, p core.ResultPatch) (model.StatementResult, error) {
	return s.M.PatchResult(resultID, p)
}

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

func (s *DesignService) DatabaseMeta(ctx context.Context, connID string) (design.DatabaseMeta, error) {
	return s.M.DatabaseMeta(ctx, connID)
}

func (s *DesignService) LoadDatabase(ctx context.Context, connID, name string) (design.Database, error) {
	return s.M.LoadDatabase(ctx, connID, name)
}

// DatabaseSQL previews New Database (old nil) or Edit Database.
func (s *DesignService) DatabaseSQL(connID string, old *design.Database, d design.Database) ([]string, error) {
	return s.M.DatabaseSQL(connID, old, d)
}

func (s *DesignService) ApplyDatabase(ctx context.Context, connID string, old *design.Database, d design.Database) core.ApplyResult {
	return s.M.ApplyDatabase(ctx, connID, old, d)
}

func (s *DesignService) DropDatabase(ctx context.Context, connID, name string) core.ApplyResult {
	return s.M.DropDatabase(ctx, connID, name)
}

func (s *DesignService) LoadView(ctx context.Context, connID string, ref model.ObjectRef) (design.View, error) {
	return s.M.LoadView(ctx, connID, ref)
}

// PreviewView returns the SQL a view save would run. old is nil for a new view.
func (s *DesignService) PreviewView(connID string, old *design.View, v design.View) (core.DesignPreview, error) {
	return s.M.PreviewView(connID, old, v)
}

func (s *DesignService) ApplyView(ctx context.Context, connID string, old *design.View, v design.View) (core.ApplyResult, error) {
	return s.M.ApplyView(ctx, connID, old, v)
}

func (s *DesignService) RefreshMaterializedView(ctx context.Context, connID string, ref model.ObjectRef, concurrently bool) core.ApplyResult {
	return s.M.RefreshMaterializedView(ctx, connID, ref, concurrently)
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

// DataTransfer copies tables and views between databases, schemas or connections.
func (s *TransferService) DataTransfer(req core.DataTransferRequest) (core.Job, error) {
	return s.M.DataTransfer(req)
}

// CompareStructure diffs two databases/schemas of the same engine (Structure Synchronization).
func (s *TransferService) CompareStructure(ctx context.Context, req core.StructureCompareRequest) (core.StructureComparison, error) {
	return s.M.CompareStructure(ctx, req)
}

// StructureScript orders the chosen comparison items into a deployment script.
func (s *TransferService) StructureScript(items []core.StructItem) []string {
	return core.StructureScript(items)
}

// Deploy runs a deployment script on the target as a job.
func (s *TransferService) Deploy(req core.DeployRequest) (core.Job, error) { return s.M.Deploy(req) }

// DataSyncTables lists source tables with their default target table and key (Data Synchronization).
func (s *TransferService) DataSyncTables(ctx context.Context, source, target core.Endpoint) ([]core.DataSyncTable, error) {
	return s.M.DataSyncTables(ctx, source, target)
}

// CompareData compares rows by key; the result is kept until DiscardDataSync.
func (s *TransferService) CompareData(ctx context.Context, req core.DataSyncRequest) (core.DataSyncComparison, error) {
	return s.M.CompareData(ctx, req)
}

// DataSyncRecords pages a table's differing records (op "" for every difference).
func (s *TransferService) DataSyncRecords(id, table, op string, offset, limit int) (core.DataSyncPage, error) {
	return s.M.DataSyncRecords(id, table, op, offset, limit)
}

// DataSyncScript builds the deployment script for the chosen actions.
func (s *TransferService) DataSyncScript(id string, tables []core.DataSyncSelection) ([]string, error) {
	return s.M.DataSyncScript(id, tables)
}

// DeployDataSync runs the chosen actions as a job.
func (s *TransferService) DeployDataSync(req core.DataSyncDeployRequest) (core.Job, error) {
	return s.M.DeployDataSync(req)
}

func (s *TransferService) DiscardDataSync(id string) { s.M.DiscardDataSync(id) }

// DefaultTransferOptions are the Data Transfer options a new transfer starts with.
func (s *TransferService) DefaultTransferOptions() core.TransferOptions {
	return core.DefaultTransferOptions()
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

// LayoutService stores diagram layouts and other per-view UI state.
type LayoutService struct{ M *core.Manager }

func (s *LayoutService) Get(key string) (string, error) { return s.M.Store.Layouts().Get(key) }
func (s *LayoutService) Set(key, doc string) error      { return s.M.Store.Layouts().Set(key, doc) }
func (s *LayoutService) Delete(key string) error        { return s.M.Store.Layouts().Delete(key) }

// WriteFile saves text, or base64-encoded binary content (PNG exports), to path.
func (s *TransferService) WriteFile(path, content string, isBase64 bool) error {
	data := []byte(content)
	if isBase64 {
		b, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return err
		}
		data = b
	}
	return safefile.Write(path, data)
}
