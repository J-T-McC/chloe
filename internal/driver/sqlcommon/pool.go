package sqlcommon

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"chloe/internal/model"
	"chloe/internal/sqlsplit"
)

// ConfigurePool applies the profile's session limit and starts the keepalive pinger.
// The returned function stops the pinger; call it before closing the pool.
func ConfigurePool(db *sql.DB, a model.Advanced) (stop func()) {
	if a.SessionLimit > 0 {
		db.SetMaxOpenConns(a.SessionLimit)
	}
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if a.KeepaliveSec <= 0 {
		return func() {}
	}
	// Idle pooled connections are kept for longer than the keepalive interval so
	// pinging actually keeps NAT/firewall state and server timeouts alive.
	db.SetConnMaxIdleTime(0)
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(time.Duration(a.KeepaliveSec) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				_ = db.PingContext(ctx)
				cancel()
			}
		}
	}()
	return func() { close(done) }
}

// RunInitialQueries executes the profile's "initial queries" on a new session.
func RunInitialQueries(ctx context.Context, q Querier, script string, d sqlsplit.Dialect) error {
	for _, st := range sqlsplit.Split(script, d) {
		if _, err := q.ExecContext(ctx, st.SQL); err != nil {
			return fmt.Errorf("initial query (line %d) failed: %w", st.StartLine, err)
		}
	}
	return nil
}

// DiscardConn closes a dedicated session connection without returning it to the
// pool. Sessions change server state (USE, SET search_path, SET variables, initial
// queries); a reused connection would carry that into unrelated work.
func DiscardConn(conn *sql.Conn) error {
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	return conn.Close()
}
