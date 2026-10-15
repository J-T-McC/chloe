package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/bi"
	"chloe/internal/model"
	"chloe/internal/store"
)

type WorkspaceArgs struct {
	Workspace string `json:"workspace" jsonschema:"a workspace name or path from list_workspaces"`
}

type CreateWorkspaceArgs struct {
	Name string `json:"name"`
}

type FilterArg struct {
	Field string `json:"field"`
	Op    string `json:"op" jsonschema:"one of = != < <= > >= contains not_contains begins_with not_begins_with ends_with not_ends_with is_null is_not_null is_empty is_not_empty between not_between in not_in in_last"`
	Value any    `json:"value,omitempty" jsonschema:"the value; two values for between, a list for in, [n, unit] for in_last (unit day week month or year: the last n units including the current one, so charts stay current), nothing for is_null/is_empty"`
	// Values is how get_workspace returns filters, accepted as well.
	Values []any `json:"values,omitempty" jsonschema:"the values as a list, as get_workspace returns them (instead of value). Values are kept as text, the way they're bound"`
}

type CreateSourceArgs struct {
	WorkspaceArgs
	Name       string      `json:"name"`
	Connection string      `json:"connection"`
	Database   string      `json:"database,omitempty"`
	Schema     string      `json:"schema,omitempty" jsonschema:"PostgreSQL schema (default public)"`
	Table      string      `json:"table,omitempty" jsonschema:"a table or view to read; or pass sql"`
	SQL        string      `json:"sql,omitempty" jsonschema:"one read-only query (SELECT, WITH, VALUES, TABLE); it always runs in a read-only transaction"`
	Mode       string      `json:"mode,omitempty" jsonschema:"live (read each time a chart is drawn, the default) or archive (read once into a snapshot)"`
	Timezone   string      `json:"timezone,omitempty" jsonschema:"IANA time zone (e.g. America/Los_Angeles) to bucket and filter date/time fields in; values without a zone are taken as UTC. MySQL needs its time zone tables loaded"`
	Filters    []FilterArg `json:"filters,omitempty" jsonschema:"conditions on the source's rows, joined with AND"`
}

type FieldArg struct {
	Field     string `json:"field"`
	Aggregate string `json:"aggregate,omitempty" jsonschema:"for value fields: sum avg count count_distinct min max, median on PostgreSQL live sources; default sum for numbers, count otherwise. Table columns may leave it empty to group by the column, or use a bucket to group a date column. Stored as aggregate whichever name you send"`
	Bucket    string `json:"bucket,omitempty" jsonschema:"for date/time dimensions and series: year quarter month week day hour minute second, or quarter_x month_x week_x day_x weekday_x hour_x minute_x second_x for one part (all Marches together; weekdays and months come back as Sun… and Jan…); default month. get_workspace returns it as aggregate, which is accepted too"`
	Alias     string `json:"alias,omitempty" jsonschema:"label shown in the chart"`
	Operation string `json:"operation,omitempty" jsonschema:"cumulative (running total) or subtract (change from the previous value)"`
	// Op is how get_workspace returns the operation, accepted as well.
	Op string `json:"op,omitempty" jsonschema:"same as operation (get_workspace's name for it)"`
}

type SortArg struct {
	By    string `json:"by" jsonschema:"dimension, value, or the name or label of one of the chart's values (same as value with field)"`
	Field string `json:"field,omitempty" jsonschema:"with by value: which value field (name or label) to sort by; tables otherwise use their first aggregated column"`
	Desc  bool   `json:"desc,omitempty"`
}

type ChartSpec struct {
	Type       string         `json:"type,omitempty"`
	Dimension  []FieldArg     `json:"dimension,omitempty" jsonschema:"the category or X axis"`
	Series     []FieldArg     `json:"series,omitempty" jsonschema:"splits values into coloured series or stacks"`
	Value      []FieldArg     `json:"value,omitempty" jsonschema:"measures (table: its columns)"`
	Secondary  []FieldArg     `json:"secondary,omitempty" jsonschema:"bar and line charts: the line values"`
	Filters    []FilterArg    `json:"filters,omitempty" jsonschema:"conditions on the source's rows for this chart, joined with AND"`
	Sort       *SortArg       `json:"sort,omitempty" jsonschema:"default: dates and numbers in order, categories by value descending"`
	Top        *int           `json:"top,omitempty" jsonschema:"keep the first n dimension values"`
	Others     *bool          `json:"others,omitempty" jsonschema:"with top: add the rest up as Others"`
	Properties map[string]any `json:"properties,omitempty" jsonschema:"display settings: title, hideTitle, legend (auto top bottom right none), labels, labelPercent, stack100, smooth, number {prefix suffix unit(auto K M B) decimals noSeparator}, dateFormat (YYYY MMM DD…), palette, colors (hex list), xTitle, yTitle. Display only: get_chart_data returns unformatted numbers"`
	// Props is how get_workspace returns properties, accepted as well.
	Props map[string]any `json:"props,omitempty" jsonschema:"same as properties (get_workspace's name for them)"`
}

