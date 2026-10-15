package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/driver"
	"chloe/internal/model"
	"chloe/internal/store"
)

// Limits on what one call returns.
const (
	defaultLimit     = 100
	maxLimit         = 1000
	maxResultBytes   = 256 << 10
	maxCellBytes     = 4 << 10
	maxInsertRows    = 1000
	statementTimeout = 30 * time.Second
)

var errDenied = errors.New("declined by the user in Chloe")

// call is one tool invocation: the endpoint it came through and its log entry.
type call struct {
	s     *Server
	ep    store.MCPEndpoint
	act   Activity
	start time.Time
	asked bool // the user approved this call in a dialog
}

func (c *call) finish(err error) {
	c.act.DurationMs = time.Since(c.start).Milliseconds()
	switch {
	case errors.Is(err, errDenied):
		c.act.Outcome = "denied"
	case err != nil:
		c.act.Outcome, c.act.Error = "error", err.Error()
	case c.act.Outcome == "":
		c.act.Outcome = "ok"
	}
	c.s.record(c.act)
}

// add registers a tool whose handler runs as a logged call of one endpoint.
func add[In, Out any](s *Server, srv *mcp.Server, epID string, t *mcp.Tool, fn func(context.Context, *call, In) (Out, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ep, err := s.endpoint(epID)
		if err != nil {
			return nil, zero, err
		}
		c := &call{s: s, ep: ep, start: time.Now(),
			act: Activity{ID: uuid.NewString(), Time: time.Now().UTC().Format(time.RFC3339), Endpoint: ep.Name, Tool: t.Name}}
		out, err := fn(ctx, c, in)
		c.finish(err)
		if err != nil {
			return nil, zero, err
		}
		return nil, out, nil
	})
}

// approve asks the user unless the endpoint is in danger mode.
func (c *call) approve(ctx context.Context, req ApprovalRequest) error {
	if c.ep.Danger {
		c.act.Outcome = "unasked"
		return nil
	}
	req.ID, req.Endpoint, req.Tool = uuid.NewString(), c.ep.Name, c.act.Tool
	actx, cancel := context.WithTimeout(ctx, ApprovalTimeout)
	defer cancel()
	if c.s.approver == nil || !c.s.approver.Approve(actx, req) {
		return errDenied
	}
	c.act.Outcome, c.asked = "approved", true
	return nil
}

// --- access -------------------------------------------------------------------

func atLeast(have, need store.MCPLevel) bool {
	switch need {
	case store.MCPRead:
		return have == store.MCPRead || have == store.MCPWrite
	case store.MCPWrite:
		return have == store.MCPWrite
	}
	return true
}

// dbLevel is the endpoint's access to one database of a connection.
func dbLevel(a store.MCPConnAccess, database string) store.MCPLevel {
	if l, ok := a.Databases[database]; ok {
		return l
	}
	return a.Level
}

// visible reports whether an endpoint can see a connection at all.
func visible(a store.MCPConnAccess) bool {
	if a.Level != store.MCPOff {
		return true
	}
	for _, l := range a.Databases {
		if l != store.MCPOff {
			return true
		}
	}
	return false
}

var sqlEngines = []model.DBType{model.MySQL, model.MariaDB, model.PostgreSQL, model.SQLite}

// target is a resolved connection/database the call may use.
type target struct {
	connID, conn string
	typ          model.DBType
	access       store.MCPConnAccess
	database     string
	schema       string
}

func (t target) ref(name string, kind model.ObjectKind) model.ObjectRef {
	return model.ObjectRef{Database: t.database, Schema: t.schema, Name: name, Kind: kind}
}

