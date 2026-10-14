package store

import (
	"errors"
	"sync"

	"github.com/zalando/go-keyring"
)

const keyringService = "app.chloe.desktop"

// Keyring stores secrets in the macOS Keychain / Windows Credential Manager / Secret Service.
type Keyring struct{}

func (Keyring) Get(key string) (string, error) { return keyring.Get(keyringService, key) }
func (Keyring) Set(key, value string) error    { return keyring.Set(keyringService, key, value) }
func (Keyring) Delete(key string) error {
	err := keyring.Delete(keyringService, key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// MemorySecrets is an in-memory Secrets implementation for tests.
type MemorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *MemorySecrets) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[key]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (s *MemorySecrets) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[key] = value
	return nil
}

func (s *MemorySecrets) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, key)
	return nil
}
