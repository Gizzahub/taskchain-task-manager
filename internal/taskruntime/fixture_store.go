package taskruntime

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// pinFixture reuses the existing lock implementation through descriptor-relative
// filesystem operations. The state directory must already exist: binding never
// initializes an execution ledger, even in a synthetic sandbox.
func (s stateStore) pinFixture(sandboxRoot *os.Root, sandbox string) (stateStore, func() error, error) {
	rel, err := filepath.Rel(sandbox, s.dir)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return stateStore{}, nil, ErrFixtureBoundary
	}
	path := ""
	var before os.FileInfo
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		before, err = sandboxRoot.Lstat(path)
		if err != nil || !fixtureSafeDirectory(before) {
			return stateStore{}, nil, ErrFixtureBoundary
		}
	}
	pinned, err := sandboxRoot.OpenRoot(rel)
	if err != nil {
		return stateStore{}, nil, ErrFixtureBoundary
	}
	opened, err := pinned.Stat(".")
	if err != nil || !os.SameFile(before, opened) || !fixtureSafeDirectory(opened) {
		_ = pinned.Close()
		return stateStore{}, nil, ErrFixtureBoundary
	}
	s.fixtureRoot = pinned
	check := func() error {
		named, err := sandboxRoot.Lstat(rel)
		if err != nil || !fixtureSafeDirectory(named) || !os.SameFile(named, opened) {
			return ErrFixtureBoundary
		}
		return nil
	}
	return s, check, nil
}

func (s stateStore) fixtureName(path string) (string, error) {
	if filepath.Dir(path) != s.dir {
		return "", ErrFixtureBoundary
	}
	return filepath.Base(path), nil
}

func (s stateStore) fsOpenFile(path string, flags int, mode os.FileMode) (*os.File, error) {
	if s.fixtureRoot == nil {
		return os.OpenFile(path, flags, mode)
	}
	name, err := s.fixtureName(path)
	if err != nil {
		return nil, err
	}
	before, err := s.fixtureRoot.Lstat(name)
	if err != nil && !os.IsNotExist(err) {
		return nil, ErrFixtureBoundary
	}
	if err == nil && !before.Mode().IsRegular() {
		return nil, ErrFixtureBoundary
	}
	f, err := s.fixtureRoot.OpenFile(name, fixtureOpenFlags(flags), mode)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || (before != nil && !os.SameFile(before, opened)) {
		_ = f.Close()
		return nil, ErrFixtureBoundary
	}
	return f, nil
}

func (s stateStore) fsReadFile(path string) ([]byte, error) {
	if s.fixtureRoot == nil {
		return os.ReadFile(path)
	}
	f, err := s.fsOpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (s stateStore) fsRemove(path string) error {
	if s.fixtureRoot == nil {
		return os.Remove(path)
	}
	name, err := s.fixtureName(path)
	if err != nil {
		return err
	}
	return s.fixtureRoot.Remove(name)
}

func (s stateStore) fsStat(path string) (os.FileInfo, error) {
	if s.fixtureRoot == nil {
		return os.Stat(path)
	}
	name, err := s.fixtureName(path)
	if err != nil {
		return nil, err
	}
	return s.fixtureRoot.Stat(name)
}

func (s stateStore) fsSyncDir() error {
	if s.fixtureRoot == nil {
		return syncDir(s.dir)
	}
	f, err := s.fixtureRoot.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
