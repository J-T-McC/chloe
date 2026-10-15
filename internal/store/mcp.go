package store

import (
	"errors"
	"slices"
)

// MCPLevel is how much an MCP endpoint may do with a group, connection or database.
type MCPLevel string

const (
	MCPOff   MCPLevel = ""
	MCPRead  MCPLevel = "read"
	MCPWrite MCPLevel = "write"
	// MCPInherit takes the level of the connection's group (connections only).
	MCPInherit MCPLevel = "inherit"
)

// MCPConnAccess is an endpoint's access to one connection. Databases overrides
// Level for single databases (PostgreSQL databases, MySQL databases, SQLite files).
// A connection without an entry inherits its group's level.
type MCPConnAccess struct {
	Level     MCPLevel            `json:"level"`
	Databases map[string]MCPLevel `json:"databases,omitempty"`
}

// MCPCapabilities are the opt-in tool groups beyond data access.
type MCPCapabilities struct {
	CreateConnections bool `json:"createConnections"`
	SavedQueries      bool `json:"savedQueries"`
	Monitor           bool `json:"monitor"`
	Models            bool `json:"models"`
	// Reports: BI workspaces, data sources and charts.
	Reports bool `json:"reports"`
}

// MCPEndpoint is a named access profile with its own token.
type MCPEndpoint struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Token   string `json:"token"`
	// Danger runs writes without asking the user first.
	Danger       bool                     `json:"danger"`
	Capabilities MCPCapabilities          `json:"capabilities"`
	Access       map[string]MCPConnAccess `json:"access"`           // by connection ID
	Groups       map[string]MCPLevel      `json:"groups,omitempty"` // by group name
	// ConnectionGroup is where connections this endpoint creates go ("" = DefaultMCPConnectionGroup).
	ConnectionGroup string `json:"connectionGroup,omitempty"`
}

// DefaultMCPConnectionGroup is the group for connections agents create.
const DefaultMCPConnectionGroup = "Created by AI"

// ConnLevel is the endpoint's level for a connection in group: its own
// setting, else its group's.
func (e MCPEndpoint) ConnLevel(connID, group string) MCPLevel {
	if a, ok := e.Access[connID]; ok && a.Level != MCPInherit {
		return a.Level
	}
	if group != "" {
		return e.Groups[group]
	}
	return MCPOff
}

// DBLevel is the endpoint's level for one database of a connection.
func (e MCPEndpoint) DBLevel(connID, group, database string) MCPLevel {
	if l, ok := e.Access[connID].Databases[database]; ok {
		return l
	}
	return e.ConnLevel(connID, group)
}

// NewConnectionGroup is the group connections created through the endpoint go into.
func (e MCPEndpoint) NewConnectionGroup() string {
	if e.ConnectionGroup != "" {
		return e.ConnectionGroup
	}
	return DefaultMCPConnectionGroup
}

// MCPSettings is the MCP server configuration, kept in mcp.json.
type MCPSettings struct {
	Enabled bool `json:"enabled"`
	Port    int  `json:"port"`
	// NoHTTP leaves only the socket that the `chloe mcp` stdio bridge uses.
	NoHTTP    bool          `json:"noHttp,omitempty"`
	Endpoints []MCPEndpoint `json:"endpoints"`
}

// DefaultMCPPort is where the server listens unless the user picks another port.
const DefaultMCPPort = 47101

const mcpFile = "mcp.json"

// MCP returns the MCP server settings. Like the connections file they are
// encrypted with the keychain key; while the store is locked they can't be read.
func (s *Store) MCP() (MCPSettings, error) {
	var m MCPSettings
	if s.locked != "" {
		return m, errors.New(s.locked)
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if err := s.readDoc(mcpFile, &m); err != nil {
		return m, err
	}
	if m.Port == 0 {
		m.Port = DefaultMCPPort
	}
	if m.Endpoints == nil {
		m.Endpoints = []MCPEndpoint{}
	}
	return m, nil
}

// OnMCPChange registers a function called when the store itself changes the
// MCP settings (a group was renamed or deleted), not when SetMCP is called.
func (s *Store) OnMCPChange(fn func(MCPSettings)) {
	s.mcpMu.Lock()
	s.mcpHook = fn
	s.mcpMu.Unlock()
}

// renameMCPGroup moves group access from old to name; an empty name removes it.
func (s *Store) renameMCPGroup(old, name string) {
	m, err := s.MCP()
	if err != nil {
		return
	}
	changed := false
	for i := range m.Endpoints {
		e := &m.Endpoints[i]
		if l, ok := e.Groups[old]; ok {
			delete(e.Groups, old)
			if name != "" {
				e.Groups[name] = l
			}
			changed = true
		}
		if e.NewConnectionGroup() == old {
			// A deleted group falls back to the default; it's recreated when needed.
			e.ConnectionGroup, changed = name, true
		}
	}
	if !changed || s.SetMCP(m) != nil {
		return
	}
	s.mcpMu.Lock()
	hook := s.mcpHook
	s.mcpMu.Unlock()
	if hook != nil {
		hook(m)
	}
}

// SetMCP saves the MCP server settings.
func (s *Store) SetMCP(m MCPSettings) error {
	if s.locked != "" {
		return errors.New(s.locked)
	}
	ids := map[string]bool{}
	for _, e := range m.Endpoints {
		if e.ID == "" || ids[e.ID] {
			return errors.New("every endpoint needs a unique ID")
		}
		ids[e.ID] = true
	}
	m.Endpoints = slices.Clone(m.Endpoints)
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	return s.writeDoc(mcpFile, m)
}
