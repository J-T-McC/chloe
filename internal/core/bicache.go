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

// maxCachedBytes bounds the text the cache holds; a result bigger than this
// isn't kept at all.
var maxCachedBytes int64 = 64 << 20

type biResultCache struct {
	mu      sync.Mutex
	entries map[string]*resultEntry
	bytes   int64       // size of the finished entries
	sweep   *time.Timer // drops expired entries while nothing reads
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
	size      int64 // counted in biResultCache.bytes once the query is done
}

// resultSize estimates the memory a result holds.
func resultSize(rows [][]*string) int64 {
	n := int64(0)
	for _, r := range rows {
		n += int64(24 + 8*len(r))
		for _, c := range r {
			if c != nil {
				n += int64(16 + len(*c))
			}
		}
	}
	return n
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
		c.mu.Lock()
		if c.entries[key] == e {
			if size := resultSize(e.rows); size > maxCachedBytes {
				delete(c.entries, key) // too big to keep; the charts waiting still get it
			} else {
				e.size = size
				c.bytes += size
				c.evict()
			}
		}
		c.schedule()
		c.mu.Unlock()
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

// evict drops expired entries, then the least recently used ones over the
// count or size limits. Locked by caller.
func (c *biResultCache) evict() {
	for k, e := range c.entries {
		if time.Since(e.started) >= BICacheTTL && e.done() {
			c.remove(k)
		}
	}
	for len(c.entries) > maxCachedResults || c.bytes > maxCachedBytes {
		var oldest string
		for k, e := range c.entries {
			if oldest == "" || e.used.Before(c.entries[oldest].used) {
				oldest = k
			}
		}
		if oldest == "" {
			return
		}
		c.remove(oldest)
	}
}

// remove forgets one entry. Locked by caller.
func (c *biResultCache) remove(k string) {
	if e := c.entries[k]; e != nil {
		c.bytes -= e.size
		delete(c.entries, k)
	}
}

// schedule sets a sweep for when the oldest entry expires. Locked by caller.
func (c *biResultCache) schedule() {
	if c.sweep != nil || len(c.entries) == 0 {
		return
	}
	c.sweep = time.AfterFunc(BICacheTTL, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.sweep = nil
		c.evict()
		c.schedule()
	})
}

func (e *resultEntry) done() bool {
	select {
	case <-e.ready:
		return true
	default:
		return false
	}
}

// drop forgets a connection's results (on disconnect).
func (c *biResultCache) drop(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.connID == connID {
			c.remove(k)
		}
	}
}
