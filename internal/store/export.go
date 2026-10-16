package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"golang.org/x/crypto/scrypt"

	"chloe/internal/model"
)

// ExportFile is the portable connections export. Passwords are only included
// when a passphrase is given, and then only inside Secrets, encrypted with it.
type ExportFile struct {
	Format      string             `json:"format"` // "chloe-connections"
	Version     int                `json:"version"`
	Groups      []string           `json:"groups"`
	Connections []model.Connection `json:"connections"`
	Secrets     *SealedSecrets     `json:"secrets,omitempty"`
}

// SealedSecrets holds each connection's passwords (in Connections order) as JSON
// encrypted with AES-256-GCM under a key derived from the passphrase with scrypt.
type SealedSecrets struct {
	KDF   string `json:"kdf"` // "scrypt"
	N     int    `json:"n"`
	R     int    `json:"r"`
	P     int    `json:"p"`
	Salt  []byte `json:"salt"`
	Nonce []byte `json:"nonce"`
	Data  []byte `json:"data"`
}

type secretSet struct {
	Password      string `json:"password,omitempty"`
	SSHPassword   string `json:"sshPassword,omitempty"`
	SSHPassphrase string `json:"sshPassphrase,omitempty"`
}

// ErrPassphrase means the passphrase doesn't open an export's passwords.
var ErrPassphrase = errors.New("wrong passphrase")

const (
	exportFormat  = "chloe-connections"
	exportVersion = 2
	scryptN       = 1 << 15
	scryptR       = 8
	scryptP       = 1
)

// additional data bound to the ciphertext, so it can't be moved to another format.
var sealAAD = []byte("chloe-connections/2")

func sealKey(passphrase string, salt []byte, n, r, p int) ([]byte, error) {
	return scrypt.Key([]byte(passphrase), salt, n, r, p, 32)
}

func seal(passphrase string, sets []secretSet) (*SealedSecrets, error) {
	plain, err := json.Marshal(sets)
	if err != nil {
		return nil, err
	}
	out := &SealedSecrets{KDF: "scrypt", N: scryptN, R: scryptR, P: scryptP, Salt: make([]byte, 16), Nonce: make([]byte, 12)}
	if _, err := rand.Read(out.Salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(out.Nonce); err != nil {
		return nil, err
	}
	key, err := sealKey(passphrase, out.Salt, out.N, out.R, out.P)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	out.Data = gcm.Seal(nil, out.Nonce, plain, sealAAD)
	return out, nil
}

func (s *SealedSecrets) open(passphrase string) ([]secretSet, error) {
	// Bound the work factor so a crafted file can't stall the app or exhaust memory.
	if s.KDF != "scrypt" || s.N < 2 || s.N > 1<<20 || s.R < 1 || s.R > 32 || s.P < 1 || s.P > 16 || len(s.Nonce) != 12 {
		return nil, errors.New("unsupported password encryption in this file")
	}
	key, err := sealKey(passphrase, s.Salt, s.N, s.R, s.P)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, s.Nonce, s.Data, sealAAD)
	if err != nil {
		return nil, ErrPassphrase
	}
	var sets []secretSet
	if err := json.Unmarshal(plain, &sets); err != nil {
		return nil, err
	}
	return sets, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Export serialises the given connections (all when ids is empty). With a
// passphrase, their saved passwords are included, encrypted with it.
func (s *Store) Export(ids []string, passphrase string) ([]byte, error) {
	f := ExportFile{Format: exportFormat, Version: exportVersion}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	groups := map[string]bool{}
	var sets []secretSet
	for _, c := range s.List() {
		if len(ids) > 0 && !want[c.ID] {
			continue
		}
		if passphrase != "" {
			full, err := s.Get(c.ID)
			if err != nil {
				return nil, err
			}
			sets = append(sets, secretSet{full.Password, full.SSH.Password, full.SSH.Passphrase})
		}
		f.Connections = append(f.Connections, stripSecrets(c))
		if c.Group != "" {
			groups[c.Group] = true
		}
	}
	if passphrase != "" {
		sealed, err := seal(passphrase, sets)
		if err != nil {
			return nil, err
		}
		f.Secrets = sealed
	}
	for g := range groups {
		f.Groups = append(f.Groups, g)
	}
	slices.Sort(f.Groups)
	return json.MarshalIndent(f, "", "  ")
}

// ExportSummary describes an export file before it is imported.
type ExportSummary struct {
	Connections int `json:"connections"`
	// Encrypted is set when the file has passwords that need a passphrase.
	Encrypted bool `json:"encrypted"`
}

func parseExport(data []byte) (ExportFile, error) {
	var f ExportFile
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("not a Chloe connections file: %w", err)
	}
	if f.Format != exportFormat {
		return f, errors.New("not a Chloe connections file")
	}
	if f.Version > exportVersion {
		return f, fmt.Errorf("this connections file needs a newer version of Chloe (format %d)", f.Version)
	}
	return f, nil
}

// Inspect reads an export's header.
func Inspect(data []byte) (ExportSummary, error) {
	f, err := parseExport(data)
	return ExportSummary{Connections: len(f.Connections), Encrypted: f.Secrets != nil}, err
}

// withoutLaunchSettings clears what would act without the user looking at the profile:
// SQL run on every session, connecting at startup, extra Postgres parameters (they can
// redirect the host) and sending the password in the clear. A file from someone else
// shouldn't bring them in; the user turns them back on in the connection dialog.
func withoutLaunchSettings(c model.Connection) model.Connection {
	c.Advanced.AutoConnect = false
	c.Advanced.InitialQueries = ""
	c.Advanced.ConnectionParams = ""
	c.Advanced.CleartextAuth = false
	return c
}

// Import adds connections from an export. Imported profiles always get new IDs;
// name clashes get a numeric suffix. Encrypted passwords are restored with the
// passphrase; without one they are skipped. Settings that act on their own are
// cleared (withoutLaunchSettings). Returns the number imported.
func (s *Store) Import(data []byte, passphrase string) (int, error) {
	f, err := parseExport(data)
	if err != nil {
		return 0, err
	}
	var sets []secretSet
	if f.Secrets != nil && passphrase != "" {
		if sets, err = f.Secrets.open(passphrase); err != nil {
			return 0, err
		}
	}
	for _, g := range f.Groups {
		_ = s.AddGroup(g) // existing groups are fine
	}
	n := 0
	for i, c := range f.Connections {
		c.ID = ""
		c.Name = s.uniqueName(c.Name)
		c = withoutLaunchSettings(c)
		if f.Secrets != nil {
			// Passwords only come from the sealed block; version 1 files kept them in the clear.
			c = stripSecrets(c)
			if i < len(sets) {
				c.Password, c.SSH.Password, c.SSH.Passphrase = sets[i].Password, sets[i].SSHPassword, sets[i].SSHPassphrase
			}
		}
		if _, err := s.Save(c); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
