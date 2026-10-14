// Package core coordinates open connections, editor sessions and result sets.
// It has no dependency on the UI framework so it can be tested and reused directly.
package core

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/store"
	"chloe/internal/tunnel"
)

// Events lets the manager notify the UI about background progress.
type Events interface {
	ResultProgress(resultID string, rows int, complete bool)
	ConnectionClosed(connID string)
	JobProgress(job Job)
}

type nopEvents struct{}

func (nopEvents) ResultProgress(string, int, bool) {}
func (nopEvents) ConnectionClosed(string)          {}
func (nopEvents) JobProgress(Job)                  {}

// Settings that bound memory use.
type Settings struct {
	MaxResultRows    int // rows buffered per result set
	FirstPageRows    int // rows to wait for before returning from Execute
	StatementTimeout time.Duration
}

var DefaultSettings = Settings{MaxResultRows: 1_000_000, FirstPageRows: 200}

type Manager struct {
	Store    *store.Store
	events   Events
	settings Settings

	mu       sync.Mutex
	conns    map[string]*openConn  // by connection profile ID
	sessions map[string]*session   // by session ID
	results  map[string]*resultSet // by result ID

	jobsMu sync.Mutex
	jobs   map[string]*jobState
}

type openConn struct {
	profile model.Connection
	dialer  tunnel.Dialer
	conn    driver.Conn
}

func NewManager(st *store.Store, ev Events, s Settings) *Manager {
	if ev == nil {
		ev = nopEvents{}
	}
	return &Manager{Store: st, events: ev, settings: s,
		conns: map[string]*openConn{}, sessions: map[string]*session{}, results: map[string]*resultSet{}, jobs: map[string]*jobState{}}
}

// open establishes the network path and driver connection for a profile.
func open(ctx context.Context, c model.Connection) (*openConn, error) {
	drv, err := driver.Get(c.Type)
	if err != nil {
		return nil, err
	}
	var d tunnel.Dialer = tunnel.NewDirect()
	if c.SSH.Enabled {
		s, err := tunnel.NewSSH(ctx, c.SSH)
		if err != nil {
			return nil, err
		}
		d = s
	}
	conn, err := drv.Open(ctx, c, d)
	if err != nil {
		d.Close()
		return nil, err
	}
	return &openConn{profile: c, dialer: d, conn: conn}, nil
}

// TestConnection opens and immediately closes a connection; it returns the server version.
func (m *Manager) TestConnection(ctx context.Context, c model.Connection) (string, error) {
	oc, err := open(ctx, c)
	if err != nil {
		return "", err
	}
	defer oc.close()
	return oc.conn.ServerVersion(), nil
}

func (oc *openConn) close() {
	oc.conn.Close()
	oc.dialer.Close()
}

// Connect opens a saved connection (no-op if already open). password overrides a
// password that wasn't saved.
func (m *Manager) Connect(ctx context.Context, id, password string) (string, error) {
	m.mu.Lock()
	if oc, ok := m.conns[id]; ok {
		m.mu.Unlock()
		return oc.conn.ServerVersion(), nil
	}
	m.mu.Unlock()

	c, err := m.Store.Get(id)
	if err != nil {
		return "", err
	}
	if password != "" {
		c.Password = password
	}
	oc, err := open(ctx, c)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.conns[id]; ok { // lost a race with another Connect
		oc.close()
		return existing.conn.ServerVersion(), nil
	}
	m.conns[id] = oc
	return oc.conn.ServerVersion(), nil
}

// Disconnect closes a connection and every session and result belonging to it.
func (m *Manager) Disconnect(id string) {
	m.mu.Lock()
	oc, ok := m.conns[id]
	delete(m.conns, id)
	var toClose []*session
	for sid, s := range m.sessions {
		if s.connID == id {
			toClose = append(toClose, s)
			delete(m.sessions, sid)
		}
	}
	m.mu.Unlock()
	for _, s := range toClose {
		m.closeSession(s)
	}
	if ok {
		oc.close()
		m.events.ConnectionClosed(id)
	}
}

// IsConnected reports whether a profile is open.
func (m *Manager) IsConnected(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.conns[id]
	return ok
}

// Close shuts everything down.
func (m *Manager) Close() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.conns))
	for id := range m.conns {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.Disconnect(id)
	}
}

var ErrNotConnected = errors.New("not connected")

