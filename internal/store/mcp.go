package store

import (
	"errors"
	"slices"
)

// MCPLevel is how much an MCP endpoint may do with a connection or database.
type MCPLevel string

const (
	MCPOff   MCPLevel = ""
	MCPRead  MCPLevel = "read"
	MCPWrite MCPLevel = "write"
)

// MCPConnAccess is an endpoint's access to one connection. Databases overrides
// Level for single databases (PostgreSQL databases, MySQL databases, SQLite files).
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
	Access       map[string]MCPConnAccess `json:"access"` // by connection ID
}

// MCPSettings is the MCP server configuration, kept in mcp.json.
type MCPSettings struct {
	Enabled   bool          `json:"enabled"`
	Port      int           `json:"port"`
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
