package declaration

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

var ErrUnavailable = errors.New("declaration file unavailable or not owner-private")

// ReadFile is opt-in and read-only. root must be a caller-selected owner-private
// collection, not the existing executions/receipts directory. There is no path
// discovery, creation, chmod, migration, retry, or provider invocation.
func ReadFile(root, name string) (Record, error) {
	var zero Record
	if !absolute(root) || filepath.Base(name) != name || !text(name, 256) || name == "." || name == ".." {
		return zero, ErrUnavailable
	}
	before, err := os.Lstat(root)
	if err != nil || !before.IsDir() || before.Mode()&os.ModeSymlink != 0 || !ownerPrivate(before, 0o700) {
		return zero, ErrUnavailable
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return zero, ErrUnavailable
	}
	defer r.Close()
	// Pin the opened root and reject replacement during open. Subsequent opens
	// are confined to that descriptor, including when ancestors are renamed.
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(before, opened) || !ownerPrivate(opened, 0o700) {
		return zero, ErrUnavailable
	}
	before, err = r.Lstat(name)
	if err != nil || !before.Mode().IsRegular() || !ownerPrivate(before, 0o600) {
		return zero, ErrUnavailable
	}
	f, err := r.OpenFile(name, readFlags(), 0)
	if err != nil {
		return zero, ErrUnavailable
	}
	defer f.Close()
	opened, err = f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !ownerPrivate(opened, 0o600) || opened.Size() > MaxBytes {
		return zero, ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return zero, ErrUnavailable
	}
	return Decode(data)
}
