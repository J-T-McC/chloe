package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"chloe/internal/datamodel"
	"chloe/internal/design"
	"chloe/internal/model"
	"chloe/internal/store"
)

const modelExt = ".chloemodel"

type ModelArgs struct {
	Model string `json:"model" jsonschema:"the model's path from list_models"`
}

type GetModelArgs struct {
	ModelArgs
	Tables []string `json:"tables,omitempty" jsonschema:"only these tables (default all)"`
}

type CreateModelArgs struct {
	Name    string `json:"name"`
	Dialect string `json:"dialect,omitempty" jsonschema:"mysql or postgres, for an empty model"`
	// Reverse engineering.
	Connection string   `json:"connection,omitempty" jsonschema:"build the model from this connection's database"`
	Database   string   `json:"database,omitempty"`
	Schema     string   `json:"schema,omitempty" jsonschema:"PostgreSQL schema (default public)"`
	Tables     []string `json:"tables,omitempty" jsonschema:"with connection: only these tables (default all)"`
	CopyOf     string   `json:"copy_of,omitempty" jsonschema:"a model path from list_models to copy"`
}

type EditModelArgs struct {
	ModelArgs
	SetTables  []map[string]any `json:"set_tables,omitempty" jsonschema:"tables to add, or to replace when the name matches; same shape as get_model's tables (name, columns, primaryKey, indexes, foreignKeys…)"`
	DropTables []string         `json:"drop_tables,omitempty"`
	Rename     string           `json:"rename,omitempty" jsonschema:"a new name for the model"`
}

type SQLArgs struct {
	ModelArgs
	Connection string `json:"connection,omitempty" jsonschema:"compare with this connection's database and return the statements that would bring it in line with the model"`
	Database   string `json:"database,omitempty"`
	Schema     string `json:"schema,omitempty" jsonschema:"PostgreSQL schema (default public)"`
}

type ModelInfo struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Dialect  string `json:"dialect,omitempty"`
	Tables   int    `json:"table_count"`
	Editable bool   `json:"editable" jsonschema:"created over MCP, so edit_model may change it"`
	Error    string `json:"error,omitempty"`
}

type ModelsOut struct {
	Models []ModelInfo `json:"models"`
}

type ModelOut struct {
	ModelInfo
	Schema string         `json:"schema,omitempty" jsonschema:"default PostgreSQL schema"`
	Detail []design.Table `json:"tables"`
}

type SQLOut struct {
	Statements []string `json:"statements"`
	Unchanged  []string `json:"unchanged,omitempty" jsonschema:"tables already matching the model"`
	Warnings   []string `json:"warnings,omitempty"`
}

