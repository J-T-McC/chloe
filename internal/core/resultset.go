package core

import (
	"sync"

	"chloe/internal/driver"
	"chloe/internal/model"
)

// resultSet buffers a statement's rows in memory while a goroutine keeps reading
// from the server. Readers wait on cond until the rows they need arrive.
type resultSet struct {
	id      string
	columns []model.ResultColumn

	mu        sync.Mutex
	cond      *sync.Cond
	rows      [][]*string
	complete  bool
	truncated bool
	err       error
	cancel    func()
}

func newResultSet(id string, cols []model.ResultColumn, cancel func()) *resultSet {
	rs := &resultSet{id: id, columns: cols, cancel: cancel}
	rs.cond = sync.NewCond(&rs.mu)
	return rs
}

// fill reads rows until exhausted, maxRows is reached, or the set is closed.
// onProgress is called periodically (outside the lock) with the current count.
func (rs *resultSet) fill(r driver.Rows, maxRows int, onProgress func(n int, done bool)) {
	defer r.Close()
	const batch = 500
	buf := make([][]*string, 0, batch)
	flush := func() bool {
		rs.mu.Lock()
		closed := rs.complete
		if !closed {
			rs.rows = append(rs.rows, buf...)
		}
		rs.cond.Broadcast()
		n := len(rs.rows)
		rs.mu.Unlock()
		buf = buf[:0]
		if onProgress != nil && !closed {
			onProgress(n, false)
		}
		return !closed
	}
	total := 0
	for {
		row := make([]*string, len(rs.columns))
		if !r.Next(row) {
			break
		}
		buf = append(buf, row)
		total++
		if total >= maxRows {
			rs.mu.Lock()
			rs.truncated = true
			rs.mu.Unlock()
			rs.abortServer()
			break
		}
		if len(buf) == batch && !flush() {
			rs.abortServer()
			return
		}
	}
	rs.mu.Lock()
	if !rs.complete {
		rs.rows = append(rs.rows, buf...)
		if !rs.truncated { // a truncated read was aborted on purpose
			rs.err = r.Err()
		}
		rs.complete = true
	}
	n := len(rs.rows)
	rs.cond.Broadcast()
	rs.mu.Unlock()
	if onProgress != nil {
		onProgress(n, true)
	}
}

// waitFor blocks until at least n rows are buffered or the set is complete.
func (rs *resultSet) waitFor(n int) {
	rs.mu.Lock()
	for len(rs.rows) < n && !rs.complete {
		rs.cond.Wait()
	}
	rs.mu.Unlock()
}

func (rs *resultSet) page(offset, limit int) model.RowPage {
	rs.waitFor(offset + limit)
	rs.mu.Lock()
	defer rs.mu.Unlock()
	p := model.RowPage{Offset: offset, Total: len(rs.rows), Complete: rs.complete}
	if offset < len(rs.rows) {
		end := min(offset+limit, len(rs.rows))
		p.Rows = rs.rows[offset:end]
	} else {
		p.Rows = [][]*string{}
	}
	return p
}

func (rs *resultSet) snapshot() (count int, complete, truncated bool, err error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	return len(rs.rows), rs.complete, rs.truncated, rs.err
}

// abortServer stops the server sending the rest of the result, so closing the
// driver rows doesn't have to drain them.
func (rs *resultSet) abortServer() {
	if rs.cancel != nil {
		rs.cancel()
	}
}

// close stops reading and releases the buffered rows.
func (rs *resultSet) close() {
	// The fill goroutine notices at its next batch and aborts the server side.
	rs.mu.Lock()
	rs.complete = true
	rs.rows = nil
	rs.cond.Broadcast()
	rs.mu.Unlock()
}
