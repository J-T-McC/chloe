package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/store"
)

// QueryTab asks the UI to open a query tab. With QueryName it opens that saved
// query; otherwise a new, unsaved tab holding SQL.
type QueryTab struct {
	ConnID    string `json:"connId"`
	Database  string `json:"database"`
	Schema    string `json:"schema,omitempty"`
	Title     string `json:"title"`
	SQL       string `json:"sql,omitempty"`
	QueryName string `json:"queryName,omitempty"`
}

type SavedQueryArgs struct {
	DBArgs
	Name string `json:"name" jsonschema:"the saved query's name"`
}

type SaveQueryArgs struct {
	DBArgs
	Name string `json:"name" jsonschema:"a name not yet used in this database's Queries folder"`
	SQL  string `json:"sql"`
}

type OpenTabArgs struct {
	DBArgs
	SQL  string `json:"sql,omitempty" jsonschema:"SQL for a new query tab; leave it out to open the saved query called name"`
	Name string `json:"name,omitempty" jsonschema:"with sql: the new tab's title; without: a saved query to open"`
}

type SavedQueryOut struct {
	Name     string `json:"name"`
	SQL      string `json:"sql,omitempty"`
	Modified string `json:"modified"`
}

type SavedQueriesOut struct {
	Queries []SavedQueryOut `json:"queries"`
}

type OpenedOut struct {
	Opened string `json:"opened" jsonschema:"the tab's title"`
}

const maxQueryBytes = 256 << 10

func (s *Server) addQueryTools(srv *mcp.Server, ep string) {
	add(s, srv, ep, readOnlyTool("list_saved_queries", "List saved queries",
		"Lists the queries the user saved in a database's Queries folder (PostgreSQL: in one schema)."),
		func(ctx context.Context, c *call, in DBArgs) (SavedQueriesOut, error) {
			t, err := c.lookup(in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return SavedQueriesOut{}, err
			}
			out := SavedQueriesOut{Queries: []SavedQueryOut{}}
			for _, q := range c.s.m.Store.Workspace().Queries(t.connID, t.database, t.schema) {
				out.Queries = append(out.Queries, SavedQueryOut{Name: q.Name, Modified: q.Modified})
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("get_saved_query", "Get saved query", "Returns the SQL of a saved query."),
		func(ctx context.Context, c *call, in SavedQueryArgs) (SavedQueryOut, error) {
			t, err := c.lookup(in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return SavedQueryOut{}, err
			}
			q, err := c.savedQuery(t, in.Name)
			if err != nil {
				return SavedQueryOut{}, err
			}
			return SavedQueryOut{Name: q.Name, SQL: q.SQL, Modified: q.Modified}, nil
		})

	add(s, srv, ep, writeTool("save_query", "Save query",
		"Saves SQL as a new query in a database's Queries folder, where the user can open and run it. "+
			"It never runs the SQL and never replaces an existing query: pick a name that isn't taken (see list_saved_queries).", false),
		func(ctx context.Context, c *call, in SaveQueryArgs) (SavedQueryOut, error) {
			t, err := c.lookup(in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return SavedQueryOut{}, err
			}
			c.act.SQL = in.SQL
			if err := checkQueryText(in.SQL); err != nil {
				return SavedQueryOut{}, err
			}
			ws := c.s.m.Store.Workspace()
			name := strings.TrimSpace(in.Name)
			if _, err := c.savedQuery(t, name); err == nil {
				return SavedQueryOut{}, fmt.Errorf("a query named %q already exists; agents can't replace saved queries, so choose another name", name)
			}
			q, err := ws.SaveQuery(store.SavedQuery{ConnID: t.connID, Database: t.database, Schema: t.schema, Name: name, SQL: in.SQL})
			if err != nil {
				return SavedQueryOut{}, err
			}
			if c.s.events != nil {
				c.s.events.QuerySaved(t.connID, t.database, t.schema)
			}
			return SavedQueryOut{Name: q.Name, Modified: q.Modified}, nil
		})

	add(s, srv, ep, writeTool("open_query_tab", "Open query tab",
		"Opens a query tab in Chloe with SQL for the user to review, edit and run, or opens one of their saved queries. "+
			"Nothing runs and you don't see the results; tell the user what the SQL does.", false),
		func(ctx context.Context, c *call, in OpenTabArgs) (OpenedOut, error) {
			t, err := c.lookup(in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return OpenedOut{}, err
			}
			tab := QueryTab{ConnID: t.connID, Database: t.database, Schema: t.schema, Title: strings.TrimSpace(in.Name)}
			if in.SQL == "" {
				q, err := c.savedQuery(t, in.Name)
				if err != nil {
					return OpenedOut{}, err
				}
				tab.Title, tab.QueryName = q.Name, q.Name
			} else {
				if err := checkQueryText(in.SQL); err != nil {
					return OpenedOut{}, err
				}
				if tab.Title == "" {
					tab.Title = "Query from " + c.ep.Name
				}
				tab.SQL = in.SQL
			}
			c.act.SQL = in.SQL
			if c.s.events == nil {
				return OpenedOut{}, errors.New("Chloe's window isn't available")
			}
			c.s.events.OpenQueryTab(tab)
			return OpenedOut{Opened: tab.Title}, nil
		})
}

func (c *call) savedQuery(t target, name string) (store.SavedQuery, error) {
	for _, q := range c.s.m.Store.Workspace().Queries(t.connID, t.database, t.schema) {
		if strings.EqualFold(q.Name, strings.TrimSpace(name)) {
			return q, nil
		}
	}
	return store.SavedQuery{}, fmt.Errorf("no saved query %q in %s (see list_saved_queries)", name, t.database)
}

func checkQueryText(sql string) error {
	switch {
	case strings.TrimSpace(sql) == "":
		return errors.New("sql is empty")
	case len(sql) > maxQueryBytes:
		return fmt.Errorf("sql is longer than %d KB", maxQueryBytes>>10)
	}
	return nil
}
