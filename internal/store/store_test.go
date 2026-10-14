package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"chloe/internal/model"
)

func newStore(t *testing.T) (*Store, string, *MemorySecrets) {
	t.Helper()
	dir := t.TempDir()
	sec := &MemorySecrets{}
	s, err := Open(dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir, sec
}

func TestSecretsNeverHitDisk(t *testing.T) {
	s, dir, sec := newStore(t)
	c, err := s.Save(model.Connection{Name: "prod", Type: model.MySQL, Password: "hunter2", SavePassword: true,
		SSH: model.SSHConfig{Enabled: true, Password: "sshpw", SavePassword: true}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "connections.json"))
	for _, secret := range []string{"hunter2", "sshpw"} {
		if contains(data, secret) {
			t.Fatalf("secret %q written to disk", secret)
		}
	}
	got, _ := s.Get(c.ID)
	if got.Password != "hunter2" || got.SSH.Password != "sshpw" {
		t.Fatalf("secrets not restored: %+v", got)
	}
	// Unchecking "save password" removes the secret.
	got.SavePassword = false
	s.Save(got)
	if v, _ := sec.Get(secretKey(c.ID, "password")); v != "" {
		t.Fatal("secret kept after save-password was unchecked")
	}
}

func TestGroupsPersistAndRename(t *testing.T) {
	s, dir, sec := newStore(t)
	if err := s.AddGroup("Empty"); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Save(model.Connection{Name: "a", Type: model.MySQL, Group: "Work"})
	if err := s.RenameGroup("Work", "Clients"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	if g := s2.Groups(); len(g) != 2 || g[0] != "Clients" || g[1] != "Empty" {
		t.Fatalf("groups %v", g)
	}
	got, _ := s2.Get(c.ID)
	if got.Group != "Clients" {
		t.Fatalf("connection group %q", got.Group)
	}
	s2.DeleteGroup("Clients")
	got, _ = s2.Get(c.ID)
	if got.Group != "" {
		t.Fatal("delete group should move connections to top level")
	}
}

func TestDuplicateAndImportExport(t *testing.T) {
	s, _, _ := newStore(t)
	c, _ := s.Save(model.Connection{Name: "db", Type: model.PostgreSQL, Password: "pw", SavePassword: true, Group: "G"})
	d, err := s.Duplicate(c.ID)
	if err != nil || d.Name != "db copy" || d.ID == c.ID {
		t.Fatalf("duplicate %+v %v", d, err)
	}
	if full, _ := s.Get(d.ID); full.Password != "pw" {
		t.Fatal("duplicate lost its password")
	}

	noSecrets, _ := s.Export(nil, false)
	if contains(noSecrets, "\"pw\"") {
		t.Fatal("export without secrets leaked a password")
	}
	withSecrets, _ := s.Export([]string{c.ID}, true)

	s2, _, _ := newStore(t)
	n, err := s2.Import(withSecrets)
	if err != nil || n != 1 {
		t.Fatalf("import %d %v", n, err)
	}
	list := s2.List()
	if got, _ := s2.Get(list[0].ID); got.Password != "pw" || got.Group != "G" {
		t.Fatalf("imported %+v", got)
	}
	if _, err := s2.Import([]byte(`{"hello":1}`)); err == nil {
		t.Fatal("expected error for foreign file")
	}
}

func TestLegacyArrayFormat(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "connections.json"), []byte(`[{"id":"x","name":"old","type":"mysql","group":"G"}]`), 0o600)
	s, err := Open(dir, &MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 1 || s.Groups()[0] != "G" {
		t.Fatalf("legacy load %+v %v", s.List(), s.Groups())
	}
}

func contains(b []byte, s string) bool { return strings.Contains(string(b), s) }
