package core

import (
	"context"
	"sync"
	"time"

	"chloe/internal/driver"
)

// BI reads, MCP reads and the table viewer's counts share a few warm sessions
// per connection instead of opening (and logging in on) a new server connection
// for every one of them. A dashboard page asks
// for all its charts at once; they queue for the connection's sessions, so a
// dashboard can't use up the server's connections.

// BIMaxSessions is how many sessions BI reads use at once on a connection. A
// connection with a session limit keeps one of its sessions free, for cancelling.
var BIMaxSessions = 4

// BIIdleSession is how long an unused BI session is kept open: long enough to
// serve the next page or refresh, well under servers' idle timeouts.
var BIIdleSession = time.Minute

type biSessions struct {
	mu    sync.Mutex
	pools map[string]*biPool // by connection ID
}

type biPool struct {
	slots chan struct{}
	idle  map[string][]idleSession // by database + schema
}

type idleSession struct {
	ds    driver.Session
	since time.Time
	begun bool // a read-only transaction is already open
}

// biBegunFresh is how long a session's open read-only transaction is trusted
// without starting it again: starting it is what notices a session the server
// has closed (or an idle-in-transaction timeout), so one idle for longer
// begins again.
var biBegunFresh = 10 * time.Second

func (b *biSessions) pool(m *Manager, connID string) *biPool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pools == nil {
		b.pools = map[string]*biPool{}
	}
	p := b.pools[connID]
	if p == nil {
		n := BIMaxSessions
		if c, err := m.Store.Profile(connID); err == nil && c.Advanced.SessionLimit > 0 {
			n = min(n, max(1, c.Advanced.SessionLimit-1))
		}
		p = &biPool{slots: make(chan struct{}, n), idle: map[string][]idleSession{}}
		b.pools[connID] = p
	}
	return p
}

// take waits for a free slot and returns a warm session for the database and
// schema if one is idle (reused reports which, begun whether its read-only
// transaction is open and fresh), else nil for the caller to open.
func (b *biSessions) take(ctx context.Context, p *biPool, key string) (ds driver.Session, reused, begun bool, err error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, false, false, ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for list := p.idle[key]; len(list) > 0; list = p.idle[key] {
		s := list[len(list)-1]
		p.idle[key] = list[:len(list)-1]
		if idle := time.Since(s.since); idle < BIIdleSession {
			return s.ds, true, s.begun && idle < biBegunFresh, nil
		}
		go s.ds.Close()
	}
	return nil, false, false, nil
}

// give frees the slot, keeping the session for the next read when it's healthy.
func (b *biSessions) give(p *biPool, key string, ds driver.Session, keep, begun bool) {
	if ds != nil && keep {
		b.mu.Lock()
		p.idle[key] = append(p.idle[key], idleSession{ds: ds, since: time.Now(), begun: begun})
		b.mu.Unlock()
		time.AfterFunc(BIIdleSession, func() { b.prune(p) })
	} else if ds != nil {
		go ds.Close()
	}
	<-p.slots
}

// prune closes sessions idle for too long.
func (b *biSessions) prune(p *biPool) {
	b.mu.Lock()
	var stale []driver.Session
	for key, list := range p.idle {
		kept := list[:0]
		for _, s := range list {
			if time.Since(s.since) >= BIIdleSession {
				stale = append(stale, s.ds)
			} else {
				kept = append(kept, s)
			}
		}
		p.idle[key] = kept
	}
	b.mu.Unlock()
	for _, ds := range stale {
		ds.Close()
	}
}

// drop closes a connection's idle sessions (on disconnect). Sessions in use are
// closed when they're given back, since their connection is gone.
func (b *biSessions) drop(connID string) {
	b.mu.Lock()
	p := b.pools[connID]
	delete(b.pools, connID)
	var all []driver.Session
	if p != nil {
		for _, list := range p.idle {
			for _, s := range list {
				all = append(all, s.ds)
			}
		}
		p.idle = map[string][]idleSession{}
	}
	b.mu.Unlock()
	for _, ds := range all {
		ds.Close()
	}
}

// WithReadSession runs fn in a read-only transaction on one of the connection's
// warm read sessions; see withBISession. Nothing fn does outlives the
// transaction except what a read can leave on a session (MySQL user variables,
// advisory locks), as for BI sources.
func (m *Manager) WithReadSession(ctx context.Context, connID, database, schema string, fn func(driver.Session, driver.Dialect) error) error {
	return m.withBISession(ctx, connID, database, schema, fn)
}

// withBISession runs fn in a read-only transaction on one of the connection's BI
// sessions. When ctx ends first, the statement is cancelled on the server too, so
// an abandoned query doesn't keep running there.
func (m *Manager) withBISession(ctx context.Context, connID, database, schema string, fn func(driver.Session, driver.Dialect) error) error {
	if err := m.Ensure(ctx, connID); err != nil {
		return err
	}
	c, err := m.conn(connID)
	if err != nil {
		return err
	}
	d := c.Dialect()
	p := m.bi.pool(m, connID)
	key := database + "\x00" + schema
	ds, reused, begun, err := m.bi.take(ctx, p, key)
	if err != nil {
		return err
	}
	keep := false
	defer func() { m.bi.give(p, key, ds, keep, begun) }()
	begin := func() error {
		if begun {
			return nil // the last read left the next read-only transaction open
		}
		if ds == nil {
			if ds, err = c.NewSession(ctx, database, schema); err != nil {
				return err
			}
		}
		return driver.BeginReadOnly(ctx, ds, d)
	}
	if err := begin(); err != nil {
		if !reused || ctx.Err() != nil {
			return err
		}
		// The server may have closed an idle session; open a new one.
		go ds.Close()
		ds = nil
		if err := begin(); err != nil {
			return err
		}
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			cctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = ds.Cancel(cctx)
			cancel()
		case <-done:
		}
	}()
	err = fn(ds, d)
	close(done)
	<-stopped
	if ctx.Err() != nil {
		return err
	}
	begun, keep = driver.RestartReadOnly(ds, d)
	return err
}
