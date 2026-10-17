// Package safefile writes files that only the user can read, atomically: the
// data goes to a new temporary file next to the target (never an existing path,
// so a planted symlink isn't followed), is synced, made 0600 and renamed over
// the target. A failed write leaves the old file as it was.
package safefile

import (
	"errors"
	"os"
	"path/filepath"
)

// Mode is the permission every file written here gets.
const Mode os.FileMode = 0o600

// File is a pending write to a path. Commit puts it in place; Close without
// Commit throws it away.
type File struct {
	*os.File
	path string
	done bool
}

// Create starts a write to path.
func Create(path string) (*File, error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return nil, err
	}
	return &File{File: f, path: path}, nil
}

// Commit syncs the data and renames it over the target.
func (f *File) Commit() error {
	if f.done {
		return errors.New("safefile: already closed")
	}
	f.done = true
	tmp := f.File.Name()
	err := f.File.Sync()
	if err == nil {
		err = f.File.Chmod(Mode)
	}
	if cerr := f.File.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, f.path)
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	syncDir(filepath.Dir(f.path))
	return nil
}

// Close discards an uncommitted write. It does nothing after Commit.
func (f *File) Close() error {
	if f.done {
		return nil
	}
	f.done = true
	err := f.File.Close()
	os.Remove(f.File.Name())
	return err
}

// Write replaces path with data.
func Write(path string, data []byte) error {
	f, err := Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.File.Write(data); err != nil {
		return err
	}
	return f.Commit()
}

// syncDir makes the rename durable where the platform allows syncing a
// directory (it can't on Windows, and that's fine).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
