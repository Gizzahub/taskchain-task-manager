package declaration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func (w *FixtureWriter) fail(stage string) error {
	if w.fault != nil {
		return w.fault(stage)
	}
	return nil
}

func (w *FixtureWriter) collection(common string) (*os.Root, error) {
	return w.openCollection(common, true)
}

func (w *FixtureWriter) collectionName() string {
	if w.restart {
		return "declaration-restart-fixture-v1"
	}
	return "declaration-fixture-v1"
}

func (w *FixtureWriter) openCollection(common string, create bool) (*os.Root, error) {
	r, err := taskruntime.OpenFixtureSandbox(w.sandbox)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	rel, err := filepath.Rel(w.sandbox, common)
	if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, ErrUnavailable
	}
	path := ""
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		path = filepath.Join(path, part)
		i, e := r.Lstat(path)
		if e != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0o022 != 0 || !ownerPrivate(i, i.Mode().Perm()) {
			return nil, ErrUnavailable
		}
	}
	parent, err := r.OpenRoot(rel)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer parent.Close()
	name := w.collectionName()
	if create {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, ErrFixtureStorage
		}
	}
	i, err := parent.Lstat(name)
	if err != nil || !i.IsDir() || !ownerPrivate(i, 0o700) {
		return nil, ErrUnavailable
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, ErrUnavailable
	}
	j, err := child.Stat(".")
	if err != nil || !os.SameFile(i, j) || !ownerPrivate(j, 0o700) {
		_ = child.Close()
		return nil, ErrUnavailable
	}
	if !create {
		return child, nil
	}
	// Retry parent sync even when the directory already exists after a crash.
	f, err := parent.Open(".")
	if err != nil {
		_ = child.Close()
		return nil, ErrFixtureStorage
	}
	err = f.Sync()
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		_ = child.Close()
		return nil, ErrFixtureDurability
	}
	return child, nil
}

func fixtureName(kind, id string) string {
	sum := sha256.Sum256([]byte(id)) // identifier only; never topic or conversation
	return kind + "-" + hex.EncodeToString(sum[:]) + ".json"
}

func (w *FixtureWriter) verifyCollection(common string, pinned *os.Root) error {
	r, err := taskruntime.OpenFixtureSandbox(w.sandbox)
	if err != nil {
		return ErrFixtureDurability
	}
	defer r.Close()
	rel, err := filepath.Rel(w.sandbox, filepath.Join(common, w.collectionName()))
	if err != nil {
		return ErrFixtureDurability
	}
	named, err := r.Lstat(rel)
	opened, openedErr := pinned.Stat(".")
	if err != nil || openedErr != nil || !named.IsDir() || !ownerPrivate(named, 0o700) || !os.SameFile(named, opened) {
		return ErrFixtureDurability
	}
	return nil
}

func (w *FixtureWriter) persist(r *os.Root, kind, id string, data []byte, compare func([]byte) error) (state string, err error) {
	name := fixtureName(kind, id)
	info, statErr := r.Lstat(name)
	if statErr == nil {
		if !info.Mode().IsRegular() || !ownerPrivate(info, 0o600) || info.Size() > MaxBytes {
			return "", ErrUnavailable
		}
		f, e := r.OpenFile(name, readFlags(), 0)
		if e != nil {
			return "", ErrUnavailable
		}
		defer f.Close()
		opened, e := f.Stat()
		if e != nil || !os.SameFile(info, opened) || !ownerPrivate(opened, 0o600) || !opened.Mode().IsRegular() {
			return "", ErrUnavailable
		}
		old, e := io.ReadAll(io.LimitReader(f, MaxBytes+1))
		if e != nil {
			return "", ErrFixtureStorage
		}
		if e = compare(old); e != nil {
			return "", e
		}
		if w.fail(kind+"-file-sync") != nil || f.Sync() != nil {
			return "", ErrFixtureDurability
		}
		if e = w.syncCollection(r, kind); e != nil {
			return "", e
		}
		return "duplicate", nil
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", ErrUnavailable
	}
	var nonce [16]byte
	if _, e := rand.Read(nonce[:]); e != nil {
		return "", ErrFixtureStorage
	}
	tmp := ".pending-" + hex.EncodeToString(nonce[:])
	if w.fail(kind+"-create") != nil {
		return "", ErrFixtureStorage
	}
	f, e := r.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if e != nil {
		return "", ErrFixtureStorage
	}
	defer func() {
		_ = f.Close()
		if e := r.Remove(tmp); e != nil && !errors.Is(e, os.ErrNotExist) {
			err = errors.Join(err, ErrFixtureStorage)
		}
	}()
	created, e := f.Stat()
	if e != nil || !created.Mode().IsRegular() || !ownerPrivate(created, 0o600) {
		return "", ErrUnavailable
	}
	if w.fail(kind+"-write") != nil {
		return "", ErrFixtureStorage
	}
	if _, e = f.Write(data); e != nil {
		return "", ErrFixtureStorage
	}
	if w.fail(kind+"-file-sync") != nil || f.Sync() != nil {
		return "", ErrFixtureStorage
	}
	if e = f.Close(); e != nil {
		return "", ErrFixtureStorage
	}
	if w.fail(kind+"-rename") != nil {
		return "", ErrFixtureStorage
	}
	if e = r.Rename(tmp, name); e != nil {
		return "", ErrFixtureStorage
	}
	if e = w.syncCollection(r, kind); e != nil {
		return "", e
	}
	return "created", nil
}

func (w *FixtureWriter) syncCollection(r *os.Root, kind string) error {
	f, err := r.Open(".")
	if err != nil {
		return ErrFixtureDurability
	}
	defer f.Close()
	if w.fail(kind+"-directory-sync") != nil || f.Sync() != nil {
		return ErrFixtureDurability
	}
	return nil
}