type CreateChartArgs struct {
	WorkspaceArgs
	Name       string `json:"name"`
	DataSource string `json:"data_source" jsonschema:"a data source name or id in the workspace"`
	ChartSpec
}

type EditChartArgs struct {
	WorkspaceArgs
	Chart  string `json:"chart" jsonschema:"a chart name or id in the workspace"`
	Rename string `json:"rename,omitempty"`
	ChartSpec
}

type ChartArgs struct {
	WorkspaceArgs
	Chart string `json:"chart" jsonschema:"a chart name or id in the workspace"`
}

type EditSourceArgs struct {
	WorkspaceArgs
	DataSource string      `json:"data_source" jsonschema:"a data source name or id in the workspace"`
	Rename     string      `json:"rename,omitempty"`
	Table      string      `json:"table,omitempty" jsonschema:"read this table or view instead"`
	SQL        string      `json:"sql,omitempty" jsonschema:"read this query instead"`
	Mode       string      `json:"mode,omitempty" jsonschema:"live or archive"`
	Timezone   *string     `json:"timezone,omitempty" jsonschema:"IANA time zone for date/time fields; empty string to clear"`
	Filters    []FilterArg `json:"filters,omitempty" jsonschema:"replaces the source's filters"`
}

type SourceArgs struct {
	WorkspaceArgs
	DataSource string `json:"data_source" jsonschema:"a data source name or id in the workspace"`
}

type DeletedOut struct {
	Deleted string `json:"deleted"`
}

type ChartDataArgs struct {
	ChartArgs
	IncludeSQL bool `json:"include_sql,omitempty" jsonschema:"also return the query Chloe built (it repeats the data source's query)"`
}

type WorkspaceInfo struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	DataSources int    `json:"data_source_count"`
	Charts      int    `json:"chart_count"`
	CreatedBy   string `json:"created_by,omitempty"`
	Error       string `json:"error,omitempty"`
}

