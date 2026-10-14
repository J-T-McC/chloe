package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"

	"chloe/internal/mdb"
	"chloe/internal/model"
)

// MongoDB connections are served by mdb.Client rather than a SQL driver.

func (m *Manager) mongo(connID string) (*mdb.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oc, ok := m.conns[connID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotConnected, connID)
	}
	if oc.doc == nil {
		return nil, fmt.Errorf("%s is not a MongoDB connection", oc.profile.Name)
	}
	return oc.doc, nil
}

// Mongo returns the MongoDB client of an open connection.
func (m *Manager) Mongo(connID string) (*mdb.Client, error) { return m.mongo(connID) }

// MongoDatabases lists databases, limited to the profile's custom list when it has one.
func (m *Manager) MongoDatabases(ctx context.Context, connID string) ([]mdb.DBInfo, error) {
	c, err := m.mongo(connID)
	if err != nil {
		return nil, err
	}
	dbs, err := c.Databases(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	custom := m.conns[connID].profile.Databases
	m.mu.Unlock()
	if !custom.Enabled || len(custom.List) == 0 {
		return dbs, nil
	}
	byName := map[string]mdb.DBInfo{}
	for _, d := range dbs {
		byName[d.Name] = d
	}
	out := make([]mdb.DBInfo, 0, len(custom.List))
	for _, e := range custom.List {
		if d, ok := byName[e.Name]; ok {
			out = append(out, d)
		} else {
			// Listed but not visible (no listDatabases privilege, or empty): show it anyway.
			out = append(out, mdb.DBInfo{Name: e.Name, Empty: true})
		}
	}
	return out, nil
}

// ---- shells ------------------------------------------------------------------------

type mongoShell struct {
	connID string
	sh     *mdb.Shell
	mu     sync.Mutex
	cancel context.CancelFunc
}

// OpenMongoShell starts a shell (for a query tab or the console) on database db.
func (m *Manager) OpenMongoShell(connID, db string) (string, error) {
	c, err := m.mongo(connID)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	m.consolesMu.Lock()
	m.shells[id] = &mongoShell{connID: connID, sh: c.NewShell(db)}
	m.consolesMu.Unlock()
	return id, nil
}

func (m *Manager) mongoShell(id string) (*mongoShell, error) {
	m.consolesMu.Lock()
	defer m.consolesMu.Unlock()
	s, ok := m.shells[id]
	if !ok {
		return nil, fmt.Errorf("the shell is closed")
	}
	return s, nil
}

// RunMongo runs a script; cursor results return their first pageSize documents.
func (m *Manager) RunMongo(ctx context.Context, id, script string, pageSize int) (mdb.RunOutput, error) {
	s, err := m.mongoShell(id)
	if err != nil {
		return mdb.RunOutput{}, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	out := s.sh.Run(ctx, script, pageSize)
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()
	return out, nil
}

// StopMongo interrupts the script a shell is running.
func (m *Manager) StopMongo(id string) {
	s, err := m.mongoShell(id)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

// FetchMongo returns more documents of a cursor result.
func (m *Manager) FetchMongo(ctx context.Context, id, cursorID string, skip, limit int) (mdb.Page, bool, error) {
	s, err := m.mongoShell(id)
	if err != nil {
		return mdb.Page{}, false, err
	}
	return s.sh.Fetch(ctx, cursorID, skip, limit)
}

// MongoShellDB is the shell's current database.
func (m *Manager) MongoShellDB(id string) string {
	s, err := m.mongoShell(id)
	if err != nil {
		return ""
	}
	return s.sh.DB
}

func (m *Manager) CloseMongoShell(id string) {
	m.consolesMu.Lock()
	delete(m.shells, id)
	m.consolesMu.Unlock()
}

func (m *Manager) closeShells(connID string) {
	m.consolesMu.Lock()
	defer m.consolesMu.Unlock()
	for id, s := range m.shells {
		if s.connID == connID {
			s.mu.Lock()
			if s.cancel != nil {
				s.cancel()
			}
			s.mu.Unlock()
			delete(m.shells, id)
		}
	}
}

// ---- server monitor ----------------------------------------------------------------

func mongoProcesses(ctx context.Context, c *mdb.Client) ([]model.Process, error) {
	r, err := c.RunCommand(ctx, "admin", bson.D{{Key: "currentOp", Value: 1}, {Key: "$all", Value: true}})
	if err != nil {
		return nil, err
	}
	var ops bson.A
	for _, e := range r {
		if e.Key == "inprog" {
			ops, _ = e.Value.(bson.A)
		}
	}
	out := make([]model.Process, 0, len(ops))
	for _, o := range ops {
		d, ok := o.(bson.D)
		if !ok {
			continue
		}
		get := func(k string) any {
			for _, e := range d {
				if e.Key == k {
					return e.Value
				}
			}
			return nil
		}
		str := func(k string) string {
			v := get(k)
			if v == nil {
				return ""
			}
			if s, ok := v.(string); ok {
				return s
			}
			return mdb.Cell(mdb.ToVal(v))
		}
		opid := str("opid")
		if opid == "" {
			continue
		}
		p := model.Process{ID: opid, Host: str("client"), Command: str("op"), State: str("desc"), Application: str("appName")}
		if ns := str("ns"); ns != "" {
			p.Database, _, _ = strings.Cut(ns, ".")
		}
		if users, ok := get("effectiveUsers").(bson.A); ok && len(users) > 0 {
			if u, ok := users[0].(bson.D); ok && len(u) > 0 {
				p.User = fmt.Sprint(u[0].Value)
			}
		}
		if active, _ := get("active").(bool); !active {
			p.State = "idle"
		}
		p.TimeSec, _ = strconv.ParseInt(str("secs_running"), 10, 64)
		if cmd := get("command"); cmd != nil {
			p.Query = mdb.Format(mdb.ToVal(cmd), "", false)
		}
		p.Self = p.Application == "Chloe" && strings.Contains(p.Query, "currentOp")
		out = append(out, p)
	}
	return out, nil
}

func mongoKill(ctx context.Context, c *mdb.Client, id string) error {
	var op any = id
	if n, err := strconv.ParseInt(id, 10, 64); err == nil {
		op = n
	}
	_, err := c.RunCommand(ctx, "admin", bson.D{{Key: "killOp", Value: 1}, {Key: "op", Value: op}})
	return err
}

func mongoParameters(ctx context.Context, c *mdb.Client) ([]model.ServerVar, error) {
	r, err := c.RunCommand(ctx, "admin", bson.D{{Key: "getParameter", Value: "*"}})
	if err != nil {
		return nil, err
	}
	out := make([]model.ServerVar, 0, len(r))
	for _, e := range r {
		if e.Key == "ok" {
			continue
		}
		out = append(out, model.ServerVar{Name: e.Key, Value: scalarText(e.Value)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// mongoStatus flattens serverStatus into dotted names, grouped by section.
func mongoStatus(ctx context.Context, c *mdb.Client) ([]model.ServerVar, error) {
	r, err := c.RunCommand(ctx, "admin", bson.D{{Key: "serverStatus", Value: 1}})
	if err != nil {
		return nil, err
	}
	var out []model.ServerVar
	var walk func(section, prefix string, d bson.D, depth int)
	walk = func(section, prefix string, d bson.D, depth int) {
		for _, e := range d {
			name := prefix + e.Key
			if sub, ok := e.Value.(bson.D); ok && depth < 3 {
				walk(section, name+".", sub, depth+1)
				continue
			}
			out = append(out, model.ServerVar{Name: name, Value: scalarText(e.Value), Description: section})
		}
	}
	for _, e := range r {
		if sub, ok := e.Value.(bson.D); ok {
			walk(e.Key, e.Key+".", sub, 1)
		} else if e.Key != "ok" {
			out = append(out, model.ServerVar{Name: e.Key, Value: scalarText(e.Value), Description: "server"})
		}
	}
	return out, nil
}

func scalarText(v any) string {
	val := mdb.ToVal(v)
	switch val.T {
	case "object", "array":
		return mdb.Format(val, "", false)
	}
	return mdb.Cell(val)
}