func (c *call) connection(name string) (model.Connection, store.MCPConnAccess, error) {
	var match []model.Connection
	for _, conn := range c.s.m.Store.List() {
		if conn.ID == name {
			match = []model.Connection{conn}
			break
		}
		if strings.EqualFold(conn.Name, name) {
			match = append(match, conn)
		}
	}
	a, ok := store.MCPConnAccess{}, false
	if len(match) == 1 {
		a, ok = c.ep.Access[match[0].ID]
	}
	switch {
	case len(match) > 1:
		return model.Connection{}, a, fmt.Errorf("several connections are named %q; pass the id from list_connections", name)
	case len(match) == 0 || !ok || !visible(a):
		return model.Connection{}, a, fmt.Errorf("no connection %q is shared with this endpoint (see list_connections)", name)
	}
	if !slices.Contains(sqlEngines, match[0].Type) {
		return model.Connection{}, a, fmt.Errorf("%s connections aren't available over MCP yet", match[0].Type)
	}
	return match[0], a, nil
}

// resolve finds the connection and database and checks the endpoint may use
// them at the needed level. It opens the connection if necessary.
func (c *call) resolve(ctx context.Context, connection, database, schema string, need store.MCPLevel) (target, error) {
	conn, a, err := c.connection(connection)
	if err != nil {
		return target{}, err
	}
	c.act.Connection = conn.Name
	t := target{connID: conn.ID, conn: conn.Name, typ: conn.Type, access: a, database: database, schema: schema}
	if t.database == "" {
		switch {
		case conn.Type == model.SQLite:
			t.database = "main"
		case conn.InitialDatabase != "":
			t.database = conn.InitialDatabase
		default:
			return t, errors.New("database is required (see list_databases)")
		}
	}
	c.act.Database = t.database
	if conn.Type == model.PostgreSQL && t.schema == "" {
		t.schema = "public"
	}
	if conn.Type != model.PostgreSQL {
		t.schema = ""
	}
	have := dbLevel(a, t.database)
	if have == store.MCPOff {
		return t, fmt.Errorf("database %q isn't shared with this endpoint", t.database)
	}
	if !atLeast(have, need) {
		return t, fmt.Errorf("this endpoint can only read %s/%s; ask the user to allow writes in Chloe's MCP Server settings", conn.Name, t.database)
	}
	if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
		return t, fmt.Errorf("couldn't open %s: %w", conn.Name, err)
	}
	return t, nil
}

// --- results ------------------------------------------------------------------

type ColumnOut struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type RowsOut struct {
	Columns   []ColumnOut `json:"columns"`
	Rows      [][]any     `json:"rows" jsonschema:"one array per row, cells as text in column order; null is SQL NULL, binary is 0x hex"`
	RowCount  int         `json:"row_count"`
	Truncated bool        `json:"truncated" jsonschema:"more rows exist than were returned; narrow the query or page with offset"`
}

// readRows reads at most limit rows of a statement, cutting long cells and
// stopping at the response size cap.
func readRows(ctx context.Context, ds driver.Session, q stmt, limit int) (RowsOut, error) {
	rows, err := ds.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return RowsOut{}, err
	}
	out := RowsOut{Rows: [][]any{}}
	for _, col := range rows.Columns() {
		out.Columns = append(out.Columns, ColumnOut{Name: col.Name, Type: col.Type})
	}
	cells := make([]*string, len(out.Columns))
	size := 0
	for rows.Next(cells) {
		if len(out.Rows) >= limit || size > maxResultBytes {
			out.Truncated = true
			// Stop the server sending the rest instead of draining it.
			_ = ds.Cancel(context.Background())
			break
		}
		row := make([]any, len(cells))
		for i, v := range cells {
			if v == nil {
				continue
			}
			s := *v
			if len(s) > maxCellBytes {
				s = s[:maxCellBytes] + fmt.Sprintf("…[%d more bytes]", len(s)-maxCellBytes)
			}
			row[i] = s
			size += len(s)
		}
		out.Rows = append(out.Rows, row)
	}
	err = rows.Err()
	rows.Close()
	if out.Truncated {
		err = nil // the cancel interrupts the statement on purpose
	}
	out.RowCount = len(out.Rows)
	return out, err
}

