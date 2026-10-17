package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/model"
	"chloe/internal/store"
)

type ServerArgs struct {
	Connection string `json:"connection" jsonschema:"connection name or id from list_connections"`
}

type VarsArgs struct {
	ServerArgs
	Filter string `json:"filter,omitempty" jsonschema:"only entries whose name or description contains this text (case-insensitive); descriptions name the server's own counters, e.g. numbackends"`
}

type KillArgs struct {
	ServerArgs
	ID        string `json:"id" jsonschema:"session id from list_sessions"`
	QueryOnly bool   `json:"query_only,omitempty" jsonschema:"cancel the running query but keep the session"`
}

type SessionOut struct {
	ID          string `json:"id"`
	User        string `json:"user"`
	Host        string `json:"host,omitempty"`
	Database    string `json:"database,omitempty"`
	Application string `json:"application,omitempty"`
	Command     string `json:"command,omitempty"`
	State       string `json:"state,omitempty"`
	Seconds     int64  `json:"seconds" jsonschema:"time in the current state or query"`
	Query       string `json:"query,omitempty"`
	Chloe       bool   `json:"chloe,omitempty" jsonschema:"one of Chloe's own sessions"`
}

type SessionsOut struct {
	Sessions  []SessionOut `json:"sessions"`
	Truncated bool         `json:"truncated"`
}

type VarOut struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Section string `json:"section,omitempty" jsonschema:"Redis: the INFO section"`
}

type VarsOut struct {
	Values    []VarOut `json:"values"`
	Truncated bool     `json:"truncated" jsonschema:"more matched than were returned; use filter"`
}

