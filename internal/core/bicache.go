package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"chloe/internal/bi"
)

// Live data sources are read once and kept for a few minutes in memory, as an
// archive snapshot is kept on disk: every chart on the source then aggregates
// that copy instead of running the source's query again. A dashboard page with
// eight charts on one source reads it once, and going back to a page doesn't
// read anything. Refresh asks for data read after a time (since), so it reads
// each source again once, however many charts use it.

// BICacheTTL is how long a read of a live source serves charts.
var BICacheTTL = 5 * time.Minute

// BICacheMaxRows bounds a cached source. Bigger sources are aggregated by their
// database chart by chart, as before.
var BICacheMaxRows int64 = 500_000

const (
	maxCachedSources = 16
	// A source too big to cache isn't tried again for a while.
	tooBigFor = 30 * time.Minute
)

var errTooBig = errors.New("too many rows to cache")

type biSourceCache struct {
	mu      sync.Mutex
	entries map[string]*sourceEntry
	tooBig  map[string]time.Time
}

type sourceEntry struct {
	connID  string
	ready   chan struct{} // closed once the read is done
	db      *sql.DB
	err     error
	started time.Time
	used    time.Time
}

// sourceKey identifies what a live source reads: where, the bound query, and
// the fields its copy is typed by.
func sourceKey(src bi.DataSource, from bi.Source) string {
	b, _ := json.Marshal([]any{src.ConnID, src.Database, src.Schema, from.SQL, from.Args, src.Fields})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fresh reports whether an entry may serve a read wanting data from since on.
func (e *sourceEntry) fresh(since time.Time) bool {
	return !e.started.Before(since) && time.Since(e.started) < BICacheTTL
}

// cachedSource returns an in-memory copy of a live source's rows read at or after
// since, reading it if needed; nil when the source is too big to keep.
func (m *Manager) cachedSource(ctx context.Context, src bi.DataSource, from bi.Source, since time.Time) (*sourceEntry, error) {
	key := sourceKey(src, from)
	c := &m.biCache
	c.mu.Lock()
	if c.entries == nil {
		c.entries, c.tooBig = map[string]*sourceEntry{}, map[string]time.Time{}
	}
	if until, ok := c.tooBig[key]; ok && time.Now().Before(until) {
		c.mu.Unlock()
		return nil, nil
	}
	e := c.entries[key]
	if e == nil || !e.fresh(since) || e.failed() {
		now := time.Now()
		e = &sourceEntry{connID: src.ConnID, ready: make(chan struct{}), started: now, used: now}
		if old := c.entries[key]; old != nil {
			old.retire()
		}
		c.entries[key] = e
		c.evict()
		c.mu.Unlock()
		// The read belongs to every chart waiting on it, not to the first one's request.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), BITimeout)
		e.db, e.err = m.readSource(rctx, src)
		cancel()
		close(e.ready)
		if errors.Is(e.err, errTooBig) {
			c.mu.Lock()
			c.tooBig[key] = time.Now().Add(tooBigFor)
			if c.entries[key] == e {
				delete(c.entries, key)
			}
			c.mu.Unlock()
			return nil, nil
		}
	} else {
		e.used = time.Now()
		c.mu.Unlock()
	}
	select {
	case <-e.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if e.err != nil {
		return nil, e.err
	}
	return e, nil
}

func (e *sourceEntry) failed() bool {
	select {
	case <-e.ready:
		return e.err != nil
	default:
		return false
	}
}

// retire closes an entry's copy once charts reading it have had time to finish.
func (e *sourceEntry) retire() {
	go func() {
		<-e.ready
		if e.db != nil {
			time.AfterFunc(BITimeout, func() { e.db.Close() })
		}
	}()
}

// evict drops the least recently used entries over the limit. Locked by caller.
func (c *biSourceCache) evict() {
	for len(c.entries) > maxCachedSources {
		var oldest string
		for k, e := range c.entries {
			if oldest == "" || e.used.Before(c.entries[oldest].used) {
				oldest = k
			}
		}
		c.entries[oldest].retire()
		delete(c.entries, oldest)
	}
}

// drop forgets a connection's cached sources (on disconnect).
func (c *biSourceCache) drop(connID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, e := range c.entries {
		if e.connID == connID {
			e.retire()
			delete(c.entries, k)
		}
	}
}

// readSource reads a live source's rows into a new in-memory database.
func (m *Manager) readSource(ctx context.Context, src bi.DataSource) (*sql.DB, error) {
	db, err := newMemDB()
	if err != nil {
		return nil, err
	}
	if _, err := m.biCopyRows(ctx, src, src.Fields, db, BICacheMaxRows, errTooBig); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
