// Package store persists connection profiles to disk and their secrets to the OS keychain.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/google/uuid"

	"chloe/internal/model"
)

// Secrets abstracts the OS keychain so tests can substitute an in-memory map.
type Secrets interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// ErrNotFound is returned when a connection ID does not exist.
var ErrNotFound = errors.New("connection not found")

type Store struct {
	mu      sync.RWMutex
	path    string
	secrets Secrets
	conns   map[string]model.Connection
}

// Open loads (or creates) the connection file in dir.
func Open(dir string, secrets Secrets) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "connections.json"), secrets: secrets, conns: map[string]model.Connection{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []model.Connection
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	for _, c := range list {
		s.conns[c.ID] = c
	}
	return s, nil
}

// List returns all connections without secrets, sorted by group then name.
func (s *Store) List() []model.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Connection, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Group != out[j].Group {
			return out[i].Group < out[j].Group
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Get returns a connection with its saved secrets filled in.
func (s *Store) Get(id string) (model.Connection, error) {
	s.mu.RLock()
	c, ok := s.conns[id]
	s.mu.RUnlock()
	if !ok {
		return c, ErrNotFound
	}
	if c.SavePassword {
		c.Password, _ = s.secrets.Get(secretKey(id, "password"))
	}
	if c.SSH.Enabled && c.SSH.SavePassword {
		c.SSH.Password, _ = s.secrets.Get(secretKey(id, "ssh-password"))
		c.SSH.Passphrase, _ = s.secrets.Get(secretKey(id, "ssh-passphrase"))
	}
	return c, nil
}

// Save inserts or updates a connection. Secrets are moved to the keychain.
func (s *Store) Save(c model.Connection) (model.Connection, error) {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Name == "" {
		return c, errors.New("connection name is required")
	}
	if err := s.storeSecret(c.ID, "password", c.Password, c.SavePassword); err != nil {
		return c, err
	}
	if err := s.storeSecret(c.ID, "ssh-password", c.SSH.Password, c.SSH.SavePassword); err != nil {
		return c, err
	}
	if err := s.storeSecret(c.ID, "ssh-passphrase", c.SSH.Passphrase, c.SSH.SavePassword); err != nil {
		return c, err
	}
	stripped := c
	stripped.Password, stripped.SSH.Password, stripped.SSH.Passphrase = "", "", ""

	s.mu.Lock()
	s.conns[c.ID] = stripped
	err := s.flushLocked()
	s.mu.Unlock()
	return stripped, err
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	if _, ok := s.conns[id]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.conns, id)
	err := s.flushLocked()
	s.mu.Unlock()
	for _, k := range []string{"password", "ssh-password", "ssh-passphrase"} {
		_ = s.secrets.Delete(secretKey(id, k))
	}
	return err
}

func (s *Store) storeSecret(id, name, value string, save bool) error {
	key := secretKey(id, name)
	if !save || value == "" {
		_ = s.secrets.Delete(key)
		return nil
	}
	return s.secrets.Set(key, value)
}

func (s *Store) flushLocked() error {
	list := make([]model.Connection, 0, len(s.conns))
	for _, c := range s.conns {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func secretKey(id, name string) string { return id + ":" + name }
