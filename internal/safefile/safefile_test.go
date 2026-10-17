package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("content %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != Mode {
			t.Fatalf("mode %v, want %v", fi.Mode().Perm(), Mode)
		}
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("left files behind: %v", es)
	}
}

func TestPlantedTmpSymlinkNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	os.WriteFile(victim, []byte("keep"), 0o644)
	p := filepath.Join(dir, "a.json")
	// The old writer used a fixed "<name>.tmp".
	os.Symlink(victim, p+".tmp")
	if err := Write(p, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("followed the symlink: victim = %q", b)
	}
}

func TestCloseWithoutCommitKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "dump.sql")
	os.WriteFile(p, []byte("old"), 0o600)
	f, err := Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("half"))
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "old" {
		t.Fatalf("content %q", b)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("left files behind: %v", es)
	}
	if err := f.Commit(); err == nil {
		t.Fatal("commit after close succeeded")
	}
}

func TestMissingDir(t *testing.T) {
	err := Write(filepath.Join(t.TempDir(), "no", "a"), nil)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v", err)
	}
}
