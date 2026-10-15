package store

import (
	"encoding/json"
	"path/filepath"
	"slices"
)

const recentModelsKey = "models:recent"

// RecentModels lists recently opened or saved model files, newest first.
func (s *Store) RecentModels() []string {
	doc, _ := s.Layouts().Get(recentModelsKey)
	var paths []string
	_ = json.Unmarshal([]byte(doc), &paths)
	return paths
}

// RememberModel puts a model file first in the recent list (10 at most).
func (s *Store) RememberModel(path string) {
	paths := slices.DeleteFunc(s.RecentModels(), func(p string) bool { return p == path })
	paths = append([]string{path}, paths...)
	if len(paths) > 10 {
		paths = paths[:10]
	}
	s.setRecentModels(paths)
}

// ForgetModel drops a model file from the recent list.
func (s *Store) ForgetModel(path string) {
	s.setRecentModels(slices.DeleteFunc(s.RecentModels(), func(p string) bool { return p == path }))
}

func (s *Store) setRecentModels(paths []string) {
	data, _ := json.Marshal(paths)
	_ = s.Layouts().Set(recentModelsKey, string(data))
}

// AgentModelDir is where models created over MCP are saved.
func (s *Store) AgentModelDir() string { return filepath.Join(filepath.Dir(s.path), "models") }
