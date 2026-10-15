package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/connurl"
	"chloe/internal/model"
	"chloe/internal/store"
)

type CreateConnectionArgs struct {
	Name     string `json:"name,omitempty" jsonschema:"name shown in Chloe (default from the host or file)"`
	URL      string `json:"url,omitempty" jsonschema:"connection string such as postgresql://user@host:5432/db?sslmode=require or mysql://user@host/db; instead of the separate fields"`
	Engine   string `json:"engine,omitempty" jsonschema:"mysql, mariadb, postgres or sqlite (when there's no url)"`
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty" jsonschema:"optional: leave it out and the user types it in Chloe, so it never passes through the conversation"`
	Database string `json:"database,omitempty" jsonschema:"initial database"`
	SSLMode  string `json:"ssl_mode,omitempty" jsonschema:"PostgreSQL sslmode (require, verify-ca, verify-full); for MySQL any value turns TLS on"`
	Path     string `json:"path,omitempty" jsonschema:"SQLite: an existing database file"`
}

type CreatedConnectionOut struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Group  string `json:"group"`
	Access string `json:"access" jsonschema:"what this endpoint may do with it (from its group's access)"`
}

// connectTimeout bounds the test connection made before saving.
const connectTimeout = 20 * time.Second

func (s *Server) addConnectionTools(srv *mcp.Server, ep string) {
	f := false
	t := true
	add(s, srv, ep, &mcp.Tool{Name: "create_connection",
		Description: "Saves a new MySQL, MariaDB, PostgreSQL or SQLite connection in Chloe. The user always reviews it in Chloe first and can type the password there. " +
			"Chloe tests it before saving. It goes into this endpoint's group for new connections, and its access comes from that group.",
		Annotations: &mcp.ToolAnnotations{Title: "Create connection", DestructiveHint: &f, OpenWorldHint: &t}},
		func(ctx context.Context, c *call, in CreateConnectionArgs) (CreatedConnectionOut, error) {
			conn, err := connectionFrom(in)
			if err != nil {
				return CreatedConnectionOut{}, err
			}
			group := c.ep.NewConnectionGroup()
			conn.Group = group
			conn.Name = c.uniqueName(conn.Name)
			c.act.Connection = conn.Name
			level := c.ep.ConnLevel("", group)
			details := describe(conn, group, level)
			c.act.SQL = details
			req := ApprovalRequest{Kind: "connection", Connection: conn.Name, Database: conn.InitialDatabase,
				Summary: "Add connection " + conn.Name, SQL: details,
				AskPassword: conn.Type != model.SQLite, HasPassword: conn.Password != ""}
			if conn.Type == model.SQLite {
				req.Warning = "The agent gets access to the database file " + conn.SQLite.Path + "."
			}
			if level == store.MCPOff {
				req.Warning = strings.TrimSpace(req.Warning + " Group “" + group + "” has no access for this endpoint, so the agent won't see the connection until you allow it.")
			}
			// Saving a connection is never covered by danger mode.
			a, err := c.ask(ctx, req)
			if err != nil {
				return CreatedConnectionOut{}, err
			}
			if a.Password != "" {
				conn.Password = a.Password
			}
			conn.SavePassword = conn.Password != ""
			tctx, cancel := context.WithTimeout(ctx, connectTimeout)
			_, err = c.s.m.TestConnection(tctx, conn)
			cancel()
			if err != nil {
				return CreatedConnectionOut{}, fmt.Errorf("couldn't connect, so the connection wasn't saved: %w", err)
			}
			if !slices.Contains(c.s.m.Store.Groups(), group) {
				if err := c.s.m.Store.AddGroup(group); err != nil {
					return CreatedConnectionOut{}, err
				}
			}
			saved, err := c.s.m.Store.Save(conn)
			if err != nil {
				return CreatedConnectionOut{}, err
			}
			if c.s.events != nil {
				c.s.events.ConnectionAdded(saved.ID)
			}
			return CreatedConnectionOut{ID: saved.ID, Name: saved.Name, Engine: string(saved.Type), Group: group, Access: levelName(level)}, nil
		})
}