func (m *Manager) conn(id string) (driver.Conn, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oc, ok := m.conns[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotConnected, id)
	}
	return oc.conn, nil
}

// CloseDatabase closes editor sessions using a database and releases its pool.
func (m *Manager) CloseDatabase(connID, database string) error {
	c, err := m.conn(connID)
	if err != nil {
		return err
	}
	m.mu.Lock()
	var toClose []*session
	for sid, s := range m.sessions {
		if s.connID == connID && s.database == database {
			toClose = append(toClose, s)
			delete(m.sessions, sid)
		}
	}
	m.mu.Unlock()
	for _, s := range toClose {
		m.closeSession(s)
	}
	c.CloseDatabase(database)
	return nil
}

// Info summarises an open connection.
func (m *Manager) Info(connID string) (model.ServerInfo, error) {
	m.mu.Lock()
	oc, ok := m.conns[connID]
	m.mu.Unlock()
	if !ok {
		return model.ServerInfo{}, fmt.Errorf("%w: %s", ErrNotConnected, connID)
	}
	p := oc.profile
	info := model.ServerInfo{Version: oc.conn.ServerVersion(), Sessions: oc.conn.OpenConnections(),
		Host: fmt.Sprintf("%s:%d", p.Host, p.Port), User: p.User, Via: "direct", SSL: p.SSL.Enabled}
	switch {
	case p.SSH.Enabled:
		info.Via = fmt.Sprintf("SSH %s@%s:%d", p.SSH.User, p.SSH.Host, p.SSH.Port)
	case p.Advanced.SocketFile != "" && (p.Type == model.MySQL || p.Type == model.MariaDB):
		info.Via = "socket " + p.Advanced.SocketFile
		info.Host = "localhost"
	}
	return info, nil
}

// --- server monitor ----------------------------------------------------------

func (m *Manager) Processes(ctx context.Context, connID string) ([]model.Process, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.Processes(ctx)
}

func (m *Manager) KillProcess(ctx context.Context, connID, id string, queryOnly bool) error {
	c, err := m.conn(connID)
	if err != nil {
		return err
	}
	return c.KillProcess(ctx, id, queryOnly)
}

func (m *Manager) Variables(ctx context.Context, connID string) ([]model.ServerVar, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.Variables(ctx)
}

func (m *Manager) ServerStatus(ctx context.Context, connID string) ([]model.ServerVar, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.Status(ctx)
}

// --- metadata --------------------------------------------------------------

func (m *Manager) Dialect(connID string) (driver.Dialect, error) {
	c, err := m.conn(connID)
	if err != nil {
		return driver.Dialect{}, err
	}
	return c.Dialect(), nil
}

func (m *Manager) ListDatabases(ctx context.Context, connID string) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	dbs, err := c.ListDatabases(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	custom := m.conns[connID].profile.Databases
	m.mu.Unlock()
	if custom.Enabled && len(custom.List) > 0 {
		// The custom list decides which databases show, in its order.
		exists := map[string]bool{}
		for _, d := range dbs {
			exists[d] = true
		}
		dbs = dbs[:0]
		for _, e := range custom.List {
			if exists[e.Name] {
				dbs = append(dbs, e.Name)
			}
		}
	}
	return dbs, nil
}

func (m *Manager) ListSchemas(ctx context.Context, connID, database string) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.ListSchemas(ctx, database)
}

func (m *Manager) ListObjects(ctx context.Context, connID, database, schema string, kind model.ObjectKind) ([]model.ObjectInfo, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.ListObjects(ctx, database, schema, kind)
}

func (m *Manager) TableDetail(ctx context.Context, connID string, ref model.ObjectRef) (model.TableDetail, error) {
	c, err := m.conn(connID)
	if err != nil {
		return model.TableDetail{}, err
	}
	return c.TableDetail(ctx, ref)
}

func (m *Manager) Catalog(ctx context.Context, connID, database string) (model.Catalog, error) {
	c, err := m.conn(connID)
	if err != nil {
		return model.Catalog{}, err
	}
	return c.Catalog(ctx, database)
}

func (m *Manager) DDL(ctx context.Context, connID string, ref model.ObjectRef) (string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return "", err
	}
	return c.DDL(ctx, ref)
}

func (m *Manager) BuiltinFunctions(connID string) ([]string, error) {
	c, err := m.conn(connID)
	if err != nil {
		return nil, err
	}
	return c.BuiltinFunctions(), nil
}

func newID() string { return uuid.NewString() }
