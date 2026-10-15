package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"

	"chloe/internal/core"
	"chloe/internal/datamodel"
	"chloe/internal/design"
)

// ModelService backs offline data models: files, reverse engineering, SQL
// generation and synchronization to a database.
type ModelService struct{ M *core.Manager }

// ModelFile is a model together with the file it was read from or saved to.
type ModelFile struct {
	Path  string          `json:"path"`
	Model datamodel.Model `json:"model"`
}

func (s *ModelService) New(name, dialect string) datamodel.Model { return datamodel.New(name, dialect) }

// Meta is designer metadata for model tables (no server involved).
func (s *ModelService) Meta(dialect string) design.Meta { return design.OfflineMeta(dialect) }

// Open reads a model file; with an empty path it asks for one first. A cancelled
// dialog returns an empty Path.
func (s *ModelService) Open(path string) (ModelFile, error) {
	if path == "" {
		p, err := application.Get().Dialog.OpenFile().SetTitle("Open Model").AddFilter("Chloe Model", "*.chloemodel").PromptForSingleSelection()
		if err != nil || p == "" {
			return ModelFile{}, err
		}
		path = p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		s.forget(path)
		return ModelFile{}, err
	}
	mdl, err := datamodel.Parse(data)
	if err != nil {
		return ModelFile{}, err
	}
	s.remember(path)
	return ModelFile{Path: path, Model: mdl}, nil
}

// Save writes the model; with an empty path it asks where first. Returns the path
// written, or "" when the dialog was cancelled.
func (s *ModelService) Save(path string, mdl datamodel.Model) (string, error) {
	if path == "" {
		name := strings.TrimSpace(mdl.Name)
		if name == "" {
			name = "Untitled"
		}
		p, err := application.Get().Dialog.SaveFile().SetFilename(name+".chloemodel").CanCreateDirectories(true).AddFilter("Chloe Model", "*.chloemodel").PromptForSingleSelection()
		if err != nil || p == "" {
			return "", err
		}
		if filepath.Ext(p) == "" {
			p += ".chloemodel"
		}
		path = p
	}
	data, err := mdl.Marshal()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	s.remember(path)
	return path, nil
}

// Recent lists recently opened or saved model files, newest first.
func (s *ModelService) Recent() []string { return s.M.Store.RecentModels() }

func (s *ModelService) remember(path string) { s.M.Store.RememberModel(path) }

func (s *ModelService) forget(path string) { s.M.Store.ForgetModel(path) }

// Reverse builds a model from the tables of a database (and PostgreSQL schema).
func (s *ModelService) Reverse(ctx context.Context, connID, database, schema string, tables []string) (datamodel.Model, error) {
	return s.M.ReverseModel(ctx, connID, database, schema, tables)
}

// GenerateSQL renders the whole model as CREATE statements.
func (s *ModelService) GenerateSQL(mdl datamodel.Model, to datamodel.Target) ([]string, error) {
	return mdl.GenerateSQL(to)
}

// TableSQL renders one model table (the designer's SQL preview).
func (s *ModelService) TableSQL(mdl datamodel.Model, table design.Table) ([]string, error) {
	mdl.Tables = []design.Table{table}
	return mdl.GenerateSQL(datamodel.Target{})
}

// PlanSync compares the model with a database and lists the changes.
func (s *ModelService) PlanSync(ctx context.Context, connID string, mdl datamodel.Model, to datamodel.Target, dropMissing bool) (datamodel.SyncPlan, error) {
	return s.M.PlanModelSync(ctx, connID, mdl, to, dropMissing)
}

// ApplySync runs a (possibly trimmed) plan.
func (s *ModelService) ApplySync(ctx context.Context, connID string, to datamodel.Target, plan datamodel.SyncPlan) core.ApplyResult {
	return s.M.ApplyModelSync(ctx, connID, to, plan)
}
