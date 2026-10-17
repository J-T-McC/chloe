package postgres

import (
	"context"
	"fmt"
	"strconv"

	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

func (c *Conn) OpenConnections() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, p := range c.pools {
		n += p.db.Stats().OpenConnections
	}
	return n
}

func (c *Conn) Processes(ctx context.Context) ([]model.Process, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, `SELECT pid, COALESCE(usename,''), COALESCE(host(client_addr) || ':' || client_port, ''),
		COALESCE(datname,''), COALESCE(application_name,''), COALESCE(backend_type,''), COALESCE(state,''),
		COALESCE(EXTRACT(EPOCH FROM now() - COALESCE(query_start, backend_start))::bigint, 0),
		COALESCE(query,''), COALESCE(wait_event_type || ': ' || wait_event, '')
		FROM pg_stat_activity ORDER BY backend_type <> 'client backend', pid`)
	if err != nil {
		return nil, err
	}
	out := make([]model.Process, 0, len(recs))
	for _, r := range recs {
		t, _ := strconv.ParseInt(r[7].String, 10, 64)
		state := r[6].String
		if w := r[9].String; w != "" && state == "active" {
			state += " (" + w + ")"
		}
		out = append(out, model.Process{
			ID: r[0].String, User: r[1].String, Host: r[2].String, Database: r[3].String,
			Application: r[4].String, Command: r[5].String, State: state, TimeSec: t, Query: r[8].String,
			Self: r[4].String == "Chloe",
		})
	}
	return out, nil
}

func (c *Conn) KillProcess(ctx context.Context, id string, queryOnly bool) error {
	pid, err := strconv.Atoi(id)
	if err != nil {
		return fmt.Errorf("invalid process id %q", id)
	}
	db, err := c.pool(ctx, "")
	if err != nil {
		return err
	}
	fn := "pg_terminate_backend"
	if queryOnly {
		fn = "pg_cancel_backend"
	}
	ok, err := c.signal(ctx, db, "", fn, int64(pid))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("process %d not found or not signalled", pid)
	}
	return nil
}

func (c *Conn) Variables(ctx context.Context) ([]model.ServerVar, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, `SELECT name, setting || COALESCE(' ' || unit, ''), short_desc FROM pg_settings ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := make([]model.ServerVar, len(recs))
	for i, r := range recs {
		out[i] = model.ServerVar{Name: r[0].String, Value: r[1].String, Description: r[2].String}
	}
	return out, nil
}

// Status reports server-wide activity counters.
func (c *Conn) Status(ctx context.Context) ([]model.ServerVar, error) {
	db, err := c.pool(ctx, "")
	if err != nil {
		return nil, err
	}
	recs, err := sqlcommon.QueryStrings(ctx, db, `
		SELECT 'uptime', date_trunc('second', now() - pg_postmaster_start_time())::text, 'Time since the server started (pg_postmaster_start_time)'
		UNION ALL SELECT 'connections', count(*)::text, 'Client backends (pg_stat_activity; like numbackends in pg_stat_database)' FROM pg_stat_activity WHERE backend_type = 'client backend'
		UNION ALL SELECT 'active_connections', count(*)::text, 'Client backends running a query (pg_stat_activity state active)' FROM pg_stat_activity WHERE state = 'active'
		UNION ALL SELECT 'max_connections', current_setting('max_connections'), 'Configured connection limit (max_connections)'
		UNION ALL SELECT 'xact_commit', sum(xact_commit)::text, 'Committed transactions, all databases (pg_stat_database.xact_commit)' FROM pg_stat_database
		UNION ALL SELECT 'xact_rollback', sum(xact_rollback)::text, 'Rolled back transactions (pg_stat_database.xact_rollback)' FROM pg_stat_database
		UNION ALL SELECT 'blks_hit', sum(blks_hit)::text, 'Buffer cache hits (pg_stat_database.blks_hit)' FROM pg_stat_database
		UNION ALL SELECT 'blks_read', sum(blks_read)::text, 'Blocks read from disk (pg_stat_database.blks_read)' FROM pg_stat_database
		UNION ALL SELECT 'cache_hit_ratio', round(100.0 * sum(blks_hit) / NULLIF(sum(blks_hit) + sum(blks_read), 0), 2)::text || ' %', 'Buffer cache hit ratio (blks_hit / (blks_hit + blks_read))' FROM pg_stat_database
		UNION ALL SELECT 'tup_returned', sum(tup_returned)::text, 'Rows returned (pg_stat_database.tup_returned)' FROM pg_stat_database
		UNION ALL SELECT 'tup_inserted', sum(tup_inserted)::text, 'Rows inserted (pg_stat_database.tup_inserted)' FROM pg_stat_database
		UNION ALL SELECT 'tup_updated', sum(tup_updated)::text, 'Rows updated (pg_stat_database.tup_updated)' FROM pg_stat_database
		UNION ALL SELECT 'tup_deleted', sum(tup_deleted)::text, 'Rows deleted (pg_stat_database.tup_deleted)' FROM pg_stat_database
		UNION ALL SELECT 'deadlocks', sum(deadlocks)::text, 'Deadlocks detected (pg_stat_database.deadlocks)' FROM pg_stat_database
		UNION ALL SELECT 'temp_bytes', pg_size_pretty(sum(temp_bytes)::bigint), 'Temporary file bytes written since the stats were reset (pg_stat_database.temp_bytes)' FROM pg_stat_database
		UNION ALL SELECT 'database_size', pg_size_pretty(sum(pg_database_size(datname))::bigint), 'Total size of all databases (pg_database_size)' FROM pg_database WHERE datallowconn`)
	if err != nil {
		return nil, err
	}
	out := make([]model.ServerVar, len(recs))
	for i, r := range recs {
		out[i] = model.ServerVar{Name: r[0].String, Value: r[1].String, Description: r[2].String}
	}
	return out, nil
}
