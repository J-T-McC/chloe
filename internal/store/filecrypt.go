package store

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// connections.json is encrypted with a random key kept in the keychain, so other
// apps can't read which servers and accounts are saved without asking for it.

// Ephemeral secrets (tests, the browser build) don't outlive the process, so a
// file encrypted under them couldn't be read again: such stores stay in plain JSON.
type ephemeral interface{ Ephemeral() bool }

func (*MemorySecrets) Ephemeral() bool { return true }

const (
	fileKeyName = "connections-file-key"
	sealedFile  = "chloe-encrypted-connections"
)

var fileAAD = []byte("chloe-connections-file/1")

type sealedConnections struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"`
}

func isSealed(data []byte) bool {
	var h struct {
		Format string `json:"format"`
	}
	return json.Unmarshal(data, &h) == nil && h.Format == sealedFile
}

func (s *Store) encrypts() bool {
	e, ok := s.secrets.(ephemeral)
	return !ok || !e.Ephemeral()
}

// fileKey returns the file key, creating it when create is set and there is none.
func (s *Store) fileKey(create bool) ([]byte, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.key != nil {
		return s.key, nil
	}
	v, err := s.secrets.Get(fileKeyName)
	if err == nil {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(key) != 32 {
			return nil, errors.New("the connections key in the keychain is damaged")
		}
		s.key = key
		return key, nil
	}
	if !create {
		return nil, err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := s.secrets.Set(fileKeyName, base64.StdEncoding.EncodeToString(key)); err != nil {
		return nil, err
	}
	s.key = key
	return key, nil
}

func (s *Store) sealFile(plain []byte) ([]byte, error) {
	key, err := s.fileKey(true)
	if err != nil {
		return nil, fmt.Errorf("keychain: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	f := sealedConnections{Format: sealedFile, Version: 1, Nonce: make([]byte, gcm.NonceSize())}
	if _, err := rand.Read(f.Nonce); err != nil {
		return nil, err
	}
	f.Data = gcm.Seal(nil, f.Nonce, plain, fileAAD)
	return json.MarshalIndent(f, "", "  ")
}

func (s *Store) openFile(data []byte) ([]byte, error) {
	var f sealedConnections
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	key, err := s.fileKey(false)
	if err != nil {
		return nil, fmt.Errorf("can't read the connections key from the keychain (%v)", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(f.Nonce) != gcm.NonceSize() {
		return nil, errors.New("connections file is damaged")
	}
	plain, err := gcm.Open(nil, f.Nonce, f.Data, fileAAD)
	if err != nil {
		return nil, errors.New("the connections key in the keychain doesn't open this file")
	}
	return plain, nil
}

// sealedDataMagic starts data sealed with SealData.
var sealedDataMagic = []byte("chloe-sealed-data/1\n")

// SealData encrypts rows Chloe keeps on disk (BI archives and snapshots) with
// the keychain key; purpose binds the data to its use. Stores that don't
// encrypt (tests, the browser build) keep it plain.
func (s *Store) SealData(plain []byte, purpose string) ([]byte, error) {
	if !s.encrypts() {
		return plain, nil
	}
	key, err := s.fileKey(true)
	if err != nil {
		return nil, fmt.Errorf("keychain: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append(append(slices.Clone(sealedDataMagic), nonce...), gcm.Seal(nil, nonce, plain, []byte(purpose))...)
	return out, nil
}

// OpenData reverses SealData; unsealed data is returned as it is.
func (s *Store) OpenData(data []byte, purpose string) ([]byte, error) {
	if !bytes.HasPrefix(data, sealedDataMagic) {
		return data, nil
	}
	data = data[len(sealedDataMagic):]
	key, err := s.fileKey(false)
	if err != nil {
		return nil, fmt.Errorf("can't read Chloe's key from the keychain (%v)", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(data) < gcm.NonceSize() {
		return nil, errors.New("the file is damaged")
	}
	plain, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], []byte(purpose))
	if err != nil {
		return nil, errors.New("Chloe's key in the keychain doesn't open this file")
	}
	return plain, nil
}
