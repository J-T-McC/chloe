package services

import (
	"context"
	"errors"
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

// Dir is the folder of the models Chloe manages.
func (s *ModelService) Dir() string { return s.M.Store.Models().Dir }

// List describes every model in Chloe's library.
func (s *ModelService) List() []datamodel.Entry { return s.M.Store.Models().List() }

// Open reads a model file; with an empty path it asks for one first (a file
// outside the library). A cancelled dialog returns an empty Path.
func (s *ModelService) Open(path string) (ModelFile, error) {
	if path == "" {
		p, err := application.Get().Dialog.OpenFile().SetTitle("Open External Model").AddFilter("Chloe Model", "*"+datamodel.Ext).PromptForSingleSelection()
		if err != nil || p == "" {
			return ModelFile{}, err
		}
		path = p
	}
	mdl, err := datamodel.Read(path)
	if err != nil {
		return ModelFile{}, err
	}
	return ModelFile{Path: path, Model: mdl}, nil
}

// Create saves a new model into the library and returns its path.
func (s *ModelService) Create(mdl datamodel.Model) (string, error) {
	return s.M.Store.Models().Create(mdl)
}

// Save writes a model back to its file (in the library or outside it).
func (s *ModelService) Save(path string, mdl datamodel.Model) error {
	if path == "" {
		return errors.New("no file to save to")
	}
	return datamodel.Write(path, mdl)
}

// SaveExternal writes a copy of the model to a file the user picks. Returns the
// path written, or "" when the dialog was cancelled.
func (s *ModelService) SaveExternal(mdl datamodel.Model) (string, error) {
	name := strings.TrimSpace(mdl.Name)
	if name == "" {
		name = "Untitled"
	}
	p, err := application.Get().Dialog.SaveFile().SetFilename(name+datamodel.Ext).CanCreateDirectories(true).AddFilter("Chloe Model", "*"+datamodel.Ext).PromptForSingleSelection()
	if err != nil || p == "" {
		return "", err
	}
	if filepath.Ext(p) == "" {
		p += datamodel.Ext
	}
	return p, datamodel.Write(p, mdl)
}

// Import copies a model file into the library and returns the copy's path.
func (s *ModelService) Import(path string) (string, error) { return s.M.Store.Models().Import(path) }

// Rename renames a library model and returns its new path.
func (s *ModelService) Rename(path, name string) (string, error) {
	return s.M.Store.Models().Rename(path, name)
}

// Duplicate copies a library model under a new name and returns the copy's path.
func (s *ModelService) Duplicate(path string) (string, error) {
	lib := s.M.Store.Models()
	if !lib.Contains(path) {
		return "", errors.New("not a model in Chloe's library")
	}
	mdl, err := datamodel.Read(path)
	if err != nil {
		return "", err
	}
	mdl.Name += " copy"
	mdl.CreatedBy = ""
	return lib.Create(mdl)
}

// Delete removes a library model.
func (s *ModelService) Delete(path string) error { return s.M.Store.Models().Delete(path) }

// Reveal shows a model file in Finder.
func (s *ModelService) Reveal(path string) error {
	return application.Get().Env.OpenFileManager(path, true)
}

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
