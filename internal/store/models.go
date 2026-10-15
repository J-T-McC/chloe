package store

import (
	"path/filepath"

	"chloe/internal/datamodel"
)

// Models is the folder of data models Chloe manages.
func (s *Store) Models() datamodel.Library {
	return datamodel.Library{Dir: filepath.Join(filepath.Dir(s.path), "models")}
}

// Dir is Chloe's config folder.
func (s *Store) Dir() string { return filepath.Dir(s.path) }
