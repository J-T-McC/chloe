package services

import (
	"context"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/tunnel"
)

// SSH host key events emitted to the frontend.
const (
	EventHostKey     = "ssh:hostkey"      // a tunnel waits for the user to trust a host's key
	EventHostKeyDone = "ssh:hostkey-done" // the question was answered elsewhere or timed out
)

func init() {
	application.RegisterEvent[tunnel.HostKeyPrompt](EventHostKey)
	application.RegisterEvent[string](EventHostKeyDone)
}

// hostKeyWait is how long a tunnel waits for an answer before giving up.
const hostKeyWait = 2 * time.Minute

// HostKeys asks in the app whether to trust an SSH host seen for the first time.
// Tunnels opening to the same host and key at once share one question.
type HostKeys struct {
	mu      sync.Mutex
	pending map[string]*hostKeyQuestion // by prompt ID
}

type hostKeyQuestion struct {
	p       tunnel.HostKeyPrompt
	done    chan struct{}
	trusted bool
}

// Confirm is tunnel.ConfirmHostKey.
func (h *HostKeys) Confirm(ctx context.Context, p tunnel.HostKeyPrompt) bool {
	app := application.Get()
	if app == nil {
		return false
	}
	h.mu.Lock()
	if h.pending == nil {
		h.pending = map[string]*hostKeyQuestion{}
	}
	var q *hostKeyQuestion
	for _, o := range h.pending {
		if o.p.Host == p.Host && o.p.Fingerprint == p.Fingerprint {
			q = o
		}
	}
	asker := q == nil
	if asker {
		q = &hostKeyQuestion{p: p, done: make(chan struct{})}
		h.pending[p.ID] = q
	}
	h.mu.Unlock()
	if asker {
		// When the asker stops waiting, tunnels sharing its question get no too.
		defer h.resolve(q.p.ID, false)
		app.Event.Emit(EventHostKey, p)
		focusMain(app)
	}
	timer := time.NewTimer(hostKeyWait)
	defer timer.Stop()
	select {
	case <-q.done:
		return q.trusted
	case <-ctx.Done():
	case <-timer.C:
	}
	if asker {
		app.Event.Emit(EventHostKeyDone, q.p.ID)
	}
	return false
}

func (h *HostKeys) resolve(id string, trusted bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if q, ok := h.pending[id]; ok {
		delete(h.pending, id)
		q.trusted = trusted
		close(q.done)
	}
}

// HostKeyService answers host key questions from the frontend.
type HostKeyService struct{ H *HostKeys }

// Pending lists the questions waiting for an answer (for a window opened after they were asked).
func (s *HostKeyService) Pending() []tunnel.HostKeyPrompt {
	s.H.mu.Lock()
	defer s.H.mu.Unlock()
	out := []tunnel.HostKeyPrompt{}
	for _, q := range s.H.pending {
		out = append(out, q.p)
	}
	return out
}

// Answer trusts (or not) the host key the question with this ID asked about.
func (s *HostKeyService) Answer(id string, trust bool) { s.H.resolve(id, trust) }
