// Package store persists connection profiles to disk and their secrets to the OS keychain.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/google/uuid"

	"chloe/internal/model"
	"chloe/internal/safefile"
)

// Secrets abstracts the OS keychain so tests can substitute an in-memory map.
type Secrets interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// ErrNotFound is returned when a connection ID does not exist.
var ErrNotFound = errors.New("connection not found")

var secretNames = []string{"password", "ssh-password", "ssh-passphrase"}

type Store struct {
	mu      sync.RWMutex
	path    string
	secrets Secrets
	conns   map[string]model.Connection
	groups  []string // folders, in tree order
	order   []string // connection IDs, in tree order
	keyMu   sync.Mutex
	key     []byte // file key, once read from the keychain
	work    *Workspace
	mcpMu   sync.Mutex // mcp.json
	mcpHook func(MCPSettings)
	// locked explains why an encrypted connections.json couldn't be read. The
	// store is then empty and never writes, so the file is left as it was.
	locked string
}

// fileFormat is the on-disk layout of connections.json. Since version 2 the
// groups and connections are in the order the tree shows them.
type fileFormat struct {
	Version     int                `json:"version"`
	Groups      []string           `json:"groups"`
	Connections []model.Connection `json:"connections"`
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
	sealed := isSealed(data)
	if !sealed {
		if err := s.refusePlain(filepath.Base(s.path)); err != nil {
			s.locked = fmt.Sprintf("Saved connections are locked: %v. Chloe left %s untouched; restore it from a backup or remove it.", err, s.path)
			return s, nil
		}
	}
	if sealed {
		plain, err := s.openFile(filepath.Base(s.path), data)
		if err != nil {
			s.locked = fmt.Sprintf("Saved connections are locked: %v. Chloe left %s untouched; allow Chloe to use its keychain item and restart.", err, s.path)
			return s, nil
		}
		data = plain
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		// Early builds wrote a bare array of connections.
		if err2 := json.Unmarshal(data, &f.Connections); err2 != nil {
			return nil, fmt.Errorf("parse %s: %w", s.path, err)
		}
	}
	if f.Version < 2 {
		// Earlier releases sorted the tree by group, then name.
		slices.Sort(f.Groups)
		sort.SliceStable(f.Connections, func(i, j int) bool {
			a, b := f.Connections[i], f.Connections[j]
			if a.Group != b.Group {
				return a.Group < b.Group
			}
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		})
	}
	for _, c := range f.Connections {
		if _, dup := s.conns[c.ID]; !dup {
			s.order = append(s.order, c.ID)
		}
		s.conns[c.ID] = c
	}
	s.groups = f.Groups
	s.addGroupsFromConnectionsLocked()
	if !sealed && s.encrypts() {
		_ = s.flushLocked() // encrypt a file written by an earlier release
	}
	return s, nil
}

// Locked explains why saved connections couldn't be read ("" when they were).
func (s *Store) Locked() string { return s.locked }

// List returns all connections without secrets, in tree order.
func (s *Store) List() []model.Connection {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.Connection, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.conns[id])
	}
	return out
}

// Groups returns the connection folders in tree order.
func (s *Store) Groups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.groups)
}

// Profile returns a connection without reading its secrets from the keychain,
// for callers that only need its name, type or settings.
func (s *Store) Profile(id string) (model.Connection, error) {
	s.mu.RLock()
	c, ok := s.conns[id]
	s.mu.RUnlock()
	if !ok {
		return c, ErrNotFound
	}
	c.Password, c.SSH.Password, c.SSH.Passphrase = "", "", ""
	return c, nil
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
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		return c, errors.New("connection name is required")
	}
	c.Group = strings.TrimSpace(c.Group)
	if err := s.storeSecret(c.ID, "password", c.Password, c.SavePassword); err != nil {
		return c, err
	}
	if err := s.storeSecret(c.ID, "ssh-password", c.SSH.Password, c.SSH.SavePassword); err != nil {
		return c, err
	}
	if err := s.storeSecret(c.ID, "ssh-passphrase", c.SSH.Passphrase, c.SSH.SavePassword); err != nil {
		return c, err
	}
	stripped := stripSecrets(c)

	s.mu.Lock()
	defer s.mu.Unlock()
	old, exists := s.conns[c.ID]
	s.conns[c.ID] = stripped
	switch {
	case !exists:
		s.order = append(s.order, c.ID)
		s.placeLocked(c.ID, "") // last in its group
	case old.Group != stripped.Group:
		s.placeLocked(c.ID, "")
	}
	s.addGroupsFromConnectionsLocked()
	return stripped, s.flushLocked()
}

// Duplicate copies a connection (including its saved secrets) under a new name.
func (s *Store) Duplicate(id string) (model.Connection, error) {
	c, err := s.Get(id)
	if err != nil {
		return c, err
	}
	c.ID = ""
	c.Name = s.uniqueName(c.Name + " copy")
	return s.Save(c)
}