// readOnly runs fn inside a read-only transaction (SQLite: query_only).
func readOnly(ctx context.Context, ds driver.Session, d driver.Dialect, fn func() error) error {
	begin := map[string]string{"mysql": "START TRANSACTION READ ONLY", "postgres": "BEGIN READ ONLY", "sqlite": "PRAGMA query_only = ON"}[d.Name]
	if _, err := ds.Exec(ctx, begin); err != nil {
		return err
	}
	err := fn()
	if d.Name != "sqlite" {
		_, _ = ds.Exec(context.Background(), "ROLLBACK")
	}
	return err
}

func scalar(ctx context.Context, ds driver.Session, q stmt) (string, error) {
	rows, err := ds.Query(ctx, q.SQL, q.Args...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cell := make([]*string, len(rows.Columns()))
	if !rows.Next(cell) {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no result")
	}
	if len(cell) == 0 || cell[0] == nil {
		return "", nil
	}
	return *cell[0], nil
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	return min(n, maxLimit)
}

// --- tool inputs and outputs ----------------------------------------------------

type DBArgs struct {
	Connection string `json:"connection" jsonschema:"connection name or id from list_connections"`
	Database   string `json:"database,omitempty" jsonschema:"database name; SQLite uses main (and attached database names)"`
	Schema     string `json:"schema,omitempty" jsonschema:"PostgreSQL schema (default public)"`
}

type TableArgs struct {
	DBArgs
	Table string `json:"table"`
}

type ConnectionOut struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Engine    string            `json:"engine"`
	Access    string            `json:"access" jsonschema:"read or write: the default for the connection's databases"`
	Databases map[string]string `json:"databases,omitempty" jsonschema:"per-database access that differs from the default (off, read, write)"`
	Database  string            `json:"default_database,omitempty"`
}

type ConnectionsOut struct {
	Connections []ConnectionOut `json:"connections"`
}

type NamesOut struct {
	Names []string `json:"names"`
}

type TableOut struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Rows    int64  `json:"estimated_rows,omitempty"`
	Comment string `json:"comment,omitempty"`
}

type TablesOut struct {
	Tables []TableOut `json:"tables"`
}

type DDLArgs struct {
	DBArgs
	Name string `json:"name"`
	Kind string `json:"kind" jsonschema:"table, view, materialized_view, function, procedure, trigger, event, sequence, type or index"`
}

type DDLOut struct {
	DDL string `json:"ddl"`
}

type SelectArgs struct {
	TableArgs
	Columns []string    `json:"columns,omitempty" jsonschema:"columns to return (default all)"`
	Where   []Condition `json:"where,omitempty" jsonschema:"conditions, all of which must hold"`
	OrderBy []Order     `json:"order_by,omitempty"`
	Limit   int         `json:"limit,omitempty" jsonschema:"rows to return (default 100, at most 1000)"`
	Offset  int         `json:"offset,omitempty"`
}

type QueryArgs struct {
	DBArgs
	SQL   string `json:"sql" jsonschema:"one read statement: SELECT, WITH, EXPLAIN, SHOW, DESCRIBE, VALUES or TABLE"`
	Limit int    `json:"limit,omitempty" jsonschema:"rows to return (default 100, at most 1000)"`
}

type InsertArgs struct {
	TableArgs
	Rows []map[string]any `json:"rows" jsonschema:"rows to insert, as column: value objects; leave out columns that should get their default"`
}

type InsertOut struct {
	Inserted int64            `json:"inserted"`
	Keys     []map[string]any `json:"keys,omitempty" jsonschema:"primary key of each inserted row"`
}

type UpdateArgs struct {
	TableArgs
	Where []Condition    `json:"where" jsonschema:"conditions selecting the rows to change; required"`
	Set   map[string]any `json:"set" jsonschema:"column: new value"`
}

