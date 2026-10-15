package services

import (
	"context"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/mcpserver"
	"chloe/internal/store"
)

// MCP events emitted to the frontend.
const (
	EventMCPApproval     = "mcp:approval"      // an agent's write waits for the user
	EventMCPApprovalDone = "mcp:approval-done" // a request was answered elsewhere or timed out
	EventMCPActivity     = "mcp:activity"
	EventMCPStatus       = "mcp:status"
	EventMCPSettings     = "mcp:settings"
	EventMCPConnection   = "mcp:connection-added"
	EventMCPQuerySaved   = "mcp:query-saved"
	EventMCPOpenQuery    = "mcp:open-query"
)

func init() {
	application.RegisterEvent[mcpserver.ApprovalRequest](EventMCPApproval)
	application.RegisterEvent[string](EventMCPApprovalDone)
	application.RegisterEvent[mcpserver.Activity](EventMCPActivity)
	application.RegisterEvent[mcpserver.Status](EventMCPStatus)
	application.RegisterEvent[store.MCPSettings](EventMCPSettings)
	application.RegisterEvent[string](EventMCPConnection)
	application.RegisterEvent[mcpserver.QueryTab](EventMCPQuerySaved)
	application.RegisterEvent[mcpserver.QueryTab](EventMCPOpenQuery)
}

// MCPApprover shows approval requests in the app and waits for the answer.
type MCPApprover struct {
	mu      sync.Mutex
	pending map[string]pendingApproval
}

type pendingApproval struct {
	req mcpserver.ApprovalRequest
	ch  chan mcpserver.Answer
}

func (a *MCPApprover) Approve(ctx context.Context, req mcpserver.ApprovalRequest) mcpserver.Answer {
	ch := make(chan mcpserver.Answer, 1)
	a.mu.Lock()
	if a.pending == nil {
		a.pending = map[string]pendingApproval{}
	}
	a.pending[req.ID] = pendingApproval{req, ch}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, req.ID)
		a.mu.Unlock()
	}()
	app := application.Get()
	if app == nil {
		return mcpserver.Answer{}
	}
	app.Event.Emit(EventMCPApproval, req)
	focusMain(app)
	select {
	case ans := <-ch:
		return ans
	case <-ctx.Done():
		app.Event.Emit(EventMCPApprovalDone, req.ID)
		return mcpserver.Answer{}
	}
}

func (a *MCPApprover) resolve(id string, ans mcpserver.Answer) {
	a.mu.Lock()
	p, found := a.pending[id]
	a.mu.Unlock()
	if found {
		select {
		case p.ch <- ans:
		default:
		}
	}
}

func (a *MCPApprover) list() []mcpserver.ApprovalRequest {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []mcpserver.ApprovalRequest{}
	for _, p := range a.pending {
		out = append(out, p.req)
	}
	return out
}

// MCPEvents forwards server activity to the frontend.
type MCPEvents struct{}

func (MCPEvents) Activity(a mcpserver.Activity) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventMCPActivity, a)
	}
}

func (MCPEvents) Settings(st store.MCPSettings) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventMCPSettings, st)
	}
}

func (MCPEvents) ConnectionAdded(id string) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventMCPConnection, id)
	}
}

func (MCPEvents) QuerySaved(connID, database, schema string) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventMCPQuerySaved, mcpserver.QueryTab{ConnID: connID, Database: database, Schema: schema})
	}
}

func (MCPEvents) OpenQueryTab(tab mcpserver.QueryTab) {
	app := application.Get()
	if app == nil {
		return
	}
	app.Event.Emit(EventMCPOpenQuery, tab)
	focusMain(app)
}

func (MCPEvents) Status(s mcpserver.Status) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventMCPStatus, s)
	}
}

// MCPService backs the MCP Server window.
type MCPService struct {
	S        *mcpserver.Server
	Approver *MCPApprover
}

func (s *MCPService) Settings() store.MCPSettings { return s.S.Settings() }

// Save stores the settings and returns them with new endpoints' IDs and tokens filled in.
func (s *MCPService) Save(st store.MCPSettings) (store.MCPSettings, error) {
	err := s.S.Save(st)
	return s.S.Settings(), err
}

func (s *MCPService) NewToken() string         { return mcpserver.NewToken() }
func (s *MCPService) Status() mcpserver.Status { return s.S.Status() }

func (s *MCPService) Activity() []mcpserver.Activity { return s.S.Activity() }
func (s *MCPService) ClearActivity()                 { s.S.ClearActivity() }

// Pending lists writes waiting for approval (shown again after a reload).
func (s *MCPService) Pending() []mcpserver.ApprovalRequest { return s.Approver.list() }

// Resolve answers an approval request. password is what the user typed for a
// new connection ("" keeps the agent's).
func (s *MCPService) Resolve(id string, approved bool, password string) {
	s.Approver.resolve(id, mcpserver.Answer{OK: approved, Password: password})
}

// focusMain brings Chloe forward so what an agent asked for isn't missed behind its window.
func focusMain(app *application.App) {
	for _, w := range app.Window.GetAll() {
		w.UnMinimise()
		w.Show()
		w.Focus()
		break
	}
}
