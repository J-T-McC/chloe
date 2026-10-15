package store

import (
	"path/filepath"

	"chloe/internal/bi"
	"chloe/internal/datamodel"
)

// Models is the folder of data models Chloe manages.
func (s *Store) Models() datamodel.Library {
	return datamodel.Library{Dir: filepath.Join(filepath.Dir(s.path), "models")}
}

// BI is the folder of BI workspaces Chloe manages.
func (s *Store) BI() bi.Library {
	return bi.Library{Dir: filepath.Join(filepath.Dir(s.path), "bi")}
}

// Dir is Chloe's config folder.
func (s *Store) Dir() string { return filepath.Dir(s.path) }
