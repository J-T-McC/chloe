package core

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

// JobStatus is the lifecycle state of a background job.
type JobStatus string

const (
	JobRunning   JobStatus = "running"
	JobDone      JobStatus = "done"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// Job is a long-running operation (export, import, dump, execute SQL file) that
// reports progress to the UI and can be cancelled.
type Job struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Title    string    `json:"title"`
	Status   JobStatus `json:"status"`
	Done     int64     `json:"done"`  // rows or statements processed
	Total    int64     `json:"total"` // 0 when unknown
	Unit     string    `json:"unit"`  // "rows", "statements"
	Errors   int       `json:"errors"`
	Message  string    `json:"message,omitempty"`
	Log      []string  `json:"log"`
	Path     string    `json:"path,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
}

const maxJobLog = 500

type jobState struct {
	mu     sync.Mutex
	job    Job
	cancel context.CancelFunc
	last   time.Time
}

// progress is the handle a job body uses to report.
type progress struct {
	m  *Manager
	st *jobState
}

func (p *progress) set(done, total int64) {
	p.st.mu.Lock()
	p.st.job.Done, p.st.job.Total = done, total
	p.st.mu.Unlock()
	p.emit(false)
}

// setDone updates the processed count, keeping the total.
func (p *progress) setDone(done int64) {
	p.st.mu.Lock()
	p.st.job.Done = done
	p.st.mu.Unlock()
	p.emit(false)
}

func (p *progress) setPath(path string) {
	p.st.mu.Lock()
	p.st.job.Path = path
	p.st.mu.Unlock()
}

func (p *progress) errorCount() int {
	p.st.mu.Lock()
	defer p.st.mu.Unlock()
	return p.st.job.Errors
}

func (p *progress) add(n int64) {
	p.st.mu.Lock()
	p.st.job.Done += n
	p.st.mu.Unlock()
	p.emit(false)
}

func (p *progress) logf(format string, args ...any) {
	p.st.mu.Lock()
	line := time.Now().Format("15:04:05") + "  " + fmt.Sprintf(format, args...)
	p.st.job.Log = append(p.st.job.Log, line)
	if len(p.st.job.Log) > maxJobLog {
		p.st.job.Log = p.st.job.Log[len(p.st.job.Log)-maxJobLog:]
	}
	p.st.mu.Unlock()
	p.emit(false)
}

func (p *progress) error(format string, args ...any) {
	p.st.mu.Lock()
	p.st.job.Errors++
	p.st.mu.Unlock()
	p.logf("ERROR: "+format, args...)
}

// emit sends a snapshot to the UI, at most every 150ms unless forced.
func (p *progress) emit(force bool) {
	p.st.mu.Lock()
	if !force && time.Since(p.st.last) < 150*time.Millisecond {
		p.st.mu.Unlock()
		return
	}
	p.st.last = time.Now()
	snap := p.st.job
	snap.Log = append([]string(nil), snap.Log...)
	p.st.mu.Unlock()
	p.m.events.JobProgress(snap)
}

// startJob runs fn in the background and tracks it.
func (m *Manager) startJob(kind, title, unit string, fn func(ctx context.Context, p *progress) (string, error)) Job {
	ctx, cancel := context.WithCancel(context.Background())
	st := &jobState{job: Job{ID: newID(), Kind: kind, Title: title, Status: JobRunning, Unit: unit, Started: time.Now(), Log: []string{}}, cancel: cancel}
	m.jobsMu.Lock()
	m.jobs[st.job.ID] = st
	m.jobsMu.Unlock()
	p := &progress{m: m, st: st}
	p.emit(true)
	go func() {
		defer cancel()
		msg, err := fn(ctx, p)
		st.mu.Lock()
		st.job.Finished = time.Now()
		switch {
		case ctx.Err() != nil:
			st.job.Status = JobCancelled
			st.job.Message = "Cancelled"
		case err != nil:
			st.job.Status = JobFailed
			st.job.Message = err.Error()
		default:
			st.job.Status = JobDone
			st.job.Message = msg
		}
		st.mu.Unlock()
		if err != nil && ctx.Err() == nil {
			p.logf("FAILED: %v", err)
		}
		p.emit(true)
	}()
	return st.job
}

// Jobs lists jobs, newest first.
func (m *Manager) Jobs() []Job {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	out := make([]Job, 0, len(m.jobs))
	for _, st := range m.jobs {
		st.mu.Lock()
		j := st.job
		j.Log = append([]string(nil), j.Log...)
		st.mu.Unlock()
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	return out
}

func (m *Manager) Job(id string) (Job, bool) {
	m.jobsMu.Lock()
	st, ok := m.jobs[id]
	m.jobsMu.Unlock()
	if !ok {
		return Job{}, false
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	j := st.job
	j.Log = append([]string(nil), j.Log...)
	return j, true
}

func (m *Manager) CancelJob(id string) {
	m.jobsMu.Lock()
	st, ok := m.jobs[id]
	m.jobsMu.Unlock()
	if ok {
		st.cancel()
	}
}

// ClearJobs forgets finished jobs.
func (m *Manager) ClearJobs() {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	for id, st := range m.jobs {
		st.mu.Lock()
		running := st.job.Status == JobRunning
		st.mu.Unlock()
		if !running {
			delete(m.jobs, id)
		}
	}
}

// WaitJob blocks until a job finishes (used by tests and scripted callers).
func (m *Manager) WaitJob(ctx context.Context, id string) (Job, error) {
	for {
		j, ok := m.Job(id)
		if !ok {
			return j, fmt.Errorf("job %s not found", id)
		}
		if j.Status != JobRunning {
			return j, nil
		}
		select {
		case <-ctx.Done():
			return j, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
