package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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

var (
	errDenied   = errors.New("declined by the user in Chloe")
	errNoAnswer = errors.New("nobody answered in Chloe before the request timed out, so nothing was done; check with the user before trying again")
)

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
	case errors.Is(err, errNoAnswer):
		c.act.Outcome = "timeout"
	case err != nil:
		c.act.Outcome, c.act.Error = "error", err.Error()
	case c.act.Outcome == "":
		c.act.Outcome = "ok"
	}
	c.s.record(c.act)
}

// add registers a tool whose handler runs as a logged call of one endpoint.
func add[In, Out any](s *Server, srv *mcp.Server, epID string, t *mcp.Tool, fn func(context.Context, *call, In) (Out, error)) {
	addRich(s, srv, epID, t, func(ctx context.Context, c *call, in In) (Out, []mcp.Content, error) {
		out, err := fn(ctx, c, in)
		return out, nil, err
	})
}

// addRich is add for tools that also return content such as images, after
// the output as JSON text.
func addRich[In, Out any](s *Server, srv *mcp.Server, epID string, t *mcp.Tool, fn func(context.Context, *call, In) (Out, []mcp.Content, error)) {
	mcp.AddTool(srv, t, func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		ep, err := s.endpoint(epID)
		if err != nil {
			return nil, zero, err
		}
		c := &call{s: s, ep: ep, start: time.Now(),
			act: Activity{ID: uuid.NewString(), Time: time.Now().UTC().Format(time.RFC3339), Endpoint: ep.Name, Tool: t.Name}}
		out, content, err := fn(ctx, c, in)
		c.finish(err)
		if err != nil {
			return nil, zero, err
		}
		c.stamp(&out)
		if len(content) == 0 {
			return nil, out, nil
		}
		text, err := json.Marshal(out)
		if err != nil {
			return nil, zero, err
		}
		return &mcp.CallToolResult{Content: append([]mcp.Content{&mcp.TextContent{Text: string(text)}}, content...)}, out, nil
	})
}

// stamp fills a write result's Approval field with how the write was cleared,
// so the agent can tell the user whether they approved it or danger mode let it
// through.
func (c *call) stamp(out any) {
	var note string
	switch c.act.Outcome {
	case "approved":
		note = "approved by the user in Chloe"
	case "unasked":
		note = "ran without asking (danger mode is on)"
	default:
		return
	}
	v := reflect.ValueOf(out).Elem()
	if v.Kind() == reflect.Struct {
		if f := v.FieldByName("Approval"); f.IsValid() && f.Kind() == reflect.String {
			f.SetString(note)
		}
	}
}

// approve asks the user unless the endpoint is in danger mode.
func (c *call) approve(ctx context.Context, req ApprovalRequest) error {
	if c.ep.Danger {
		c.act.Outcome = "unasked"
		return nil
	}
	req.Kind = "write"
	_, err := c.ask(ctx, req)
	return err
}

