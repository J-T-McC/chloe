// Package mcpserver serves Chloe's connections to AI assistants over the Model
// Context Protocol (Streamable HTTP on loopback). Each endpoint has its own bearer
// token and access rules; writes wait for the user's approval unless the
// endpoint is in danger mode. See docs/specs/mcp-server.md.
package mcpserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/core"
	"chloe/internal/store"
)

// ApprovalRequest is what the user is asked to approve.
type ApprovalRequest struct {
	ID string `json:"id"`
	// Kind is "write" (rows) or "connection" (a new saved connection).
	Kind       string `json:"kind"`
	Endpoint   string `json:"endpoint"`
	Tool       string `json:"tool"`
	Connection string `json:"connection"`
	Database   string `json:"database,omitempty"`
	Table      string `json:"table,omitempty"`
	// Summary is one line, e.g. "Update 3 rows in orders".
	Summary string `json:"summary"`
	SQL     string `json:"sql"` // the statement, or a new connection's settings
	Rows    int64  `json:"rows"`
	// AskPassword lets the user type the connection's password in the dialog;
	// HasPassword says the agent sent one (used when the user leaves it empty).
	AskPassword bool   `json:"askPassword,omitempty"`
	HasPassword bool   `json:"hasPassword,omitempty"`
	Warning     string `json:"warning,omitempty"`
}

// Answer is the user's reply to an approval request.
type Answer struct {
	OK       bool
	Password string
}

// Approver asks the user. It answers not OK when they decline or ctx ends.
type Approver interface {
	Approve(ctx context.Context, req ApprovalRequest) Answer
}

