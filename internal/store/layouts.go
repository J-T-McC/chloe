package store

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

// Layouts persists small JSON documents (diagram positions, UI state) by key,
// one file each, next to the connections file.
type Layouts struct{ dir string }

func (s *Store) Layouts() *Layouts {
	return &Layouts{dir: filepath.Join(filepath.Dir(s.path), "layouts")}
}

func (l *Layouts) file(key string) string {
	h := sha1.Sum([]byte(key))
	return filepath.Join(l.dir, hex.EncodeToString(h[:])+".json")
}

// Get returns the stored document, or "" when there is none.
func (l *Layouts) Get(key string) (string, error) {
	b, err := os.ReadFile(l.file(key))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

func (l *Layouts) Set(key, doc string) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	tmp := l.file(key) + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.file(key))
}

func (l *Layouts) Delete(key string) error {
	err := os.Remove(l.file(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
