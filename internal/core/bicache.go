package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"chloe/internal/bi"
	"chloe/internal/model"
)

// Live chart results are kept for a few minutes and shared: going back to a
// dashboard page, the same chart on several pages, and exporting a dashboard
// don't query the database again. Each chart's query still runs on its database,
// so text compares, groups and sorts by the database's own collation. (Reading a
// source once and aggregating it in SQLite, as archives do, would group "North"
// and "north" apart where MySQL groups them together.) Refresh asks for results
// read after a time (since), so each query runs again once.

// BICacheTTL is how long a chart query's result serves charts.
var BICacheTTL = 5 * time.Minute

const maxCachedResults = 256

type biResultCache struct {
	mu      sync.Mutex
	entries map[string]*resultEntry
}

type resultEntry struct {
	connID    string
	ready     chan struct{} // closed once the query is done
	cols      []model.ResultColumn
	rows      [][]*string
	truncated bool
	err       error
	started   time.Time
	used      time.Time
}

// resultKey identifies a chart query: where it runs and the statement with its
// bound values.
func resultKey(src bi.DataSource, q bi.Stmt, limit int) string {
	b, _ := json.Marshal([]any{src.ConnID, src.Database, src.Schema, src.Timezone, q.SQL, q.Args, limit})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fresh reports whether an entry may serve a read wanting data from since on.
func (e *resultEntry) fresh(since time.Time) bool {
	return !e.started.Before(since) && time.Since(e.started) < BICacheTTL
}

func (e *resultEntry) failed() bool {
	select {
	case <-e.ready:
		return e.err != nil
	default:
		return false
	}
}

// cachedResult returns the result of a chart query run at or after since,
// running it (once, for every chart waiting on it) if needed. hit reports that an
// earlier read's result was used.
func (m *Manager) cachedResult(ctx context.Context, src bi.DataSource, q bi.Stmt, limit int, since time.Time, run func(context.Context) ([]model.ResultColumn, [][]*string, bool, error)) (e *resultEntry, hit bool, err error) {
	key := resultKey(src, q, limit)
	c := &m.biCache
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*resultEntry{}
	}
	e = c.entries[key]
	if e == nil || !e.fresh(since) || e.failed() {
		now := time.Now()
		e = &resultEntry{connID: src.ConnID, ready: make(chan struct{}), started: now, used: now}
		c.entries[key] = e
		c.evict()
		c.mu.Unlock()
		// The query belongs to every chart waiting on it, not to the first one's request.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), BITimeout)
		e.cols, e.rows, e.truncated, e.err = run(rctx)
		cancel()
		close(e.ready)
	} else {
		e.used, hit = time.Now(), true
		c.mu.Unlock()
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}
	return e, hit, e.err
}

// evict drops the least recently used entries over the limit. Locked by caller.
func (c *biResultCache) evict() {
	for len(c.entries) > maxCachedResults {
		var oldest string
		for k, e := range c.entries {
			if oldest == "" || e.used.Before(c.entries[oldest].used) {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
}

// drop forgets a connection's results (on disconnect).
func (c *biResultCache) drop(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.connID == connID {
			delete(c.entries, k)
		}
	}
}
