package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"

	"github.com/google/uuid"

	"chloe/internal/model"
	"chloe/internal/rdb"
)

// Redis connections are served by rdb.Client rather than a SQL driver.

func (m *Manager) redis(connID string) (*rdb.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oc, ok := m.conns[connID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNotConnected, connID)
	}
	if oc.kv == nil {
		return nil, fmt.Errorf("%s is not a Redis connection", oc.profile.Name)
	}
	return oc.kv, nil
}

// Redis returns the Redis client of an open connection.
func (m *Manager) Redis(connID string) (*rdb.Client, error) { return m.redis(connID) }

type redisConsole struct {
	connID string
	c      *rdb.Console
}

// OpenRedisConsole reserves a connection for a console tab.
func (m *Manager) OpenRedisConsole(ctx context.Context, connID string, db int) (string, error) {
	kv, err := m.redis(connID)
	if err != nil {
		return "", err
	}
	c, err := kv.OpenConsole(ctx, db)
	if err != nil {
		return "", err
	}
	id := uuid.NewString()
	m.consolesMu.Lock()
	m.consoles[id] = &redisConsole{connID: connID, c: c}
	m.consolesMu.Unlock()
	return id, nil
}

// RedisConsoleResult is the output of one console command.
type RedisConsoleResult struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
	// DB is the console's database after the command (SELECT changes it).
	DB int `json:"db"`
}

func (m *Manager) RedisConsoleExec(ctx context.Context, id, line string) (RedisConsoleResult, error) {
	m.consolesMu.Lock()
	rc, ok := m.consoles[id]
	m.consolesMu.Unlock()
	if !ok {
		return RedisConsoleResult{}, fmt.Errorf("console closed")
	}
	out, err := rc.c.Exec(ctx, line)
	res := RedisConsoleResult{Output: out, DB: rc.c.DB}
	if err != nil {
		res.Error = err.Error()
	}
	return res, nil
}

func (m *Manager) CloseRedisConsole(id string) {
	m.consolesMu.Lock()
	rc, ok := m.consoles[id]
	delete(m.consoles, id)
	m.consolesMu.Unlock()
	if ok {
		rc.c.Close()
	}
}

func (m *Manager) closeConsoles(connID string) {
	m.consolesMu.Lock()
	var gone []*redisConsole
	for id, rc := range m.consoles {
		if rc.connID == connID {
			gone = append(gone, rc)
			delete(m.consoles, id)
		}
	}
	m.consolesMu.Unlock()
	for _, rc := range gone {
		rc.c.Close()
	}
}

// ---- server monitor ----------------------------------------------------------------

func redisProcesses(ctx context.Context, kv *rdb.Client) ([]model.Process, error) {
	cls, err := kv.Clients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Process, 0, len(cls))
	for _, c := range cls {
		age, _ := strconv.ParseInt(c["idle"], 10, 64)
		out = append(out, model.Process{
			ID: c["id"], User: c["user"], Host: c["addr"], Database: "db" + c["db"], Application: c["name"],
			Command: c["cmd"], State: c["flags"], TimeSec: age, Query: c["cmd"], Self: c["name"] == "Chloe",
		})
	}
	return out, nil
}

func redisConfig(ctx context.Context, kv *rdb.Client) ([]model.ServerVar, error) {
	cfg, err := kv.Config(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.ServerVar, 0, len(cfg))
	for k, v := range cfg {
		out = append(out, model.ServerVar{Name: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func redisStatus(ctx context.Context, kv *rdb.Client) ([]model.ServerVar, error) {
	info, err := kv.Info(ctx)
	if err != nil {
		return nil, err
	}
	var out []model.ServerVar
	for _, s := range info {
		for _, f := range s.Fields {
			out = append(out, model.ServerVar{Name: f[0], Value: f[1], Description: s.Name})
		}
	}
	return out, nil
}
