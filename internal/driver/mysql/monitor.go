package mysql

import (
	"context"
	"fmt"
	"strconv"

	"chloe/internal/driver/sqlcommon"
	"chloe/internal/model"
)

func (c *Conn) OpenConnections() int { return c.db.Stats().OpenConnections }

func (c *Conn) Processes(ctx context.Context) ([]model.Process, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, "SHOW FULL PROCESSLIST")
	if err != nil {
		return nil, err
	}
	// Chloe tags its connections with program_name=Chloe; reading the tags needs
	// performance_schema access, so without it nothing is flagged.
	mine := map[string]bool{}
	if ids, err := sqlcommon.QueryStrings(ctx, c.db, `SELECT PROCESSLIST_ID FROM performance_schema.session_connect_attrs
		WHERE ATTR_NAME = 'program_name' AND ATTR_VALUE = 'Chloe'`); err == nil {
		for _, r := range ids {
			mine[r[0].String] = true
		}
	}
	out := make([]model.Process, 0, len(recs))
	for _, r := range recs {
		// Id, User, Host, db, Command, Time, State, Info
		t, _ := strconv.ParseInt(r[5].String, 10, 64)
		p := model.Process{
			ID: r[0].String, User: r[1].String, Host: r[2].String, Database: r[3].String,
			Command: r[4].String, TimeSec: t, State: r[6].String, Query: r[7].String,
		}
		p.Self = mine[p.ID]
		out = append(out, p)
	}
	return out, nil
}

func (c *Conn) KillProcess(ctx context.Context, id string, queryOnly bool) error {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid process id %q", id)
	}
	stmt := "KILL "
	if queryOnly {
		stmt += "QUERY "
	}
	return c.kill(ctx, stmt+strconv.FormatInt(n, 10))
}

func (c *Conn) Variables(ctx context.Context) ([]model.ServerVar, error) {
	return nameValues(ctx, c, "SHOW GLOBAL VARIABLES")
}

func (c *Conn) Status(ctx context.Context) ([]model.ServerVar, error) {
	return nameValues(ctx, c, "SHOW GLOBAL STATUS")
}

func nameValues(ctx context.Context, c *Conn, q string) ([]model.ServerVar, error) {
	recs, err := sqlcommon.QueryStrings(ctx, c.db, q)
	if err != nil {
		return nil, err
	}
	out := make([]model.ServerVar, len(recs))
	for i, r := range recs {
		out[i] = model.ServerVar{Name: r[0].String, Value: r[1].String}
	}
	return out, nil
}
