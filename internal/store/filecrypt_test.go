package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chloe/internal/model"
)

// persistentSecrets behaves like the OS keychain: it outlives the store.
type persistentSecrets struct{ MemorySecrets }

func (*persistentSecrets) Ephemeral() bool { return false }

func TestEncryptedConnectionsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connections.json")
	// A plain file from an earlier release is encrypted on open.
	os.WriteFile(path, []byte(`{"version":1,"groups":["Prod"],"connections":[{"id":"a","name":"orders","type":"mysql","host":"db.internal","user":"admin","group":"Prod"}]}`), 0o600)
	kc := &persistentSecrets{}
	s, err := Open(dir, kc)
	if err != nil || s.Locked() != "" {
		t.Fatal(err, s.Locked())
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "db.internal") || strings.Contains(string(raw), "admin") || !isSealed(raw) {
		t.Fatalf("file not encrypted: %s", raw)
	}
	if _, err := s.Save(model.Connection{Name: "second", Type: model.PostgreSQL, Host: "pg.internal"}); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "pg.internal") {
		t.Fatal("save wrote plain text")
	}

	s2, err := Open(dir, kc)
	if err != nil || s2.Locked() != "" || len(s2.List()) != 2 || s2.Groups()[0] != "Prod" {
		t.Fatalf("reopen: %v %q %+v", err, s2.Locked(), s2.List())
	}

	// Without the key the store is locked: empty, and it never overwrites the file.
	before, _ := os.ReadFile(path)
	s3, err := Open(dir, &persistentSecrets{})
	if err != nil || s3.Locked() == "" || len(s3.List()) != 0 {
		t.Fatalf("locked: %v %q %d", err, s3.Locked(), len(s3.List()))
	}
	if _, err := s3.Save(model.Connection{Name: "x", Type: model.MySQL}); err == nil {
		t.Fatal("a locked store saved")
	}
	if after, _ := os.ReadFile(path); string(after) != string(before) {
		t.Fatal("a locked store changed the file")
	}
	// The original key still opens it.
	if s4, _ := Open(dir, kc); len(s4.List()) != 2 {
		t.Fatal("file unreadable after a locked open")
	}
}

func TestEphemeralSecretsKeepPlainFile(t *testing.T) {
	s, dir, _ := newStore(t)
	s.Save(model.Connection{Name: "a", Type: model.MySQL, Host: "h1"})
	raw, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
	if !strings.Contains(string(raw), "h1") {
		t.Fatal("memory secrets should keep a readable file for tests")
	}
}
