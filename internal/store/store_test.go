package store

import (
	"errors"
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

	noSecrets, _ := s.Export(nil, "")
	if contains(noSecrets, "\"pw\"") {
		t.Fatal("export without secrets leaked a password")
	}
	if sum, _ := Inspect(noSecrets); sum.Encrypted || sum.Connections != 2 {
		t.Fatalf("inspect %+v", sum)
	}
	withSecrets, _ := s.Export([]string{c.ID}, "correct horse")
	if contains(withSecrets, "\"pw\"") {
		t.Fatal("export with secrets wrote a password in the clear")
	}
	if sum, _ := Inspect(withSecrets); !sum.Encrypted || sum.Connections != 1 {
		t.Fatalf("inspect %+v", sum)
	}

	s2, _, _ := newStore(t)
	if _, err := s2.Import(withSecrets, "wrong"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("wrong passphrase: %v", err)
	}
	if len(s2.List()) != 0 {
		t.Fatal("a failed import added connections")
	}
	n, err := s2.Import(withSecrets, "correct horse")
	if err != nil || n != 1 {
		t.Fatalf("import %d %v", n, err)
	}
	list := s2.List()
	if got, _ := s2.Get(list[0].ID); got.Password != "pw" || got.Group != "G" {
		t.Fatalf("imported %+v", got)
	}

	// Without the passphrase the connections come in without passwords.
	s3, _, _ := newStore(t)
	if n, err := s3.Import(withSecrets, ""); err != nil || n != 1 {
		t.Fatalf("import without passphrase %d %v", n, err)
	}
	if got, _ := s3.Get(s3.List()[0].ID); got.Password != "" {
		t.Fatal("password restored without the passphrase")
	}

	// Version 1 exports kept passwords in the clear; they still import.
	v1 := `{"format":"chloe-connections","version":1,"connections":[{"name":"old","type":"mysql","password":"pw1","savePassword":true}]}`
	s4, _, _ := newStore(t)
	if n, err := s4.Import([]byte(v1), ""); err != nil || n != 1 {
		t.Fatalf("v1 import %d %v", n, err)
	}
	if got, _ := s4.Get(s4.List()[0].ID); got.Password != "pw1" {
		t.Fatalf("v1 password %q", got.Password)
	}

	if _, err := s2.Import([]byte(`{"hello":1}`), ""); err == nil {
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
