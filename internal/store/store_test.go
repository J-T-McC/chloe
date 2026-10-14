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
	if g := s2.Groups(); len(g) != 2 || g[0] != "Empty" || g[1] != "Clients" {
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

func TestOrderAndMove(t *testing.T) {
	s, dir, sec := newStore(t)
	save := func(name, group string) string {
		c, err := s.Save(model.Connection{Name: name, Type: model.SQLite, Group: group})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	a, b := save("a", ""), save("b", "G1")
	c, d := save("c", ""), save("d", "G1")
	s.AddGroup("G2")
	names := func(s *Store) string {
		var out []string
		for _, c := range s.List() {
			out = append(out, c.Group+"/"+c.Name)
		}
		return strings.Join(out, " ") + " | " + strings.Join(s.Groups(), " ")
	}
	if got := names(s); got != "/a /c G1/b G1/d | G1 G2" {
		t.Fatalf("order %q", got)
	}
	// Reorder within the top level, into a group before another, and to the end of a group.
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Move(c, "", a))
	must(s.Move(a, "G1", d))
	must(s.Move(b, "G2", ""))
	must(s.MoveGroup("G2", "G1"))
	want := "/c G1/a G1/d G2/b | G2 G1"
	if got := names(s); got != want {
		t.Fatalf("after moves %q, want %q", got, want)
	}
	// A before that isn't in the target group means "last in the group".
	must(s.Move(d, "", b))
	if got := names(s); got != "/c /d G1/a G2/b | G2 G1" {
		t.Fatalf("move with foreign before %q", got)
	}
	must(s.Move(d, "G1", ""))
	s2, err := Open(dir, sec)
	if err != nil {
		t.Fatal(err)
	}
	if got := names(s2); got != want {
		t.Fatalf("reopened %q, want %q", got, want)
	}
}

func TestVersion1SortsByGroupThenName(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "connections.json"), []byte(`{"version":1,"groups":["Z","A"],"connections":[
		{"id":"1","name":"beta","type":"sqlite","group":"Z"},{"id":"2","name":"Alpha","type":"sqlite","group":"Z"},{"id":"3","name":"top","type":"sqlite"}]}`), 0o600)
	s, err := Open(dir, &MemorySecrets{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range s.List() {
		got = append(got, c.Name)
	}
	if strings.Join(got, ",") != "top,Alpha,beta" || strings.Join(s.Groups(), ",") != "A,Z" {
		t.Fatalf("%v %v", got, s.Groups())
	}
}
