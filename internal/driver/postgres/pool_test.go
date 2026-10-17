package postgres

import (
	"context"
	"database/sql"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"chloe/internal/model"
)

// hangingDialer never connects; it counts the attempts.
type hangingDialer struct{ dials atomic.Int32 }

func (h *hangingDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	h.dials.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}
func (h *hangingDialer) Close() error { return nil }

// Opening a pool for a server that doesn't answer holds up neither the pools
// already open, and a failed attempt isn't kept.
func TestPoolOpensOutsideTheLock(t *testing.T) {
	d := &hangingDialer{}
	open, _ := sql.Open("pgx", "")
	c := &Conn{profile: model.Connection{Host: "db.example", Port: 5432, User: "u"}, dialer: d, defaultDB: "a",
		pools: map[string]*pool{"a": {db: open, stopKeepalive: func() {}}}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	errs := make(chan error, 2)
	for range 2 {
		go func() { _, err := c.pool(ctx, "b"); errs <- err }()
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	if db, err := c.pool(context.Background(), "a"); err != nil || db != open {
		t.Fatalf("open pool: %v %v", db, err)
	}
	if time.Since(start) > 20*time.Millisecond {
		t.Fatal("an open pool waited for another database's ping")
	}
	for range 2 {
		if err := <-errs; err == nil {
			t.Fatal("unreachable database opened")
		}
	}
	if _, ok := c.pools["b"]; ok {
		t.Error("a failed pool was kept")
	}
}