// ask shows a request to the user, whatever the endpoint's danger mode.
func (c *call) ask(ctx context.Context, req ApprovalRequest) (Answer, error) {
	req.ID, req.Endpoint, req.Tool = uuid.NewString(), c.ep.Name, c.act.Tool
	actx, cancel := context.WithTimeout(ctx, ApprovalTimeout)
	defer cancel()
	var a Answer
	if c.s.approver != nil {
		a = c.s.approver.Approve(actx, req)
	}
	if !a.OK {
		switch {
		case ctx.Err() != nil:
			return a, ctx.Err() // the agent went away
		case actx.Err() != nil:
			return a, errNoAnswer
		}
		return a, errDenied
	}
	c.act.Outcome, c.asked = "approved", true
	return a, nil
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

// access is an endpoint's effective access to one connection: its own level
// or its group's, plus per-database overrides.
type access struct {
	level store.MCPLevel
	dbs   map[string]store.MCPLevel
}

func accessTo(e store.MCPEndpoint, conn model.Connection) access {
	return access{level: e.ConnLevel(conn.ID, conn.Group), dbs: e.Access[conn.ID].Databases}
}

func (a access) db(database string) store.MCPLevel {
	if l, ok := a.dbs[database]; ok {
		return l
	}
	return a.level
}

// require checks that the endpoint has need on one database (key in the
// access list, name as the agent knows it) of the connection conn; see says
// where the agent can list the shared ones.
func (a access) require(conn, key, name, see string, need store.MCPLevel) error {
	have := a.db(key)
	if have == store.MCPOff {
		return fmt.Errorf("database %q of %s isn't shared with this endpoint (see %s)", name, conn, see)
	}
	if !atLeast(have, need) {
		return fmt.Errorf("this endpoint can only read %s/%s; ask the user to allow writes in Chloe's MCP Server settings", conn, name)
	}
	return nil
}

// visible reports whether the endpoint can see the connection at all.
func (a access) visible() bool {
	if a.level != store.MCPOff {
		return true
	}
	for _, l := range a.dbs {
		if l != store.MCPOff {
			return true
		}
	}
	return false
}

// hidesDatabases reports whether some database isn't readable.
func (a access) hidesDatabases() bool {
	if !atLeast(a.level, store.MCPRead) {
		return true
	}
	for _, l := range a.dbs {
		if l == store.MCPOff {
			return true
		}
	}
	return false
}

var (
	sqlEngines = []model.DBType{model.MySQL, model.MariaDB, model.PostgreSQL, model.SQLite}
	// mcpEngines are the engines list_connections shows.
	mcpEngines = append(slices.Clone(sqlEngines), model.Redis, model.MongoDB)
)

// target is a resolved connection/database the call may use.
type target struct {
	connID, conn string
	typ          model.DBType
	access       access
	database     string
	schema       string
}

func (t target) ref(name string, kind model.ObjectKind) model.ObjectRef {
	return model.ObjectRef{Database: t.database, Schema: t.schema, Name: name, Kind: kind}
}

// connection finds a SQL connection the endpoint can see.
func (c *call) connection(name string) (model.Connection, access, error) {
	return c.connectionOf(name, sqlEngines)
}

// connectionOf finds a connection of one of the engines that the endpoint can see.
func (c *call) connectionOf(name string, engines []model.DBType) (model.Connection, access, error) {
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
	var a access
	if len(match) == 1 {
		a = accessTo(c.ep, match[0])
	}
	switch {
	case len(match) > 1:
		return model.Connection{}, a, fmt.Errorf("several connections are named %q; pass the id from list_connections", name)
	case len(match) == 0 || !a.visible():
		return model.Connection{}, a, fmt.Errorf("no connection %q is shared with this endpoint (see list_connections)", name)
	}
	if typ := match[0].Type; !slices.Contains(engines, typ) {
		switch {
		case typ == model.Redis:
			return model.Connection{}, a, fmt.Errorf("%s is a Redis connection; use the redis_ tools", match[0].Name)
		case typ == model.MongoDB:
			return model.Connection{}, a, fmt.Errorf("%s is a MongoDB connection; use the mongo_ tools", match[0].Name)
		case slices.Contains(sqlEngines, typ):
			return model.Connection{}, a, fmt.Errorf("%s is a %s connection; use the SQL tools (list_tables, select_rows…)", match[0].Name, typ)
		}
		return model.Connection{}, a, fmt.Errorf("%s connections aren't available over MCP yet", typ)
	}
	return match[0], a, nil
}

// resolve finds the connection and database and checks the endpoint may use
// them at the needed level. It opens the connection if necessary.
func (c *call) resolve(ctx context.Context, connection, database, schema string, need store.MCPLevel) (target, error) {
	t, err := c.lookup(connection, database, schema, need)
	if err != nil {
		return t, err
	}
	if err := c.s.m.Ensure(ctx, t.connID); err != nil {
		return t, fmt.Errorf("couldn't open %s: %w", t.conn, err)
	}
	return t, nil
}

// lookup is resolve without opening the connection.
func (c *call) lookup(connection, database, schema string, need store.MCPLevel) (target, error) {
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
	return t, a.require(conn.Name, t.database, t.database, "list_databases", need)
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
	Group     string            `json:"group,omitempty"`
	Engine    string            `json:"engine"`
	Access    string            `json:"access" jsonschema:"read or write: the default for the connection's databases"`
	Databases map[string]string `json:"databases,omitempty" jsonschema:"per-database access that differs from the default (off, read, write); Redis databases are numbers"`
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
	Approval string           `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
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
	Approval string `json:"approval,omitempty" jsonschema:"how the change was cleared: approved by the user in Chloe, or ran without asking because danger mode is on"`
	Rows     int64  `json:"rows" jsonschema:"rows changed"`
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

// canWrite reports whether an endpoint has write access anywhere; endpoints
// that don't aren't offered the write tools at all.
func canWrite(e store.MCPEndpoint) bool {
	for _, l := range e.Groups {
		if l == store.MCPWrite {
			return true
		}
	}
	for _, a := range e.Access {
		if a.Level == store.MCPWrite {
			return true
		}
		for _, l := range a.Databases {
			if l == store.MCPWrite {
				return true
			}
		}
	}
	return false
}

func (s *Server) addTools(srv *mcp.Server, ep string, writes bool) {
	add(s, srv, ep, readOnlyTool("list_connections", "List connections",
		"Lists the database connections shared with this endpoint and what it may do with each."),
		func(ctx context.Context, c *call, _ NoArgs) (ConnectionsOut, error) {
			out := ConnectionsOut{Connections: []ConnectionOut{}}
			for _, conn := range c.s.m.Store.List() {
				a := accessTo(c.ep, conn)
				if !a.visible() || !slices.Contains(mcpEngines, conn.Type) {
					continue
				}
				o := ConnectionOut{ID: conn.ID, Name: conn.Name, Group: conn.Group, Engine: string(conn.Type), Access: levelName(a.level), Database: conn.InitialDatabase}
				for db, l := range a.dbs {
					if l != a.level {
						if o.Databases == nil {
							o.Databases = map[string]string{}
						}
						o.Databases[db] = levelName(l)
					}
				}
				switch conn.Type {
				case model.SQLite:
					o.Database = "main"
				case model.Redis:
					o.Database = ""
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
				if a.db(db) != store.MCPOff {
					out.Names = append(out.Names, db)
				}
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("list_schemas", "List schemas", "Lists the schemas of a PostgreSQL database."),
		toolListConnections)

	add(s, srv, ep, readOnlyTool("list_tables", "List tables", "Lists the tables and views of a database (PostgreSQL: of one schema), with estimated row counts."),
		toolListTables)

	add(s, srv, ep, readOnlyTool("describe_table", "Describe table", "Shows a table's or view's columns (type, nullable, default, primary key), indexes and foreign keys. "+
		"Check it before writing a query: filters on indexed columns stay fast on big tables, and the foreign keys give the joins."),
		toolDescribeTable)

	add(s, srv, ep, readOnlyTool("get_ddl", "Get DDL", "Returns the CREATE statement of a table, view, function, procedure, trigger or other object."),
		toolGetDdl)

	add(s, srv, ep, readOnlyTool("select_rows", "Select rows", "Reads rows from a table or view, with optional column list, conditions, ordering and paging."),
		toolSelectRows)

	add(s, srv, ep, readOnlyTool("run_query", "Run read-only query",
		"Runs one read-only SQL statement (SELECT, WITH, EXPLAIN, SHOW…) in a read-only transaction and returns its rows. "+
			"Filter and aggregate in the SQL: at most 1000 rows come back. Use EXPLAIN (MySQL and PostgreSQL also EXPLAIN ANALYZE; SQLite EXPLAIN QUERY PLAN) "+
			"to check that a query over a big table uses an index before running it or saving it. "+
			"Not available on connections where some databases are hidden from this endpoint (except PostgreSQL)."),
		toolRunQuery)

	if !writes {
		return
	}
	add(s, srv, ep, writeTool("insert_rows", "Insert rows", "Inserts rows into a table in one transaction. The user approves it in Chloe unless they've turned on danger mode. "+
		"Send all the rows in one call (up to 1000): each call is one approval.", false),
		toolInsertRows)

	add(s, srv, ep, writeTool("update_rows", "Update rows", "Changes columns of the rows matching the conditions to the same values, in one transaction. The user approves it in Chloe unless they've turned on danger mode. "+
		"Match on the primary key where you can; select_rows with the same conditions shows what will change.", true),
		toolUpdateRows)

	add(s, srv, ep, writeTool("delete_rows", "Delete rows", "Deletes the rows matching the conditions, in one transaction. The user approves it in Chloe unless they've turned on danger mode.", true),
		toolDeleteRows)
}

func toolListConnections(ctx context.Context, c *call, in DBArgs) (NamesOut, error) {
	t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
	if err != nil {
		return NamesOut{}, err
	}
	if t.typ != model.PostgreSQL {
		return NamesOut{}, errors.New("only PostgreSQL databases have schemas")
	}
	names, err := c.s.m.ListSchemas(ctx, t.connID, t.database)
	return NamesOut{Names: nonNil(names)}, err
}

func toolListTables(ctx context.Context, c *call, in DBArgs) (TablesOut, error) {
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
}

func toolDescribeTable(ctx context.Context, c *call, in TableArgs) (model.TableDetail, error) {
	t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
	if err != nil {
		return model.TableDetail{}, err
	}
	return c.s.m.TableDetail(ctx, t.connID, t.ref(in.Table, model.KindTable))
}

func toolGetDdl(ctx context.Context, c *call, in DDLArgs) (DDLOut, error) {
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
}

func toolSelectRows(ctx context.Context, c *call, in SelectArgs) (RowsOut, error) {
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
	d, err := c.s.m.Dialect(t.connID)
	if err != nil {
		return RowsOut{}, err
	}
	q, err := newBuilder(d, detail).selectRows(in.Columns, in.Where, in.OrderBy, limit+1, max(in.Offset, 0))
	if err != nil {
		return RowsOut{}, err
	}
	c.act.SQL = display(d, q)
	err = c.s.m.WithReadSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, _ driver.Dialect) error {
		out, err = readRows(ctx, ds, q, limit)
		return err
	})
	c.act.Rows = int64(out.RowCount)
	return out, err
}

func toolRunQuery(ctx context.Context, c *call, in QueryArgs) (RowsOut, error) {
	t, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
	if err != nil {
		return RowsOut{}, err
	}
	// MySQL and SQLite queries can name any database of the connection, so
	// raw SQL is only allowed when every database is readable.
	if t.typ != model.PostgreSQL {
		if t.access.hidesDatabases() {
			return RowsOut{}, errors.New("run_query isn't available here because some databases of this connection are hidden from this endpoint; use select_rows")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	limit := clampLimit(in.Limit)
	var out RowsOut
	d, err := c.s.m.Dialect(t.connID)
	if err != nil {
		return RowsOut{}, err
	}
	sql, err := checkReadOnly(d, in.SQL)
	c.act.SQL = strings.TrimSpace(in.SQL)
	if err != nil {
		return RowsOut{}, err
	}
	err = c.s.m.WithReadSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, _ driver.Dialect) error {
		// One row past the limit tells readRows the result was truncated.
		out, err = readRows(ctx, ds, stmt{SQL: limitRead(d, sql, limit+1)}, limit)
		return err
	})
	c.act.Rows = int64(out.RowCount)
	return out, err
}

func toolInsertRows(ctx context.Context, c *call, in InsertArgs) (InsertOut, error) {
	if len(in.Rows) == 0 {
		return InsertOut{}, errors.New("rows is empty")
	}
	if len(in.Rows) > maxInsertRows {
		return InsertOut{}, fmt.Errorf("insert at most %d rows per call", maxInsertRows)
	}
	var out InsertOut
	err := c.write(ctx, in.TableArgs, func(ctx context.Context, with sessionFn, d driver.Dialect, b *builder) (int64, error) {
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
		return n, with(func(ds driver.Session) error {
			return driver.InTx(ctx, ds, func() error {
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
	})
	return out, err
}

func toolUpdateRows(ctx context.Context, c *call, in UpdateArgs) (ChangedOut, error) {
	var out ChangedOut
	err := c.write(ctx, in.TableArgs, func(ctx context.Context, with sessionFn, d driver.Dialect, b *builder) (int64, error) {
		n, err := c.matchingThenRun(ctx, with, d, b, in.Where, in.Table, "Update", func(k *keySet) (stmt, error) {
			return b.update(in.Set, in.Where, k)
		})
		out.Rows = n
		return n, err
	})
	return out, err
}

func toolDeleteRows(ctx context.Context, c *call, in DeleteArgs) (ChangedOut, error) {
	var out ChangedOut
	err := c.write(ctx, in.TableArgs, func(ctx context.Context, with sessionFn, d driver.Dialect, b *builder) (int64, error) {
		n, err := c.matchingThenRun(ctx, with, d, b, in.Where, in.Table, "Delete", func(k *keySet) (stmt, error) {
			return b.delete(in.Where, k)
		})
		out.Rows = n
		return n, err
	})
	return out, err
}

// sessionFn runs fn on a fresh session of the table's database.
type sessionFn func(fn func(driver.Session) error) error

// write resolves a table for writing and runs fn with a builder for it. fn opens
// sessions through with as it needs them, so none is held while the user decides.
func (c *call) write(ctx context.Context, in TableArgs, fn func(context.Context, sessionFn, driver.Dialect, *builder) (int64, error)) error {
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
	d, err := c.s.m.Dialect(t.connID)
	if err != nil {
		return err
	}
	with := func(f func(driver.Session) error) error {
		return c.s.m.WithSession(ctx, t.connID, t.database, t.schema, func(ds driver.Session, _ driver.Dialect) error { return f(ds) })
	}
	n, err := fn(ctx, with, d, newBuilder(d, detail))
	c.act.Rows = n
	return err
}

// matchingThenRun finds the rows a change would touch, asks the user, then runs
// it on those rows only: pinned by primary key, so a row that starts matching
// while the user decides is left alone. Without a primary key, or with more than
// maxPinned rows, the matching rows are counted again in the transaction and the
// change is rolled back if the count moved.
func (c *call) matchingThenRun(ctx context.Context, with sessionFn, d driver.Dialect, b *builder, where []Condition, table, verb string, build func(*keySet) (stmt, error)) (int64, error) {
	q, err := build(nil)
	if err != nil {
		return 0, err
	}
	cq, err := b.count(where)
	if err != nil {
		return 0, err
	}
	var keys *keySet
	var matching int64
	cctx, cancel := context.WithTimeout(ctx, statementTimeout)
	err = with(func(ds driver.Session) error {
		var err error
		if keys, err = b.keys(cctx, ds, where); err != nil {
			return err
		}
		if keys != nil {
			matching = int64(len(keys.rows))
			return nil
		}
		v, err := scalar(cctx, ds, cq)
		matching, _ = strconv.ParseInt(v, 10, 64)
		return err
	})
	cancel()
	if err != nil {
		return 0, err
	}
	c.act.SQL = display(d, q) + keys.list()
	if err := c.approve(ctx, ApprovalRequest{Connection: c.act.Connection, Database: c.act.Database, Table: table,
		Summary: fmt.Sprintf("%s %s in %s", verb, plural(matching, "row"), table), SQL: c.act.SQL, Rows: matching}); err != nil {
		return 0, err
	}
	if keys != nil {
		if q, err = build(keys); err != nil {
			return 0, err
		}
	}
	ctx, cancel = context.WithTimeout(ctx, statementTimeout)
	defer cancel()
	var n int64
	err = with(func(ds driver.Session) error {
		return driver.InTx(ctx, ds, func() error {
			if keys == nil && c.asked {
				v, err := scalar(ctx, ds, cq)
				if err != nil {
					return err
				}
				if now, _ := strconv.ParseInt(v, 10, 64); now != matching {
					return fmt.Errorf("the conditions now match %s but %d were approved; rolled back (rows changed in the meantime)", plural(now, "row"), matching)
				}
			}
			var err error
			if n, err = ds.Exec(ctx, q.SQL, q.Args...); err != nil {
				return err
			}
			if c.asked && n > matching {
				return fmt.Errorf("the change would affect %d rows but %d were approved; rolled back (rows changed in the meantime)", n, matching)
			}
			return nil
		})
	})
	return n, err
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
