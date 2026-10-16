package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"chloe/internal/bi"
	"chloe/internal/driver"
	"chloe/internal/model"
)

// A dashboard asks for its charts all at once. Chart reads on the same data
// source with the same filters that arrive within a moment of each other share
// one read of the source (bi.BatchQuery): a page with eight charts on a slow
// query runs it once, not eight times. A chart read alone, or a batch the
// database refuses, runs one chart at a time as before.

// BIBatchWindow is how long a chart read waits for others on the same source.
var BIBatchWindow = 20 * time.Millisecond

// biMaxBatch caps the charts in one statement.
const biMaxBatch = 12

type biBatcher struct {
	mu     sync.Mutex
	open   map[string]*biBatch
	shared atomic.Int64 // chart reads served by a shared read (for tests)
}

type biBatch struct {
	src   bi.DataSource
	from  bi.Source
	d     driver.Dialect
	parts []*biBatchPart
}

type biBatchPart struct {
	c     bi.Chart
	q     bi.Stmt // the chart's own statement, for reading it alone
	limit int
	done  chan struct{}
	cols  []model.ResultColumn
	rows  [][]*string
	more  bool // rows past limit were left out
	err   error
}

// batchKey groups chart reads that can share a read: the same source, read the
// same way, with the same filters.
func batchKey(src bi.DataSource, from bi.Source, c bi.Chart) string {
	b, _ := json.Marshal([]any{src.ConnID, src.Database, src.Schema, src.Timezone, from.SQL, from.Args, src.CustomFields, src.Filters, src.Fields, c.Filters})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// biBatched reads a chart's statement q, sharing the read of its source with
// other charts read at the same moment where it can.
func (m *Manager) biBatched(ctx context.Context, d driver.Dialect, src bi.DataSource, from bi.Source, c bi.Chart, q bi.Stmt, limit int) ([]model.ResultColumn, [][]*string, bool, error) {
	if !bi.Batchable(d, src) || BIBatchWindow <= 0 {
		return m.biRead(ctx, src, q, limit)
	}
	p := &biBatchPart{c: c, q: q, limit: limit, done: make(chan struct{})}
	key := batchKey(src, from, c)
	bt := &m.biBatch
	bt.mu.Lock()
	if bt.open == nil {
		bt.open = map[string]*biBatch{}
	}
	b := bt.open[key]
	if b == nil {
		b = &biBatch{src: src, from: from, d: d}
		bt.open[key] = b
		time.AfterFunc(BIBatchWindow, func() { m.closeBatch(key, b) })
	}
	b.parts = append(b.parts, p)
	if len(b.parts) >= biMaxBatch {
		delete(bt.open, key)
		go m.runBatch(b)
	}
	bt.mu.Unlock()
	select {
	case <-p.done:
		return p.cols, p.rows, p.more, p.err
	case <-ctx.Done():
		return nil, nil, false, ctx.Err()
	}
}

// closeBatch stops a batch taking more charts and runs it (unless it filled up
// and ran already).
func (m *Manager) closeBatch(key string, b *biBatch) {
	m.biBatch.mu.Lock()
	if m.biBatch.open[key] != b {
		m.biBatch.mu.Unlock()
		return
	}
	delete(m.biBatch.open, key)
	m.biBatch.mu.Unlock()
	m.runBatch(b)
}

func (m *Manager) runBatch(b *biBatch) {
	// The read belongs to every chart in it, not to the first one's request.
	ctx, cancel := context.WithTimeout(context.Background(), BITimeout)
	defer cancel()
	finish := func(p *biBatchPart, cols []model.ResultColumn, rows [][]*string, more bool, err error) {
		p.cols, p.rows, p.more, p.err = cols, rows, more, err
		close(p.done)
	}
	alone := func(ps []*biBatchPart) {
		var wg sync.WaitGroup
		for _, p := range ps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cols, rows, more, err := m.biRead(ctx, b.src, p.q, p.limit)
				finish(p, cols, rows, more, err)
			}()
		}
		wg.Wait()
	}
	if len(b.parts) == 1 {
		alone(b.parts)
		return
	}
	charts := make([]bi.Chart, len(b.parts))
	limits := make([]int, len(b.parts))
	total := 0
	for i, p := range b.parts {
		// One row past each chart's limit tells whether it was cut off.
		charts[i], limits[i] = p.c, p.limit+1
		total += p.limit + 1
	}
	q, layout, err := bi.BatchQuery(b.d, b.from, b.src, charts, limits)
	if err != nil {
		alone(b.parts)
		return
	}
	cols, rows, _, err := m.biRead(ctx, b.src, q, total)
	if err != nil {
		// One chart the database refuses would fail them all: read each alone, so
		// only that one shows the error. A read out of time would only time out again.
		if errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			for _, p := range b.parts {
				finish(p, nil, nil, false, err)
			}
			return
		}
		alone(b.parts)
		return
	}
	m.biBatch.shared.Add(int64(len(b.parts)))
	for i, p := range b.parts {
		part, err := bi.SplitBatch(rows, i, layout[i])
		var pc []model.ResultColumn
		if err == nil && len(cols) >= 1+layout[i].Offset+layout[i].Width {
			pc = cols[1+layout[i].Offset : 1+layout[i].Offset+layout[i].Width]
		}
		more := len(part) > p.limit
		if more {
			part = part[:p.limit]
		}
		finish(p, pc, part, more, err)
	}
}

// biRead runs one statement on one of the source's connection's BI sessions.
func (m *Manager) biRead(ctx context.Context, src bi.DataSource, q bi.Stmt, limit int) (cols []model.ResultColumn, rows [][]*string, more bool, err error) {
	err = m.biLive(ctx, src, func(ctx context.Context, ds driver.Session, _ driver.Dialect) error {
		cols, rows, more, err = readStmt(ctx, ds, q, limit)
		return err
	})
	return
}
