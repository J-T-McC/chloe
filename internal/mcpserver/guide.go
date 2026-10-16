package mcpserver

import (
	"context"
	"embed"
	"fmt"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/store"
)

// The guide is what an agent should know beyond each tool's own description:
// how the tools fit together, and how to use them without hurting the user's
// servers. The instructions are its short form, sent when a session starts;
// the guide tool returns its topics in full.

//go:embed guide/*.md
var guideFS embed.FS

// offer is what one endpoint is offered, which decides what its instructions
// say and which guide topics it has.
type offer struct {
	sql, redis, mongo bool // engines it sees
	writes, danger    bool
	caps              store.MCPCapabilities
}

func offerFor(ep store.MCPEndpoint, sql, redis, mongo bool) offer {
	return offer{sql: sql, redis: redis, mongo: mongo, writes: canWrite(ep), danger: ep.Danger, caps: ep.Capabilities}
}

// topics are the guide topics an endpoint has, in the order they're listed.
func (o offer) topics() []string {
	var t []string
	add := func(ok bool, name string) {
		if ok {
			t = append(t, name)
		}
	}
	add(o.sql, "sql")
	add(o.writes, "writes")
	add(o.caps.Reports, "reports")
	add(o.caps.Models, "models")
	add(o.mongo, "mongodb")
	add(o.redis, "redis")
	return t
}

// instructions are the server instructions for an endpoint.
func (o offer) instructions() string {
	var b strings.Builder
	p := func(s string) { b.WriteString(s + "\n") }
	p("Chloe is a database client (build " + Build + "). These tools reach only the connections the user has shared with this endpoint; " +
		"list_connections shows them and what you may do with each (read, or read-write, per database).")
	p("")
	p("How to work:")
	var look []string
	if o.sql {
		look = append(look, "list_tables (it estimates row counts), then describe_table for the columns, indexes and foreign keys of the tables you'll use")
	}
	if o.mongo {
		look = append(look, "mongo_describe for a collection's fields and indexes")
	}
	if o.redis {
		look = append(look, "redis_overview for what a Redis database holds")
	}
	p("- Look before you query: " + strings.Join(look, "; ") + ". Don't guess names or joins.")
	p("- Let the database do the work. Reads return at most 1000 rows or documents (truncated says when there were more), so filter, " +
		"join and aggregate in the query rather than adding up returned rows.")
	if o.sql {
		p("- Be kind to the server: it is often in production. Each statement has 30 seconds. Before a query over a big table, or one " +
			"that will run again (a saved query, a report data source), check its plan with EXPLAIN through run_query. When an index is " +
			"missing, tell the user and suggest the CREATE INDEX instead of reading more rows.")
		ddl := "give the user the SQL in your reply to run themselves."
		if o.caps.SavedQueries {
			ddl = "write the SQL and open it with open_query_tab for the user to review and run."
		}
		p("- No DDL: Chloe doesn't run CREATE, ALTER, DROP, GRANT or other schema changes over MCP; " + ddl)
	} else {
		p("- Be kind to the server: it is often in production. Each call has 30 seconds; use indexes, and read big values a page at a time.")
	}
	if o.writes {
		ask := "Writes wait for the user to approve each call in Chloe. "
		if o.sql || o.mongo {
			ask += "Batch them: one insert call takes up to 1000 rows or documents and is one approval. "
		}
		ask += "If the user declines, that is their answer: don't retry the change another way; ask them."
		if o.danger {
			ask = "This endpoint is in danger mode: writes run at once, without asking the user. Say what you'll change before you change it."
		}
		p("- " + ask)
	} else {
		p("- This endpoint is read-only: there are no write tools.")
	}
	if o.caps.Reports {
		p("- Reports: every chart on a live data source runs its own query with the source's SQL inside it, each time it is drawn, so a " +
			"slow source is slow once per chart. Read guide topic reports before building a dashboard.")
	}
	if o.caps.Models {
		p("- ER models never touch a database: create_model, edit_model, then generate_sql; the user applies the SQL.")
	}
	p("")
	p("The guide tool has more on: " + strings.Join(o.topics(), ", ") + ".")
	return strings.TrimSpace(b.String())
}

type GuideArgs struct {
	Topic string `json:"topic" jsonschema:"the topic to read; the server instructions list this endpoint's topics"`
}

type GuideOut struct {
	Topic  string   `json:"topic"`
	Text   string   `json:"text" jsonschema:"the topic, as Markdown"`
	Topics []string `json:"topics" jsonschema:"every topic this endpoint has"`
}

func guideTopic(name string) (string, error) {
	b, err := guideFS.ReadFile("guide/" + name + ".md")
	return string(b), err
}

func (s *Server) addGuideTool(srv *mcp.Server, ep string, o offer) {
	topics := o.topics()
	add(s, srv, ep, readOnlyTool("guide", "Read the guide",
		"Explains how to use these tools well: one topic of "+strings.Join(topics, ", ")+". "+
			"Read the topic before you start on that kind of work (reports before building a dashboard, writes before changing data)."),
		func(ctx context.Context, c *call, in GuideArgs) (GuideOut, error) {
			t := strings.ToLower(strings.TrimSpace(in.Topic))
			if !slices.Contains(topics, t) {
				return GuideOut{}, fmt.Errorf("no topic %q; this endpoint has %s", in.Topic, strings.Join(topics, ", "))
			}
			text, err := guideTopic(t)
			return GuideOut{Topic: t, Text: text, Topics: topics}, err
		})
}