type DeleteArgs struct {
	TableArgs
	Where []Condition `json:"where" jsonschema:"conditions selecting the rows to delete; required"`
}

type ChangedOut struct {
	Rows int64 `json:"rows" jsonschema:"rows changed"`
}

type NoArgs struct{}

// --- tools --------------------------------------------------------------------

func readOnlyTool(name, title, desc string) *mcp.Tool {
	f := false
	return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: &f}}
}

func writeTool(name, title, desc string, destructive bool) *mcp.Tool {
	f := false
	return &mcp.Tool{Name: name, Description: desc, Annotations: &mcp.ToolAnnotations{Title: title, DestructiveHint: &destructive, OpenWorldHint: &f}}
}

func (s *Server) addTools(srv *mcp.Server, ep string) {
	add(s, srv, ep, readOnlyTool("list_connections", "List connections",
		"Lists the database connections shared with this endpoint and what it may do with each."),
		func(ctx context.Context, c *call, _ NoArgs) (ConnectionsOut, error) {
			out := ConnectionsOut{Connections: []ConnectionOut{}}
			for _, conn := range c.s.m.Store.List() {
				a, ok := c.ep.Access[conn.ID]
				if !ok || !visible(a) || !slices.Contains(sqlEngines, conn.Type) {
					continue
				}
				o := ConnectionOut{ID: conn.ID, Name: conn.Name, Engine: string(conn.Type), Access: levelName(a.Level), Database: conn.InitialDatabase}
				for db, l := range a.Databases {
					if l != a.Level {
						if o.Databases == nil {
							o.Databases = map[string]string{}
						}
						o.Databases[db] = levelName(l)
					}
				}
				if conn.Type == model.SQLite {
					o.Database = "main"
				}
				out.Connections = append(out.Connections, o)
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("list_databases", "List databases",
		"Lists the databases of a connection that this endpoint can see (SQLite: main and attached files)."),
		func(ctx context.Context, c *call, in struct {
			Connection string `json:"connection" jsonschema:"connection name or id from list_connections"`
		}) (NamesOut, error) {
			conn, a, err := c.connection(in.Connection)
			if err != nil {
				return NamesOut{}, err
			}
			c.act.Connection = conn.Name
			if err := c.s.m.Ensure(ctx, conn.ID); err != nil {
				return NamesOut{}, err
			}
			dbs, err := c.s.m.ListDatabases(ctx, conn.ID)
			if err != nil {
				return NamesOut{}, err
			}
			out := NamesOut{Names: []string{}}
			for _, db := range dbs {
				if dbLevel(a, db) != store.MCPOff {
					out.Names = append(out.Names, db)
				}
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("list_schemas", "List schemas", "Lists the schemas of a PostgreSQL database."),
		func(ctx context.Context, c *call, in DBArgs) (NamesOut, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return NamesOut{}, err
			}
			if t.typ != model.PostgreSQL {
				return NamesOut{}, errors.New("only PostgreSQL databases have schemas")
			}
			names, err := c.s.m.ListSchemas(ctx, t.connID, t.database)
			return NamesOut{Names: nonNil(names)}, err
		})

	add(s, srv, ep, readOnlyTool("list_tables", "List tables", "Lists the tables and views of a database (PostgreSQL: of one schema), with estimated row counts."),
		func(ctx context.Context, c *call, in DBArgs) (TablesOut, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return TablesOut{}, err
			}
			out := TablesOut{Tables: []TableOut{}}
			kinds := []model.ObjectKind{model.KindTable, model.KindView}
			if t.typ == model.PostgreSQL {
				kinds = append(kinds, model.KindMatView)
			}
			for _, k := range kinds {
				objs, err := c.s.m.ListObjects(ctx, t.connID, t.database, t.schema, k)
				if err != nil {
					return out, err
				}
				for _, o := range objs {
					out.Tables = append(out.Tables, TableOut{Name: o.Name, Kind: string(k), Rows: o.Rows, Comment: o.Comment})
				}
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("describe_table", "Describe table", "Shows a table's or view's columns (type, nullable, default, primary key), indexes and foreign keys."),
		func(ctx context.Context, c *call, in TableArgs) (model.TableDetail, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return model.TableDetail{}, err
			}
			return c.s.m.TableDetail(ctx, t.connID, t.ref(in.Table, model.KindTable))
		})

	add(s, srv, ep, readOnlyTool("get_ddl", "Get DDL", "Returns the CREATE statement of a table, view, function, procedure, trigger or other object."),
		func(ctx context.Context, c *call, in DDLArgs) (DDLOut, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return DDLOut{}, err
			}
			kind := model.ObjectKind(in.Kind)
			if !slices.Contains([]model.ObjectKind{model.KindTable, model.KindView, model.KindMatView, model.KindFunction, model.KindProcedure,
				model.KindTrigger, model.KindEvent, model.KindSequence, model.KindType, model.KindIndex}, kind) {
				return DDLOut{}, fmt.Errorf("unknown kind %q", in.Kind)
			}
			ddl, err := c.s.m.DDL(ctx, t.connID, t.ref(in.Name, kind))
			return DDLOut{DDL: ddl}, err
		})

	add(s, srv, ep, readOnlyTool("select_rows", "Select rows", "Reads rows from a table or view, with optional column list, conditions, ordering and paging."),
		func(ctx context.Context, c *call, in SelectArgs) (RowsOut, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return RowsOut{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			detail, err := c.s.m.TableDetail(ctx, t.connID, t.ref(in.Table, model.KindTable))
			if err != nil {
				return RowsOut{}, err
			}
			if len(detail.Columns) == 0 {
				return RowsOut{}, fmt.Errorf("no table or view %q", in.Table)
			}
			limit := clampLimit(in.Limit)
			var out RowsOut
			err = c.s.m.WithSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, d driver.Dialect) error {
				q, err := newBuilder(d, detail).selectRows(in.Columns, in.Where, in.OrderBy, limit+1, max(in.Offset, 0))
				if err != nil {
					return err
				}
				c.act.SQL = display(d, q)
				return readOnly(ctx, ds, d, func() error {
					out, err = readRows(ctx, ds, q, limit)
					return err
				})
			})
			c.act.Rows = int64(out.RowCount)
			return out, err
		})

	add(s, srv, ep, readOnlyTool("run_query", "Run read-only query",
		"Runs one read-only SQL statement (SELECT, WITH, EXPLAIN, SHOW…) in a read-only transaction and returns its rows. "+
			"Not available on connections where some databases are hidden from this endpoint (except PostgreSQL)."),
		func(ctx context.Context, c *call, in QueryArgs) (RowsOut, error) {
			t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return RowsOut{}, err
			}
			// MySQL and SQLite queries can name any database of the connection, so
			// raw SQL is only allowed when every database is readable.
			if t.typ != model.PostgreSQL {
				hidden := !atLeast(t.access.Level, store.MCPRead)
				for _, l := range t.access.Databases {
					hidden = hidden || l == store.MCPOff
				}
				if hidden {
					return RowsOut{}, errors.New("run_query isn't available here because some databases of this connection are hidden from this endpoint; use select_rows")
				}
			}
			ctx, cancel := context.WithTimeout(ctx, statementTimeout)
			defer cancel()
			limit := clampLimit(in.Limit)
			var out RowsOut
			err = c.s.m.WithSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, d driver.Dialect) error {
				sql, err := checkReadOnly(d, in.SQL)
				c.act.SQL = strings.TrimSpace(in.SQL)
				if err != nil {
					return err
				}
				return readOnly(ctx, ds, d, func() error {
					out, err = readRows(ctx, ds, stmt{SQL: sql}, limit)
					return err
				})
			})
			c.act.Rows = int64(out.RowCount)
			return out, err
		})

	add(s, srv, ep, writeTool("insert_rows", "Insert rows", "Inserts rows into a table in one transaction. The user approves it in Chloe unless they've turned on danger mode.", false),
		func(ctx context.Context, c *call, in InsertArgs) (InsertOut, error) {
			if len(in.Rows) == 0 {
				return InsertOut{}, errors.New("rows is empty")
			}
			if len(in.Rows) > maxInsertRows {
				return InsertOut{}, fmt.Errorf("insert at most %d rows per call", maxInsertRows)
			}
			var out InsertOut
			err := c.write(ctx, in.TableArgs, func(ctx context.Context, ds driver.Session, d driver.Dialect, b *builder) (int64, error) {
				stmts := make([]stmt, len(in.Rows))
				shown := make([]string, 0, 6)
				for i, row := range in.Rows {
					var err error
					if stmts[i], err = b.insert(row); err != nil {
						return 0, fmt.Errorf("row %d: %w", i+1, err)
					}
					if i < 5 {
						shown = append(shown, display(d, stmts[i])+";")
					}
				}
				if len(stmts) > 5 {
					shown = append(shown, fmt.Sprintf("-- … and %d more rows", len(stmts)-5))
				}
				n := int64(len(stmts))
				c.act.SQL = strings.Join(shown, "\n")
				if err := c.approve(ctx, ApprovalRequest{Connection: c.act.Connection, Database: c.act.Database, Table: in.Table,
					Summary: fmt.Sprintf("Insert %s into %s", plural(n, "row"), in.Table), SQL: c.act.SQL, Rows: n}); err != nil {
					return 0, err
				}
				ctx, cancel := context.WithTimeout(ctx, statementTimeout)
				defer cancel()
				return n, tx(ctx, ds, func() error {
					pk := b.primaryKey()
					for i, q := range stmts {
						key, err := insertOne(ctx, ds, d, b, q, in.Rows[i], pk)
						if err != nil {
							return fmt.Errorf("row %d: %w", i+1, err)
						}
						if key != nil {
							out.Keys = append(out.Keys, key)
						}
					}
					out.Inserted = n
					return nil
				})
			})
			return out, err
		})

	add(s, srv, ep, writeTool("update_rows", "Update rows", "Changes columns of the rows matching the conditions, in one transaction. The user approves it in Chloe unless they've turned on danger mode.", true),
		func(ctx context.Context, c *call, in UpdateArgs) (ChangedOut, error) {
			var out ChangedOut
			err := c.write(ctx, in.TableArgs, func(ctx context.Context, ds driver.Session, d driver.Dialect, b *builder) (int64, error) {
				q, err := b.update(in.Set, in.Where)
				if err != nil {
					return 0, err
				}
				n, err := c.matchingThenRun(ctx, ds, d, b, q, in.Where, in.Table, "Update")
				out.Rows = n
				return n, err
			})
			return out, err
		})

	add(s, srv, ep, writeTool("delete_rows", "Delete rows", "Deletes the rows matching the conditions, in one transaction. The user approves it in Chloe unless they've turned on danger mode.", true),
		func(ctx context.Context, c *call, in DeleteArgs) (ChangedOut, error) {
			var out ChangedOut
			err := c.write(ctx, in.TableArgs, func(ctx context.Context, ds driver.Session, d driver.Dialect, b *builder) (int64, error) {
				q, err := b.delete(in.Where)
				if err != nil {
					return 0, err
				}
				n, err := c.matchingThenRun(ctx, ds, d, b, q, in.Where, in.Table, "Delete")
				out.Rows = n
				return n, err
			})
			return out, err
		})
}