// Activity is one tool call in the activity log.
type Activity struct {
	ID         string `json:"id"`
	Time       string `json:"time"`
	Endpoint   string `json:"endpoint"`
	Tool       string `json:"tool"`
	Connection string `json:"connection,omitempty"`
	Database   string `json:"database,omitempty"`
	SQL        string `json:"sql,omitempty"`
	Rows       int64  `json:"rows"`
	// Outcome is ok, error, approved, denied, timeout (no answer) or unasked (danger mode).
	Outcome    string `json:"outcome"`
	Error      string `json:"error,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

// Status describes the HTTP listener.
type Status struct {
	Running bool   `json:"running"`
	URL     string `json:"url"`
	Error   string `json:"error,omitempty"`
	// Danger is set while the server runs with an enabled danger-mode endpoint.
	Danger bool `json:"danger"`
	// HTTP is set while the HTTP listener runs (URL is where).
	HTTP bool `json:"http"`
	// Socket is the stdio bridge's socket; Bridge is the command clients launch.
	Socket string `json:"socket,omitempty"`
	Bridge string `json:"bridge,omitempty"`
}

// Events notifies the UI.
type Events interface {
	Activity(a Activity)
	Status(s Status)
	// Settings reports settings changed outside the MCP window (a group rename).
	Settings(st store.MCPSettings)
	// ConnectionAdded reports a connection an agent created.
	ConnectionAdded(id string)
	// QuerySaved reports a query an agent saved into a database's Queries folder.
	QuerySaved(connID, database, schema string)
	// OpenQueryTab asks the UI to open a query tab for the user.
	OpenQueryTab(tab QueryTab)
	// ModelChanged reports that an agent edited a model file.
	ModelChanged(path string)
	// OpenModel asks the UI to open a model file.
	OpenModel(path string)
	// BIChanged reports that an agent changed a BI workspace.
	BIChanged(path string)
	// OpenBI asks the UI to open an object (a chart) of a BI workspace.
	OpenBI(path, id string)
}

// ApprovalTimeout is how long a write waits for the user.
var ApprovalTimeout = 2 * time.Minute

const maxActivity = 1000

type Server struct {
	m        *core.Manager
	approver Approver
	events   Events

	mu       sync.Mutex
	settings store.MCPSettings
	servers  map[string]*mcp.Server // by endpoint ID
	http     *http.Server
	status   Status

	sock     *socketServer
	sockPath string

	actMu    sync.Mutex
	activity []Activity // oldest first
}

func New(m *core.Manager, approver Approver, events Events) *Server {
	s := &Server{m: m, approver: approver, events: events, servers: map[string]*mcp.Server{}, sockPath: filepath.Join(m.Store.Dir(), "mcp.sock")}
	// Renaming or deleting a group rewrites the endpoints' group access.
	m.Store.OnMCPChange(func(st store.MCPSettings) {
		_ = s.apply(st)
		if events != nil {
			events.Settings(st)
		}
	})
	return s
}

// NewToken makes an endpoint token.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return "chloe_" + base64.RawURLEncoding.EncodeToString(b)
}

// Load reads the saved settings and starts the server if it is switched on.
func (s *Server) Load() error {
	st, err := s.m.Store.MCP()
	if err != nil {
		return err
	}
	return s.apply(st)
}

// Settings returns the current settings.
func (s *Server) Settings() store.MCPSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// Save stores new settings and starts, stops or moves the listener to match.
func (s *Server) Save(st store.MCPSettings) error {
	for i := range st.Endpoints {
		e := &st.Endpoints[i]
		if e.ID == "" {
			e.ID = uuid.NewString()
		}
		if e.Token == "" {
			e.Token = NewToken()
		}
		e.Name = strings.TrimSpace(e.Name)
		if e.Name == "" {
			return errors.New("every endpoint needs a name")
		}
	}
	if st.Port <= 0 || st.Port > 65535 {
		st.Port = store.DefaultMCPPort
	}
	if err := s.m.Store.SetMCP(st); err != nil {
		return err
	}
	return s.apply(st)
}

func (s *Server) apply(st store.MCPSettings) error {
	s.mu.Lock()
	wantHTTP := st.Enabled && !st.NoHTTP
	restart := s.http != nil && (!wantHTTP || st.Port != s.settings.Port)
	startHTTP := wantHTTP && (s.http == nil || restart)
	startSock := st.Enabled && s.sock == nil
	var stopSock *socketServer
	if !st.Enabled {
		stopSock, s.sock = s.sock, nil
	}
	s.settings = st
	// Servers are rebuilt so a changed endpoint never keeps a stale tool list.
	s.servers = map[string]*mcp.Server{}
	old := s.http
	if restart {
		s.http = nil
	}
	sock := s.sock
	s.mu.Unlock()
	if restart {
		shutdown(old)
	}
	if stopSock != nil {
		stopSock.close()
	}
	if sock != nil {
		// Bridged sessions of deleted or disabled endpoints end now.
		sock.drop(func(id string) bool { _, err := s.endpoint(id); return err == nil })
	}
	var errs []error
	if startHTTP {
		errs = append(errs, s.listen(st.Port))
	}
	if startSock {
		ss, err := s.listenSocket(s.sockPath)
		errs = append(errs, err)
		if err == nil {
			s.mu.Lock()
			s.sock = ss
			s.mu.Unlock()
		}
	}
	err := errors.Join(errs...)
	s.publishStatus(err)
	return err
}

func shutdown(h *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if h.Shutdown(ctx) != nil {
		h.Close()
	}
}

// Close stops the listeners.
func (s *Server) Close() {
	s.mu.Lock()
	h, ss := s.http, s.sock
	s.http, s.sock = nil, nil
	s.mu.Unlock()
	if h != nil {
		h.Close()
	}
	if ss != nil {
		ss.close()
	}
}

func (s *Server) listen(port int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("couldn't listen on %s: %w", addr, err)
	}
	h := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	s.http = h
	s.mu.Unlock()
	go func() { _ = h.Serve(ln) }()
	return nil
}

func (s *Server) publishStatus(err error) {
	exe, _ := os.Executable()
	s.mu.Lock()
	st := Status{URL: fmt.Sprintf("http://127.0.0.1:%d/mcp", s.settings.Port), HTTP: s.http != nil, Running: s.http != nil || s.sock != nil}
	if s.sock != nil {
		st.Socket, st.Bridge = s.sock.path, exe
	}
	if err != nil {
		st.Error = err.Error()
	}
	for _, e := range s.settings.Endpoints {
		if st.Running && e.Enabled && e.Danger {
			st.Danger = true
		}
	}
	s.status = st
	s.mu.Unlock()
	if s.events != nil {
		s.events.Status(st)
	}
}

// Status reports whether the listener is running.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

type ctxKey struct{}

// Handler is the HTTP handler: token check, browser protection, then MCP.
func (s *Server) Handler() http.Handler {
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		id, _ := r.Context().Value(ctxKey{}).(string)
		return s.serverFor(id)
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	protect := http.NewCrossOriginProtection()
	mux := http.NewServeMux()
	mux.Handle("/mcp", protect.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Browsers send Origin; MCP clients don't. A page must never reach the server.
		if o := r.Header.Get("Origin"); o != "" && !localOrigin(o) {
			http.Error(w, "cross-origin requests are not allowed", http.StatusForbidden)
			return
		}
		e, ok := s.endpointForToken(bearer(r))
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="chloe"`)
			http.Error(w, "unknown or disabled endpoint token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, e.ID)))
	})))
	return mux
}