// connectionFrom turns the agent's arguments into an unsaved profile. Only
// plain connections: no SSH tunnels, client certificates or socket files.
func connectionFrom(in CreateConnectionArgs) (model.Connection, error) {
	var c model.Connection
	if in.URL != "" {
		var err error
		if c, err = connurl.Parse(in.URL); err != nil {
			return c, err
		}
	} else {
		c.Type = model.DBType(strings.ToLower(in.Engine))
		switch {
		case c.Type == "postgresql":
			c.Type = model.PostgreSQL
		case c.Type == "" && in.Path != "":
			c.Type = model.SQLite
		}
	}
	if !slices.Contains(sqlEngines, c.Type) {
		return c, fmt.Errorf("engine must be mysql, mariadb, postgres or sqlite (got %q)", c.Type)
	}
	set := func(dst *string, v string) {
		if v != "" {
			*dst = v
		}
	}
	set(&c.Host, in.Host)
	set(&c.User, in.User)
	set(&c.Password, in.Password)
	set(&c.InitialDatabase, in.Database)
	set(&c.Name, in.Name)
	if in.Port != 0 {
		c.Port = in.Port
	}
	if in.SSLMode != "" {
		c.SSL.Enabled = true
		if c.Type == model.PostgreSQL {
			c.SSL.Mode = in.SSLMode
		}
	}
	c.SSH = model.SSHConfig{}
	c.SSL.CAFile, c.SSL.CertFile, c.SSL.KeyFile, c.SSL.CRLFile = "", "", "", ""
	c.Advanced.SocketFile, c.Advanced.InitialQueries = "", ""

	if c.Type == model.SQLite {
		path := in.Path
		if path == "" {
			path = c.SQLite.Path
		}
		if path == "" || !filepath.IsAbs(path) {
			return c, errors.New("path must be the absolute path of an existing SQLite file")
		}
		if st, err := os.Stat(path); err != nil || st.IsDir() {
			return c, fmt.Errorf("no SQLite file at %s", path)
		}
		c.SQLite = model.SQLiteConfig{Path: path}
		c.Host, c.Port, c.User, c.Password = "", 0, "", ""
		if c.Name == "" {
			c.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
		return c, nil
	}
	if c.Host == "" {
		return c, errors.New("host is required")
	}
	c.Defaults()
	if c.Name == "" {
		c.Name = c.Host
	}
	return c, nil
}

func (c *call) uniqueName(base string) string {
	taken := map[string]bool{}
	for _, conn := range c.s.m.Store.List() {
		taken[strings.ToLower(conn.Name)] = true
	}
	name := base
	for i := 2; taken[strings.ToLower(name)]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	return name
}

// describe lists a new connection's settings for the approval dialog and the log.
func describe(c model.Connection, group string, level store.MCPLevel) string {
	lines := []string{"Engine:   " + string(c.Type)}
	if c.Type == model.SQLite {
		lines = append(lines, "File:     "+c.SQLite.Path)
	} else {
		lines = append(lines, fmt.Sprintf("Host:     %s:%d", c.Host, c.Port), "User:     "+c.User)
		if c.InitialDatabase != "" {
			lines = append(lines, "Database: "+c.InitialDatabase)
		}
		ssl := "off"
		if c.SSL.Enabled {
			ssl = "on"
			if c.SSL.Mode != "" {
				ssl = c.SSL.Mode
			}
		}
		lines = append(lines, "SSL:      "+ssl)
	}
	access := map[store.MCPLevel]string{store.MCPOff: "none", store.MCPRead: "read", store.MCPWrite: "read-write"}[level]
	return strings.Join(append(lines, "Group:    "+group+" (this endpoint: "+access+")"), "\n")
}