// write resolves a table for writing and runs fn on a session with a builder for it.
func (c *call) write(ctx context.Context, in TableArgs, fn func(context.Context, driver.Session, driver.Dialect, *builder) (int64, error)) error {
	t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPWrite)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, statementTimeout)
	detail, err := c.s.m.TableDetail(dctx, t.connID, t.ref(in.Table, model.KindTable))
	cancel()
	if err != nil {
		return err
	}
	if len(detail.Columns) == 0 {
		return fmt.Errorf("no table %q", in.Table)
	}
	return c.s.m.WithSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, d driver.Dialect) error {
		n, err := fn(ctx, ds, d, newBuilder(d, detail))
		c.act.Rows = n
		return err
	})
}

// matchingThenRun counts the rows a change would touch, asks the user, then
// runs it. A change touching more rows than were approved is rolled back.
func (c *call) matchingThenRun(ctx context.Context, ds driver.Session, d driver.Dialect, b *builder, q stmt, where []Condition, table, verb string) (int64, error) {
	cq, err := b.count(where)
	if err != nil {
		return 0, err
	}
	cctx, cancel := context.WithTimeout(ctx, statementTimeout)
	v, err := scalar(cctx, ds, cq)
	cancel()
	if err != nil {
		return 0, err
	}
	matching, _ := strconv.ParseInt(v, 10, 64)
	c.act.SQL = display(d, q)
	if err := c.approve(ctx, ApprovalRequest{Connection: c.act.Connection, Database: c.act.Database, Table: table,
		Summary: fmt.Sprintf("%s %s in %s", verb, plural(matching, "row"), table), SQL: c.act.SQL, Rows: matching}); err != nil {
		return 0, err
	}
	ctx, cancel = context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	var n int64
	err = tx(ctx, ds, func() error {
		var err error
		if n, err = ds.Exec(ctx, q.SQL, q.Args...); err != nil {
			return err
		}
		if c.asked && n > matching {
			return fmt.Errorf("the change would affect %d rows but %d were approved; rolled back (rows changed in the meantime)", n, matching)
		}
		return nil
	})
	return n, err
}