func (s *Store) uniqueName(base string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	taken := map[string]bool{}
	for _, c := range s.conns {
		taken[c.Name] = true
	}
	name := base
	for i := 2; taken[name]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	return name
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	if _, ok := s.conns[id]; !ok {
		s.mu.Unlock()
		return ErrNotFound
	}
	delete(s.conns, id)
	s.order = slices.DeleteFunc(s.order, func(o string) bool { return o == id })
	err := s.flushLocked()
	s.mu.Unlock()
	s.Workspace().forgetConnection(id)
	for _, k := range secretNames {
		_ = s.secrets.Delete(secretKey(id, k))
	}
	return err
}

// AddGroup creates an empty folder.
func (s *Store) AddGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("group name is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.groups, name) {
		return fmt.Errorf("group %q already exists", name)
	}
	s.groups = append(s.groups, name)
	return s.flushLocked()
}

// RenameGroup renames a folder and moves its connections (and MCP access) with it.
func (s *Store) RenameGroup(old, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("group name is required")
	}
	if err := s.renameGroup(old, name); err != nil {
		return err
	}
	if name != old {
		s.renameMCPGroup(old, name)
	}
	return nil
}

func (s *Store) renameGroup(old, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if name != old && slices.Contains(s.groups, name) {
		return fmt.Errorf("group %q already exists", name)
	}
	for i, g := range s.groups {
		if g == old {
			s.groups[i] = name
		}
	}
	s.groups = slices.Compact(s.groups)
	for id, c := range s.conns {
		if c.Group == old {
			c.Group = name
			s.conns[id] = c
		}
	}
	return s.flushLocked()
}

// DeleteGroup removes a folder; its connections move to the top level and
// MCP endpoints lose the access they had through it.
func (s *Store) DeleteGroup(name string) error {
	s.mu.Lock()
	s.groups = slices.DeleteFunc(s.groups, func(g string) bool { return g == name })
	for id, c := range s.conns {
		if c.Group == name {
			c.Group = ""
			s.conns[id] = c
		}
	}
	err := s.flushLocked()
	s.mu.Unlock()
	if err == nil {
		s.renameMCPGroup(name, "")
	}
	return err
}

// MoveToGroup moves a connection to the end of a folder ("" = top level).
func (s *Store) MoveToGroup(id, group string) error {
	return s.Move(id, group, "")
}

// Move puts a connection in a folder ("" = top level), just before the
// connection before, or last in the folder when before is "" or elsewhere.
func (s *Store) Move(id, group, before string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.conns[id]
	if !ok {
		return ErrNotFound
	}
	c.Group = strings.TrimSpace(group)
	s.conns[id] = c
	if b, ok := s.conns[before]; !ok || b.Group != c.Group || before == id {
		before = ""
	}
	s.placeLocked(id, before)
	s.addGroupsFromConnectionsLocked()
	return s.flushLocked()
}

// MoveGroup puts a folder just before the folder before ("" = last).
func (s *Store) MoveGroup(name, before string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.groups, name) {
		return fmt.Errorf("group %q not found", name)
	}
	if name == before {
		return nil
	}
	s.groups = slices.DeleteFunc(s.groups, func(g string) bool { return g == name })
	i := slices.Index(s.groups, before)
	if i < 0 {
		i = len(s.groups)
	}
	s.groups = slices.Insert(s.groups, i, name)
	return s.flushLocked()
}

// placeLocked moves id in the order to just before before, or after the last
// other connection in its group (at the end when the group has no others).
func (s *Store) placeLocked(id, before string) {
	s.order = slices.DeleteFunc(s.order, func(o string) bool { return o == id })
	i := slices.Index(s.order, before)
	if i < 0 {
		i = len(s.order)
		group := s.conns[id].Group
		for j := len(s.order) - 1; j >= 0; j-- {
			if s.conns[s.order[j]].Group == group {
				i = j + 1
				break
			}
		}
	}
	s.order = slices.Insert(s.order, i, id)
}

func (s *Store) storeSecret(id, name, value string, save bool) error {
	key := secretKey(id, name)
	if !save || value == "" {
		_ = s.secrets.Delete(key)
		return nil
	}
	return s.secrets.Set(key, value)
}

func (s *Store) addGroupsFromConnectionsLocked() {
	for _, id := range s.order {
		if c := s.conns[id]; c.Group != "" && !slices.Contains(s.groups, c.Group) {
			s.groups = append(s.groups, c.Group)
		}
	}
}

func (s *Store) flushLocked() error {
	if s.locked != "" {
		return errors.New(s.locked)
	}
	f := fileFormat{Version: 2, Groups: s.groups, Connections: make([]model.Connection, 0, len(s.order))}
	for _, id := range s.order {
		f.Connections = append(f.Connections, s.conns[id])
	}
	if f.Groups == nil {
		f.Groups = []string{}
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if s.encrypts() {
		if data, err = s.sealFile(filepath.Base(s.path), data); err != nil {
			return err
		}
	}
	return safefile.Write(s.path, data)
}

func stripSecrets(c model.Connection) model.Connection {
	c.Password, c.SSH.Password, c.SSH.Passphrase = "", "", ""
	return c
}

func secretKey(id, name string) string { return id + ":" + name }