func (s *Server) addModelTools(srv *mcp.Server, ep string) {
	f := false
	t := true
	add(s, srv, ep, readOnlyTool("list_models", "List models",
		"Lists data models (ER diagrams): the user's recent model files, which you can read and copy, and the models created over MCP, which you can edit."),
		func(ctx context.Context, c *call, _ NoArgs) (ModelsOut, error) {
			out := ModelsOut{Models: []ModelInfo{}}
			for _, p := range c.modelPaths() {
				m, err := readModel(p)
				info := c.modelInfo(p, m)
				if err != nil {
					info.Error = err.Error()
				}
				out.Models = append(out.Models, info)
			}
			return out, nil
		})

	add(s, srv, ep, readOnlyTool("get_model", "Get model", "Returns a model's tables: columns, keys, indexes, foreign keys and options."),
		func(ctx context.Context, c *call, in GetModelArgs) (ModelOut, error) {
			p, m, err := c.model(in.Model)
			if err != nil {
				return ModelOut{}, err
			}
			out := ModelOut{ModelInfo: c.modelInfo(p, m), Schema: m.Schema, Detail: []design.Table{}}
			for _, t := range m.Tables {
				if len(in.Tables) == 0 || slices.ContainsFunc(in.Tables, func(n string) bool { return strings.EqualFold(n, t.Name) || strings.EqualFold(n, datamodel.Key(t)) }) {
					out.Detail = append(out.Detail, t)
				}
			}
			if data, _ := json.Marshal(out.Detail); len(data) > maxResultBytes {
				return ModelOut{}, fmt.Errorf("the model is too large to return at once (%d KB); pass tables", len(data)>>10)
			}
			return out, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "create_model", Description: "Creates a model you can edit: empty (name and dialect), reverse engineered from a database (connection, database, schema, tables), " +
		"or a copy of another model (copy_of). It's saved in Chloe and added to the user's recent models.",
		Annotations: &mcp.ToolAnnotations{Title: "Create model", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in CreateModelArgs) (ModelInfo, error) {
			var m datamodel.Model
			switch {
			case in.CopyOf != "" && in.Connection != "":
				return ModelInfo{}, errors.New("pass copy_of or connection, not both")
			case in.CopyOf != "":
				_, src, err := c.model(in.CopyOf)
				if err != nil {
					return ModelInfo{}, err
				}
				m = src
			case in.Connection != "":
				tg, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
				if err != nil {
					return ModelInfo{}, err
				}
				if tg.typ == model.SQLite {
					return ModelInfo{}, errors.New("models support MySQL, MariaDB and PostgreSQL, not SQLite")
				}
				if m, err = c.s.m.ReverseModel(ctx, tg.connID, tg.database, tg.schema, in.Tables); err != nil {
					return ModelInfo{}, err
				}
			default:
				d := strings.ToLower(in.Dialect)
				if d == "postgresql" {
					d = "postgres"
				}
				if d != "mysql" && d != "mariadb" && d != "postgres" {
					return ModelInfo{}, errors.New("an empty model needs dialect mysql or postgres")
				}
				m = datamodel.New("", d)
			}
			if name := strings.TrimSpace(in.Name); name != "" {
				m.Name = name
			}
			if strings.TrimSpace(m.Name) == "" {
				return ModelInfo{}, errors.New("name is required")
			}
			p, err := c.saveNewModel(m)
			if err != nil {
				return ModelInfo{}, err
			}
			return c.modelInfo(p, m), nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "edit_model", Description: "Changes a model created over MCP: adds or replaces tables (set_tables), drops tables, or renames it. " +
		"Other tables are kept. The user's own models can't be edited; copy one with create_model first. It never touches a database.",
		Annotations: &mcp.ToolAnnotations{Title: "Edit model", DestructiveHint: &t, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in EditModelArgs) (ModelInfo, error) {
			p, m, err := c.model(in.Model)
			if err != nil {
				return ModelInfo{}, err
			}
			if !c.agentModel(p) {
				return ModelInfo{}, errors.New("this is one of the user's models; agents can't change them, so copy it with create_model(copy_of) and edit the copy")
			}
			key := func(t design.Table) string {
				if m.Dialect == "postgres" && t.Schema == "" {
					t.Schema = m.Schema
				}
				return strings.ToLower(datamodel.Key(t))
			}
			for _, name := range in.DropTables {
				n := len(m.Tables)
				m.Tables = slices.DeleteFunc(m.Tables, func(t design.Table) bool {
					if strings.EqualFold(t.Name, name) || strings.EqualFold(datamodel.Key(t), name) {
						delete(m.Positions, datamodel.Key(t))
						return true
					}
					return false
				})
				if len(m.Tables) == n {
					return ModelInfo{}, fmt.Errorf("no table %q in the model", name)
				}
			}
			for i, raw := range in.SetTables {
				t, err := decodeTable(raw)
				if err != nil {
					return ModelInfo{}, fmt.Errorf("set_tables[%d]: %w", i, err)
				}
				t.Database, t.OrigName = "", ""
				if j := slices.IndexFunc(m.Tables, func(o design.Table) bool { return key(o) == key(t) }); j >= 0 {
					m.Tables[j] = t
				} else {
					m.Tables = append(m.Tables, t)
				}
			}
			if r := strings.TrimSpace(in.Rename); r != "" {
				m.Name = r
			}
			if err := m.Validate(); err != nil {
				return ModelInfo{}, err
			}
			if err := writeModel(p, m); err != nil {
				return ModelInfo{}, err
			}
			c.s.m.Store.RememberModel(p)
			if c.s.events != nil {
				c.s.events.ModelChanged(p)
			}
			return c.modelInfo(p, m), nil
		})

	add(s, srv, ep, readOnlyTool("generate_sql", "Generate model SQL",
		"Returns the model as SQL without running it: CREATE statements, or with a connection the statements that would bring that database in line with the model. "+
			"Applying them is up to the user (Synchronize to Database, or a query tab)."),
		func(ctx context.Context, c *call, in SQLArgs) (SQLOut, error) {
			_, m, err := c.model(in.Model)
			if err != nil {
				return SQLOut{}, err
			}
			if in.Connection == "" {
				stmts, err := m.GenerateSQL(datamodel.Target{Database: in.Database, Schema: in.Schema})
				return SQLOut{Statements: nonNil(stmts)}, err
			}
			tg, err := c.resolve(ctx, in.Connection, in.Database, in.Schema, store.MCPRead)
			if err != nil {
				return SQLOut{}, err
			}
			if tg.typ == model.SQLite {
				return SQLOut{}, errors.New("models support MySQL, MariaDB and PostgreSQL, not SQLite")
			}
			schema := ""
			if tg.typ == model.PostgreSQL {
				schema = tg.schema
			}
			plan, err := c.s.m.PlanModelSync(ctx, tg.connID, m, datamodel.Target{Database: tg.database, Schema: schema}, false)
			if err != nil {
				return SQLOut{}, err
			}
			c.act.SQL = strings.Join(plan.Statements(), ";\n")
			return SQLOut{Statements: nonNil(plan.Statements()), Unchanged: plan.Unchanged, Warnings: plan.Warnings}, nil
		})

	add(s, srv, ep, &mcp.Tool{Name: "open_model", Description: "Opens a model's diagram in Chloe for the user.",
		Annotations: &mcp.ToolAnnotations{Title: "Open model", DestructiveHint: &f, OpenWorldHint: &f}},
		func(ctx context.Context, c *call, in ModelArgs) (ModelInfo, error) {
			p, m, err := c.model(in.Model)
			if err != nil {
				return ModelInfo{}, err
			}
			if c.s.events == nil {
				return ModelInfo{}, errors.New("Chloe's window isn't available")
			}
			c.s.events.OpenModel(p)
			return c.modelInfo(p, m), nil
		})
}

// modelPaths lists the models an agent may read: those created over MCP, then
// the user's recent model files.
func (c *call) modelPaths() []string {
	var out []string
	if entries, err := os.ReadDir(c.s.m.Store.AgentModelDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() && filepath.Ext(e.Name()) == modelExt {
				out = append(out, filepath.Join(c.s.m.Store.AgentModelDir(), e.Name()))
			}
		}
	}
	for _, p := range c.s.m.Store.RecentModels() {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// model reads a model by a path list_models returned; no other file is opened.
func (c *call) model(path string) (string, datamodel.Model, error) {
	p := filepath.Clean(path)
	if !slices.Contains(c.modelPaths(), p) {
		return p, datamodel.Model{}, fmt.Errorf("no model %q (see list_models)", path)
	}
	m, err := readModel(p)
	return p, m, err
}

func (c *call) agentModel(p string) bool {
	return filepath.Dir(p) == filepath.Clean(c.s.m.Store.AgentModelDir())
}

func (c *call) modelInfo(p string, m datamodel.Model) ModelInfo {
	return ModelInfo{Path: p, Name: m.Name, Dialect: m.Dialect, Tables: len(m.Tables), Editable: c.agentModel(p)}
}

var unsafeName = regexp.MustCompile(`[^\pL\pN ._-]+`)

// saveNewModel writes m under a new file name in the agent model folder.
func (c *call) saveNewModel(m datamodel.Model) (string, error) {
	dir := c.s.m.Store.AgentModelDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	base := strings.Trim(unsafeName.ReplaceAllString(m.Name, "-"), " .-")
	if base == "" {
		base = "model"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	p := filepath.Join(dir, base+modelExt)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			break
		}
		p = filepath.Join(dir, fmt.Sprintf("%s %d%s", base, i, modelExt))
	}
	if err := writeModel(p, m); err != nil {
		return "", err
	}
	c.s.m.Store.RememberModel(p)
	c.act.SQL = p
	return p, nil
}

func readModel(p string) (datamodel.Model, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return datamodel.Model{}, err
	}
	return datamodel.Parse(data)
}

func writeModel(p string, m datamodel.Model) error {
	m.Format, m.Version = datamodel.Format, max(m.Version, 1)
	if m.Positions == nil {
		m.Positions = map[string]datamodel.Point{}
	}
	if m.Tables == nil {
		m.Tables = []design.Table{}
	}
	data, err := m.Marshal()
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// decodeTable reads a table the agent sent, refusing unknown fields so a
// misspelt one isn't silently dropped.
func decodeTable(raw map[string]any) (design.Table, error) {
	var t design.Table
	data, err := json.Marshal(raw)
	if err != nil {
		return t, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return t, err
	}
	if strings.TrimSpace(t.Name) == "" {
		return t, errors.New("a table needs a name")
	}
	return t, nil
}