func localOrigin(o string) bool {
	for _, p := range []string{"http://127.0.0.1:", "http://localhost:", "http://[::1]:"} {
		if strings.HasPrefix(o, p) {
			return true
		}
	}
	return false
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) endpointForToken(tok string) (store.MCPEndpoint, bool) {
	if tok == "" {
		return store.MCPEndpoint{}, false
	}
	want := sha256.Sum256([]byte(tok))
	s.mu.Lock()
	defer s.mu.Unlock()
	var found store.MCPEndpoint
	ok := false
	for _, e := range s.settings.Endpoints {
		have := sha256.Sum256([]byte(e.Token))
		if subtle.ConstantTimeCompare(want[:], have[:]) == 1 && e.Enabled {
			found, ok = e, true
		}
	}
	return found, ok
}

// endpoint returns an endpoint's current settings; a deleted or disabled
// endpoint stops working at once, even mid-session.
func (s *Server) endpoint(id string) (store.MCPEndpoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.settings.Endpoints {
		if e.ID == id && e.Enabled {
			return e, nil
		}
	}
	return store.MCPEndpoint{}, errors.New("this endpoint has been disabled or deleted in Chloe")
}

func (s *Server) serverFor(id string) *mcp.Server {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[id]; ok {
		return srv
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "chloe", Title: "Chloe", Version: "1"}, &mcp.ServerOptions{
		Instructions: "Chloe is a database client. These tools reach the connections the user has shared with this endpoint. " +
			"Start with list_connections. Writes may wait for the user to approve them in Chloe.",
	})
	var ep store.MCPEndpoint
	for _, e := range s.settings.Endpoints {
		if e.ID == id {
			ep = e
		}
	}
	writes := canWrite(ep)
	s.addTools(srv, id, writes)
	if ep.Capabilities.CreateConnections {
		s.addConnectionTools(srv, id)
	}
	if ep.Capabilities.SavedQueries {
		s.addQueryTools(srv, id)
	}
	if ep.Capabilities.Monitor {
		s.addMonitorTools(srv, id, writes)
	}
	if ep.Capabilities.Models {
		s.addModelTools(srv, id)
	}
	if ep.Capabilities.Reports {
		s.addBITools(srv, id)
		s.addDashboardTools(srv, id)
	}
	s.servers[id] = srv
	return srv
}

// ServerForTest builds an endpoint's MCP server without the HTTP layer.
func (s *Server) ServerForTest(endpointID string) *mcp.Server { return s.serverFor(endpointID) }

func (s *Server) record(a Activity) {
	s.actMu.Lock()
	s.activity = append(s.activity, a)
	if len(s.activity) > maxActivity {
		s.activity = s.activity[len(s.activity)-maxActivity:]
	}
	s.actMu.Unlock()
	if s.events != nil {
		s.events.Activity(a)
	}
}

// Activity returns the recorded tool calls, newest first.
func (s *Server) Activity() []Activity {
	s.actMu.Lock()
	defer s.actMu.Unlock()
	out := make([]Activity, len(s.activity))
	for i, a := range s.activity {
		out[len(out)-1-i] = a
	}
	return out
}

// ClearActivity empties the activity log.
func (s *Server) ClearActivity() {
	s.actMu.Lock()
	s.activity = nil
	s.actMu.Unlock()
}