// tx runs fn in a transaction, rolling back when it fails.
func tx(ctx context.Context, ds driver.Session, fn func() error) error {
	if _, err := ds.Exec(ctx, "BEGIN"); err != nil {
		return err
	}
	if err := fn(); err != nil {
		_, _ = ds.Exec(context.Background(), "ROLLBACK")
		return err
	}
	_, err := ds.Exec(ctx, "COMMIT")
	return err
}

// insertOne runs one INSERT and returns the new row's primary key: from
// RETURNING (PostgreSQL, SQLite) or LAST_INSERT_ID() (MySQL auto increment).
func insertOne(ctx context.Context, ds driver.Session, d driver.Dialect, b *builder, q stmt, row map[string]any, pk []string) (map[string]any, error) {
	if len(pk) == 0 {
		_, err := ds.Exec(ctx, q.SQL, q.Args...)
		return nil, err
	}
	if d.Name != "mysql" {
		rows, err := ds.Query(ctx, q.SQL, q.Args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		cells := make([]*string, len(pk))
		if !rows.Next(cells) {
			return nil, rows.Err()
		}
		key := map[string]any{}
		for i, c := range pk {
			if cells[i] != nil {
				key[c] = *cells[i]
			}
		}
		return key, nil
	}
	if _, err := ds.Exec(ctx, q.SQL, q.Args...); err != nil {
		return nil, err
	}
	key := map[string]any{}
	for _, c := range pk {
		if v, ok := row[c]; ok {
			key[c] = v
		} else if b.cols[c].AutoIncrement {
			id, err := scalar(ctx, ds, stmt{SQL: "SELECT LAST_INSERT_ID()"})
			if err != nil {
				return nil, err
			}
			key[c] = id
		}
	}
	return key, nil
}

func levelName(l store.MCPLevel) string {
	if l == store.MCPOff {
		return "off"
	}
	return string(l)
}

func plural(n int64, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}