type WorkspacesOut struct {
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

type SourceOut struct {
	bi.DataSource
	Connection string `json:"connection" jsonschema:"the connection's name"`
	Editable   bool   `json:"editable"`
}

type ChartOut struct {
	bi.Chart
	Editable bool `json:"editable" jsonschema:"created over MCP, so edit_chart may change it"`
}

type WorkspaceOut struct {
	WorkspaceInfo
	Theme       string      `json:"theme,omitempty"`
	Sources     []SourceOut `json:"data_sources"`
	ChartList   []ChartOut  `json:"charts"`
}

type ChartDataOut struct {
	Columns   []bi.Column `json:"columns"`
	Rows      [][]any     `json:"rows" jsonschema:"one array per group in column order: dimension and series values as text, values as numbers, null for NULL"`
	RowCount  int         `json:"row_count"`
	Truncated bool        `json:"truncated"`
	Complete  bool        `json:"complete" jsonschema:"false while a shelf the chart type needs is empty"`
	Mode      string      `json:"mode" jsonschema:"live or archive"`
	AsOf      string      `json:"as_of" jsonschema:"when the rows were read (archive: when the snapshot was taken)"`
	SQL       string      `json:"sql,omitempty" jsonschema:"the query Chloe built, with include_sql"`
}

func chartTypesText() string {
	var parts []string
	for _, k := range bi.TypeNames() {
		t := bi.ChartTypes[k]
		var sh []string
		for _, s := range []struct {
			n string
			l bi.Shelf
		}{{"dimension", t.Dimension}, {"series", t.Series}, {"value", t.Value}, {"secondary", t.Secondary}} {
			switch {
			case s.l.Max == 0:
			case s.l.Min == s.l.Max:
				sh = append(sh, fmt.Sprintf("%s %d", s.n, s.l.Max))
			default:
				sh = append(sh, fmt.Sprintf("%s %d-%d", s.n, s.l.Min, s.l.Max))
			}
		}
		parts = append(parts, k+" ("+strings.Join(sh, ", ")+")")
	}
	return strings.Join(parts, "; ")
}

func (s *Server) addBITools(srv *mcp.Server, ep string) {
	f := false
	t := true
	add(s, srv, ep, readOnlyTool("list_workspaces", "List BI workspaces",
		"Lists the BI workspaces in Chloe's library: data sources and the charts built on them. You can read all of them, and edit the charts created over MCP."),
		func(ctx context.Context, c *call, _ NoArgs) (WorkspacesOut, error) {
			out := WorkspacesOut{Workspaces: []WorkspaceInfo{}}
			for _, e := range c.s.m.Store.BI().List() {
				out.Workspaces = append(out.Workspaces, WorkspaceInfo{Path: e.Path, Name: e.Name, DataSources: e.DataSources, Charts: e.Charts, CreatedBy: e.CreatedBy, Error: e.Error})
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("get_workspace", "Get BI workspace", "Returns a workspace's data sources (connection, table or query, mode, fields, filters) and charts (type, shelves, properties). Definitions only; get_chart_data reads data."),
		func(ctx context.Context, c *call, in WorkspaceArgs) (WorkspaceOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return WorkspaceOut{}, err
			}
			out := WorkspaceOut{WorkspaceInfo: workspaceInfo(p, w), Theme: w.Theme, Sources: []SourceOut{}, ChartList: []ChartOut{}}
			hidden := map[string]bool{}
			for _, d := range w.DataSources {
				conn, err := c.s.m.Store.Get(d.ConnID)
				if err != nil || !c.readable(d) {
					// Only the name of a source on a connection this endpoint can't read.
					hidden[d.ID] = true
					out.Sources = append(out.Sources, SourceOut{DataSource: bi.DataSource{Meta: bi.Meta{ID: d.ID, Name: d.Name}, Mode: d.Mode}, Connection: "(not shared with this endpoint)"})
					continue
				}
				out.Sources = append(out.Sources, SourceOut{DataSource: d, Connection: conn.Name, Editable: agentMade(d.CreatedBy)})
			}
			for _, ch := range w.Charts {
				if hidden[ch.DataSource] {
					ch = bi.Chart{Meta: bi.Meta{ID: ch.ID, Name: ch.Name}, DataSource: ch.DataSource, Type: ch.Type}
				}
				out.ChartList = append(out.ChartList, ChartOut{Chart: ch, Editable: agentMade(ch.CreatedBy) && !hidden[ch.DataSource]})
			}
			return out, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "create_workspace", Description: "Creates an empty BI workspace in Chloe's library, for data sources and charts.",
		Annotations: &mcp.ToolAnnotations{Title: "Create BI workspace", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in CreateWorkspaceArgs) (WorkspaceInfo, error) {
			name := strings.TrimSpace(in.Name)
			if name == "" {
				return WorkspaceInfo{}, errors.New("name is required")
			}
			w := bi.New(name)
			w.CreatedBy = "mcp:" + c.ep.Name
			p, err := c.s.m.Store.BI().Create(w)
			if err != nil {
				return WorkspaceInfo{}, err
			}
			c.act.SQL = p
			c.biChanged(p)
			return workspaceInfo(p, w), nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "create_data_source", Description: "Adds a data source to a workspace: a table or view, or one read-only query, on a connection shared with you. " +
		"Charts aggregate its rows. It always runs read-only; archive mode reads the rows once into a snapshot kept (encrypted) with the workspace.",
		Annotations: &mcp.ToolAnnotations{Title: "Create data source", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in CreateSourceArgs) (SourceOut, error) {
			p, _, err := c.workspace(in.Workspace)
			if err != nil {
				return SourceOut{}, err
			}
			tg, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return SourceOut{}, err
			}
			src := bi.DataSource{Meta: bi.Meta{Name: in.Name, CreatedBy: "mcp:" + c.ep.Name}, ConnID: tg.connID, Database: tg.database, Schema: tg.schema,
				Table: in.Table, SQL: in.SQL, Mode: in.Mode, Timezone: in.Timezone}
			if err := c.checkSource(tg, src); err != nil {
				return SourceOut{}, err
			}
			if src.Filters, err = filters(in.Filters); err != nil {
				return SourceOut{}, err
			}
			c.act.SQL = in.SQL
			if in.Table != "" {
				c.act.SQL = in.Table
			}
			saved, err := c.s.m.BISaveSource(ctx, p, src)
			if err != nil {
				return SourceOut{}, err
			}
			c.biChanged(p)
			return SourceOut{DataSource: saved, Connection: tg.conn, Editable: true}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "edit_data_source", Description: "Changes a data source created over MCP: its table or query, mode, time zone, filters (each replaced when given), or its name. " +
		"Charts built on it keep working while the fields they use remain. The user's own data sources can't be changed.",
		Annotations: &mcp.ToolAnnotations{Title: "Edit data source", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in EditSourceArgs) (SourceOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return SourceOut{}, err
			}
			found := findSource(&w, in.DataSource)
			if found == nil {
				return SourceOut{}, fmt.Errorf("no data source %q in the workspace (see get_workspace)", in.DataSource)
			}
			if !agentMade(found.CreatedBy) {
				return SourceOut{}, errors.New("this is one of the user's data sources; agents can't change them, so create one of your own")
			}
			src := *found
			if in.Table != "" && in.SQL != "" {
				return SourceOut{}, errors.New("pass table or sql, not both")
			}
			if in.Table != "" {
				src.Table, src.SQL = in.Table, ""
			}
			if in.SQL != "" {
				src.Table, src.SQL = "", in.SQL
			}
			if r := strings.TrimSpace(in.Rename); r != "" {
				src.Name = r
			}
			if in.Mode != "" {
				src.Mode = in.Mode
			}
			if in.Timezone != nil {
				src.Timezone = *in.Timezone
			}
			if in.Filters != nil {
				if src.Filters, err = filters(in.Filters); err != nil {
					return SourceOut{}, err
				}
			}
			tg, err := c.resolve(ctx, src.ConnID, src.Database, src.Schema, store.MCPRead)
			if err != nil {
				return SourceOut{}, err
			}
			if err := c.checkSource(tg, src); err != nil {
				return SourceOut{}, err
			}
			c.act.SQL = src.SQL
			saved, err := c.s.m.BISaveSource(ctx, p, src)
			if err != nil {
				return SourceOut{}, err
			}
			c.biChanged(p)
			return SourceOut{DataSource: saved, Connection: tg.conn, Editable: true}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "create_chart", Description: "Adds a chart on one of the workspace's data sources. Chloe builds its query from the fields you place: " +
		"dimension (categories or X axis; date fields are grouped by bucket), series (splits into colours or stacks), value (aggregated measures), secondary (bar and line charts' lines). " +
		"Types and their shelves: " + chartTypesText() + ". The chart is saved in the workspace for the user to open; get_chart_data returns what it shows.",
		Annotations: &mcp.ToolAnnotations{Title: "Create chart", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in CreateChartArgs) (ChartOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return ChartOut{}, err
			}
			src := findSource(&w, in.DataSource)
			if src == nil {
				return ChartOut{}, fmt.Errorf("no data source %q in the workspace (see get_workspace)", in.DataSource)
			}
			if !c.readable(*src) {
				return ChartOut{}, fmt.Errorf("data source %q reads a connection that isn't shared with this endpoint", src.Name)
			}
			ch := bi.Chart{Meta: bi.Meta{Name: in.Name, CreatedBy: "mcp:" + c.ep.Name}, DataSource: src.ID, Type: "bar"}
			if err := c.applySpec(&ch, *src, in.ChartSpec); err != nil {
				return ChartOut{}, err
			}
			saved, err := c.putChart(p, ch)
			return ChartOut{Chart: saved, Editable: true}, err
		})

	add(s, srv, ep, &mcp.Tool{Name: "edit_chart", Description: "Changes a chart created over MCP: its type, shelves, filters, sort, top and properties (each replaced when given), or its name. " +
		"The user's own charts can't be changed; create a chart of your own instead.",
		Annotations: &mcp.ToolAnnotations{Title: "Edit chart", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in EditChartArgs) (ChartOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return ChartOut{}, err
			}
			ch := findChart(&w, in.Chart)
			if ch == nil {
				return ChartOut{}, fmt.Errorf("no chart %q in the workspace (see get_workspace)", in.Chart)
			}
			if !agentMade(ch.CreatedBy) {
				return ChartOut{}, errors.New("this is one of the user's charts; agents can't change them, so create a chart of your own")
			}
			src := w.Source(ch.DataSource)
			if src == nil {
				return ChartOut{}, errors.New("the chart's data source is no longer in the workspace")
			}
			if !c.readable(*src) {
				return ChartOut{}, fmt.Errorf("data source %q reads a connection that isn't shared with this endpoint", src.Name)
			}
			edited := *ch
			if r := strings.TrimSpace(in.Rename); r != "" {
				edited.Name = r
			}
			if err := c.applySpec(&edited, *src, in.ChartSpec); err != nil {
				return ChartOut{}, err
			}
			saved, err := c.putChart(p, edited)
			return ChartOut{Chart: saved, Editable: true}, err
		})

	add(s, srv, ep, readOnlyTool("get_chart_data", "Get chart data",
		"Reads what a chart shows: one row per dimension (and series) value with its aggregated values, in the chart's order, as the user sees it in Chloe. "+
			"Values are unformatted numbers (the chart's number format is display only); periods with no rows on a date axis are included, with 0 for sums and counts."),
		func(ctx context.Context, c *call, in ChartDataArgs) (ChartDataOut, error) {
			_, w, err := c.workspace(in.Workspace)
			if err != nil {
				return ChartDataOut{}, err
			}
			ch := findChart(&w, in.Chart)
			if ch == nil {
				return ChartDataOut{}, fmt.Errorf("no chart %q in the workspace (see get_workspace)", in.Chart)
			}
			src := w.Source(ch.DataSource)
			if src == nil {
				return ChartDataOut{}, errors.New("the chart's data source is no longer in the workspace")
			}
			if err := c.canRead(ctx, *src); err != nil {
				return ChartDataOut{}, err
			}
			data, err := c.s.m.BIChartData(ctx, w, *ch)
			c.act.SQL = data.SQL
			c.act.Rows = int64(len(data.Rows))
			if err != nil {
				return ChartDataOut{}, err
			}
			out := ChartDataOut{Columns: data.Columns, Rows: data.Rows, RowCount: len(data.Rows), Truncated: data.Truncated, Complete: data.Complete, Mode: data.Mode, AsOf: data.RanAt}
			if in.IncludeSQL {
				out.SQL = data.SQL
			}
			return out, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "delete_chart", Description: "Deletes a chart created over MCP (scratch work, a chart you replaced). The user's own charts can't be deleted.",
		Annotations: &mcp.ToolAnnotations{Title: "Delete chart", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in ChartArgs) (DeletedOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DeletedOut{}, err
			}
			ch := findChart(&w, in.Chart)
			if ch == nil {
				return DeletedOut{}, fmt.Errorf("no chart %q in the workspace (see get_workspace)", in.Chart)
			}
			if !agentMade(ch.CreatedBy) {
				return DeletedOut{}, errors.New("this is one of the user's charts; agents can only delete charts they created")
			}
			id, name := ch.ID, ch.Name
			if _, err := c.s.m.Store.BI().Update(p, func(w *bi.Workspace) error { return w.Delete(id) }); err != nil {
				return DeletedOut{}, err
			}
			c.act.SQL = p + " › " + name
			c.biChanged(p)
			return DeletedOut{Deleted: name}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "delete_data_source", Description: "Deletes a data source created over MCP, with its archive snapshot. Delete the charts built on it first; " +
		"the user's own data sources can't be deleted.",
		Annotations: &mcp.ToolAnnotations{Title: "Delete data source", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in SourceArgs) (DeletedOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return DeletedOut{}, err
			}
			src := findSource(&w, in.DataSource)
			if src == nil {
				return DeletedOut{}, fmt.Errorf("no data source %q in the workspace (see get_workspace)", in.DataSource)
			}
			if !agentMade(src.CreatedBy) {
				return DeletedOut{}, errors.New("this is one of the user's data sources; agents can only delete data sources they created")
			}
			if users := w.ChartsUsing(src.ID); len(users) > 0 {
				var names []string
				for _, u := range users {
					names = append(names, u.Name)
				}
				return DeletedOut{}, fmt.Errorf("charts are built on %q: %s; delete them first", src.Name, strings.Join(names, ", "))
			}
			id, name := src.ID, src.Name
			lib := c.s.m.Store.BI()
			if _, err := lib.Update(p, func(w *bi.Workspace) error { return w.Delete(id) }); err != nil {
				return DeletedOut{}, err
			}
			_ = os.Remove(lib.ArchivePath(w.ID, id))
			c.act.SQL = p + " › " + name
			c.biChanged(p)
			return DeletedOut{Deleted: name}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "open_chart", Description: "Opens a chart in Chloe for the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Open chart", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in ChartArgs) (ChartOut, error) {
			p, w, err := c.workspace(in.Workspace)
			if err != nil {
				return ChartOut{}, err
			}
			ch := findChart(&w, in.Chart)
			if ch == nil {
				return ChartOut{}, fmt.Errorf("no chart %q in the workspace (see get_workspace)", in.Chart)
			}
			if c.s.events == nil {
				return ChartOut{}, errors.New("Chloe's window isn't available")
			}
			c.s.events.OpenBI(p, ch.ID)
			return ChartOut{Chart: *ch, Editable: agentMade(ch.CreatedBy)}, nil
		})
}

// workspace reads a workspace of Chloe's library by name or path; no other file is opened.
func (c *call) workspace(nameOrPath string) (string, bi.Workspace, error) {
	p, err := c.s.m.Store.BI().Find(nameOrPath)
	if err != nil {
		return "", bi.Workspace{}, fmt.Errorf("%v (see list_workspaces)", err)
	}
	w, err := bi.Read(p)
	return p, w, err
}

func workspaceInfo(p string, w bi.Workspace) WorkspaceInfo {
	return WorkspaceInfo{Path: p, Name: w.Name, DataSources: len(w.DataSources), Charts: len(w.Charts), CreatedBy: w.CreatedBy}
}

func (c *call) biChanged(p string) {
	if c.s.events != nil {
		c.s.events.BIChanged(p)
	}
}

// checkSource applies run_query's rules to a query source: one read statement,
// and on MySQL and SQLite only where no database of the connection is hidden.
func (c *call) checkSource(tg target, src bi.DataSource) error {
	if strings.TrimSpace(src.SQL) == "" {
		return nil
	}
	if tg.typ != model.PostgreSQL && tg.access.hidesDatabases() {
		return errors.New("query data sources aren't available here because some databases of this connection are hidden from this endpoint; use a table")
	}
	d, err := c.s.m.Dialect(tg.connID)
	if err != nil {
		return err
	}
	_, err = bi.CheckSQL(d, src.SQL)
	return err
}

// canRead checks the endpoint may read a data source's database, archived or
// not (the snapshot's rows came from there).
func (c *call) canRead(ctx context.Context, src bi.DataSource) error {
	tg, err := c.resolve(ctx, src.ConnID, src.Database, src.Schema, store.MCPRead)
	if err != nil {
		return fmt.Errorf("data source %q: %w", src.Name, err)
	}
	return c.checkSource(tg, src)
}

// readable reports whether the endpoint may read a data source's database,
// without logging it as the call's connection.
func (c *call) readable(src bi.DataSource) bool {
	conn, a, err := c.connection(src.ConnID)
	if err != nil {
		return false
	}
	db := src.Database
	if db == "" {
		db = conn.InitialDatabase
	}
	return atLeast(a.db(db), store.MCPRead)
}

func findSource(w *bi.Workspace, ref string) *bi.DataSource {
	if d := w.Source(ref); d != nil {
		return d
	}
	for i := range w.DataSources {
		if strings.EqualFold(w.DataSources[i].Name, ref) {
			return &w.DataSources[i]
		}
	}
	return nil
}

func findChart(w *bi.Workspace, ref string) *bi.Chart {
	if ch := w.Chart(ref); ch != nil {
		return ch
	}
	for i := range w.Charts {
		if strings.EqualFold(w.Charts[i].Name, ref) {
			return &w.Charts[i]
		}
	}
	return nil
}

func (c *call) putChart(p string, ch bi.Chart) (bi.Chart, error) {
	var saved bi.Chart
	_, err := c.s.m.Store.BI().Update(p, func(w *bi.Workspace) error {
		var err error
		saved, err = w.PutChart(ch)
		return err
	})
	if err != nil {
		return saved, err
	}
	c.act.SQL = p + " › " + saved.Name
	c.biChanged(p)
	return saved, nil
}

// applySpec sets the parts of a chart the agent passed and checks the result
// against the data source.
func (c *call) applySpec(ch *bi.Chart, src bi.DataSource, in ChartSpec) error {
	if in.Type != "" {
		ch.Type = in.Type
	}
	t, ok := bi.ChartTypes[ch.Type]
	if !ok {
		return fmt.Errorf("unknown chart type %q (types: %s)", ch.Type, strings.Join(bi.TypeNames(), ", "))
	}
	shelf := func(name string, args []FieldArg, dst *[]bi.ChartField) error {
		if args == nil {
			return nil
		}
		out := []bi.ChartField{}
		for _, a := range args {
			f, ok := src.Field(a.Field)
			if !ok {
				return fmt.Errorf("data source %q has no field %q (fields: %s)", src.Name, a.Field, strings.Join(names(src.Visible()), ", "))
			}
			agg := a.Aggregate
			if a.Bucket != "" {
				if agg != "" {
					return fmt.Errorf("%s: pass aggregate or bucket, not both", a.Field)
				}
				agg = a.Bucket
			}
			if agg == "" && !(t.Raw && name == "value") {
				agg = bi.DefaultAggregate(name, f, false)
			}
			op := a.Operation
			if op == "" {
				op = a.Op
			}
			out = append(out, bi.ChartField{Field: a.Field, Aggregate: agg, Alias: a.Alias, Op: op})
		}
		*dst = out
		return nil
	}
	for _, sh := range []struct {
		name string
		args []FieldArg
		dst  *[]bi.ChartField
	}{{"dimension", in.Dimension, &ch.Dimension}, {"series", in.Series, &ch.Series}, {"value", in.Value, &ch.Value}, {"secondary", in.Secondary, &ch.Secondary}} {
		if err := shelf(sh.name, sh.args, sh.dst); err != nil {
			return err
		}
	}
	if in.Filters != nil {
		fs, err := filters(in.Filters)
		if err != nil {
			return err
		}
		ch.Filters = fs
	}
	if in.Sort != nil {
		ch.Sort = &bi.ChartSort{By: in.Sort.By, Field: in.Sort.Field, Desc: in.Sort.Desc}
	}
	if in.Top != nil {
		ch.Top = *in.Top
	}
	if in.Others != nil {
		ch.Others = *in.Others
	}
	if in.Properties == nil {
		in.Properties = in.Props
	}
	if in.Properties != nil {
		var p bi.Props
		data, _ := json.Marshal(in.Properties)
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&p); err != nil {
			return fmt.Errorf("properties: %w", err)
		}
		ch.Props = p
	}
	dialect := "sqlite"
	if src.Mode == bi.ModeLive {
		dialect = c.dialectName(src.ConnID)
	}
	complete, err := ch.Check(src, dialect)
	if err != nil {
		return err
	}
	if !complete {
		var need []string
		for _, s := range []struct {
			n string
			l bi.Shelf
			f []bi.ChartField
		}{{"dimension", t.Dimension, ch.Dimension}, {"series", t.Series, ch.Series}, {"value", t.Value, ch.Value}, {"secondary", t.Secondary, ch.Secondary}} {
			if len(s.f) < s.l.Min {
				need = append(need, fmt.Sprintf("%s (at least %d)", s.n, s.l.Min))
			}
		}
		return fmt.Errorf("a %s chart needs %s", t.Name, strings.Join(need, " and "))
	}
	return nil
}

func (c *call) dialectName(connID string) string {
	conn, err := c.s.m.Store.Get(connID)
	if err != nil {
		return ""
	}
	switch conn.Type {
	case model.PostgreSQL:
		return "postgres"
	case model.SQLite:
		return "sqlite"
	}
	return "mysql"
}

func names(fs []bi.Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}

// filters converts the agent's conditions; values become text, as Chloe binds them.
func filters(in []FilterArg) ([]bi.Filter, error) {
	out := []bi.Filter{}
	for _, a := range in {
		f := bi.Filter{Field: a.Field, Op: a.Op}
		v := a.Value
		if v == nil && a.Values != nil {
			v = a.Values
		}
		switch v := v.(type) {
		case nil:
		case []any:
			for _, x := range v {
				f.Values = append(f.Values, text(x))
			}
		default:
			f.Values = []string{text(v)}
		}
		n, ok := bi.FilterOps[f.Op]
		if ok && n == 1 && len(f.Values) != 1 {
			return nil, fmt.Errorf("%s on %s needs one value", f.Op, f.Field)
		}
		out = append(out, f)
	}
	return out, nil
}

func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return ""
	}
	data, _ := json.Marshal(v)
	return string(data)
}