type KilledOut struct {
	Approval string `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Killed   string `json:"killed"`
}

// server finds a connection for the monitor tools. Sessions and server status
// span every database, so the endpoint needs the level on all of them.
func (c *call) server(ctx context.Context, name string, need store.MCPLevel) (model.Connection, error) {
	conn, a, err := c.connectionOf(name, mcpEngines)
	if err != nil {
		return conn, err
	}
	c.act.Connection = conn.Name
	if conn.Type == model.SQLite {
		return conn, errors.New("SQLite connections have no server to monitor")
	}
	ok := atLeast(a.level, need)
	for _, l := range a.dbs {
		ok = ok && atLeast(l, need)
	}
	if !ok {
		what := "read"
		if need == store.MCPWrite {
			what = "read-write"
		}
		return conn, fmt.Errorf("the monitor tools need %s access to every database of %s, because sessions and server status cover them all", what, conn.Name)
	}
	if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
		return conn, fmt.Errorf("couldn't open %s: %w", conn.Name, err)
	}
	return conn, nil
}

func (s *Server) addMonitorTools(srv *mcp.Server, ep string, writes bool) {
	add(s, srv, ep, readOnlyTool("list_sessions", "List sessions",
		"Lists the server's sessions (MySQL processlist, PostgreSQL pg_stat_activity, Redis CLIENT LIST, MongoDB currentOp) with their state and running query."),
		func(ctx context.Context, c *call, in ServerArgs) (SessionsOut, error) {
			conn, err := c.server(ctx, in.Connection, store.MCPRead)
			if err != nil {
				return SessionsOut{}, err
			}
			ps, err := c.s.m.Processes(ctx, conn.ID)
			if err != nil {
				return SessionsOut{}, err
			}
			out := SessionsOut{Sessions: []SessionOut{}}
			for _, p := range ps {
				if len(out.Sessions) == maxLimit {
					out.Truncated = true
					break
				}
				out.Sessions = append(out.Sessions, SessionOut{ID: p.ID, User: p.User, Host: p.Host, Database: p.Database, Application: p.Application,
					Command: p.Command, State: p.State, Seconds: p.TimeSec, Query: cut(p.Query), Chloe: p.Self})
			}
			c.act.Rows = int64(len(out.Sessions))
			return out, nil
		})

	vars := func(name, title, desc string, get func(context.Context, string) ([]model.ServerVar, error)) {
		add(s, srv, ep, readOnlyTool(name, title, desc),
			func(ctx context.Context, c *call, in VarsArgs) (VarsOut, error) {
				conn, err := c.server(ctx, in.Connection, store.MCPRead)
				if err != nil {
					return VarsOut{}, err
				}
				vs, err := get(ctx, conn.ID)
				if err != nil {
					return VarsOut{}, err
				}
				out := VarsOut{Values: []VarOut{}}
				f := strings.ToLower(in.Filter)
				for _, v := range vs {
					// The description names the server's own counter (numbackends…), which matches too.
					if !strings.Contains(strings.ToLower(v.Name), f) && !strings.Contains(strings.ToLower(v.Description), f) {
						continue
					}
					if len(out.Values) == maxLimit {
						out.Truncated = true
						break
					}
					o := VarOut{Name: v.Name, Value: cut(v.Value)}
					if secretName.MatchString(v.Name) && o.Value != "" {
						o.Value = "(hidden)"
					}
					if conn.Type == model.Redis || conn.Type == model.MongoDB {
						o.Section = v.Description
					}
					out.Values = append(out.Values, o)
				}
				c.act.Rows = int64(len(out.Values))
				return out, nil
			})
	}
	vars("server_status", "Server status", "Returns the server's status counters (MySQL SHOW GLOBAL STATUS; PostgreSQL database and server statistics; Redis INFO and MongoDB serverStatus, with each entry's section).", s.m.ServerStatus)
	vars("server_variables", "Server variables", "Returns the server's configuration variables (MySQL SHOW VARIABLES, PostgreSQL pg_settings, Redis CONFIG GET, MongoDB getParameter; passwords are hidden).", s.m.Variables)

	if !writes {
		return
	}
	add(s, srv, ep, writeTool("kill_session", "Kill session",
		"Ends a session, or with query_only cancels its running query. The user approves it in Chloe unless they've turned on danger mode. "+
			"Chloe's own sessions can't be killed.", true),
		func(ctx context.Context, c *call, in KillArgs) (KilledOut, error) {
			conn, err := c.server(ctx, in.Connection, store.MCPWrite)
			if err != nil {
				return KilledOut{}, err
			}
			ps, err := c.s.m.Processes(ctx, conn.ID)
			if err != nil {
				return KilledOut{}, err
			}
			var p *model.Process
			for i := range ps {
				if ps[i].ID == in.ID {
					p = &ps[i]
				}
			}
			switch {
			case p == nil:
				return KilledOut{}, fmt.Errorf("no session %q on %s (see list_sessions)", in.ID, conn.Name)
			case p.Self:
				return KilledOut{}, fmt.Errorf("session %s is one of Chloe's own", in.ID)
			}
			verb, done := "Kill session "+p.ID, "session "+p.ID
			if in.QueryOnly {
				verb, done = "Cancel the query of session "+p.ID, "the query of session "+p.ID
			}
			c.act.SQL = describeSession(*p)
			if err := c.approve(ctx, ApprovalRequest{Connection: conn.Name, Database: p.Database, Summary: verb + " on " + conn.Name, SQL: c.act.SQL}); err != nil {
				return KilledOut{}, err
			}
			if conn.Type == model.Redis && in.QueryOnly {
				return KilledOut{}, errors.New("Redis can't cancel a client's command; kill the session instead")
			}
			if err := c.s.m.KillProcess(ctx, conn.ID, p.ID, in.QueryOnly); err != nil {
				return KilledOut{}, err
			}
			return KilledOut{Killed: done}, nil
		})
}

// secretName matches variables that may hold credentials (MongoDB
// getParameter has a few, e.g. ldapQueryPassword).
var secretName = regexp.MustCompile(`(?i)password|passwd|secret|passphrase`)

// describeSession lists a session for the approval dialog and the log.
func describeSession(p model.Process) string {
	lines := []string{"Session:  " + p.ID, "User:     " + p.User}
	for _, f := range [][2]string{{"Host:     ", p.Host}, {"Database: ", p.Database}, {"App:      ", p.Application}, {"State:    ", p.State}} {
		if f[1] != "" {
			lines = append(lines, f[0]+f[1])
		}
	}
	lines = append(lines, fmt.Sprintf("Time:     %ds", p.TimeSec))
	if p.Query != "" {
		lines = append(lines, "Query:    "+cut(p.Query))
	}
	return strings.Join(lines, "\n")
}

// cut shortens long text the way result cells are.
func cut(s string) string {
	if len(s) > maxCellBytes {
		return s[:maxCellBytes] + fmt.Sprintf("…[%d more bytes]", len(s)-maxCellBytes)
	}
	return s
}
