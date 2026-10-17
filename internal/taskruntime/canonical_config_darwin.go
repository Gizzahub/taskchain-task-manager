//go:build darwin && cgo

package taskruntime

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func readCanonicalConfig(root string, hook canonicalReadHook) ([]byte, error) {
	return readCanonicalConfigUsing(root, hook, nil)
}

func readCanonicalConfigUsing(root string, hook canonicalReadHook, callback func(*os.File, *os.File, []byte) error) (data []byte, err error) {
	var files []*os.File
	defer func() {
		for i := len(files) - 1; i >= 0; i-- {
			closeErr := files[i].Close()
			if injected := canonicalReadPoint(hook, "close"); injected != nil {
				closeErr = errors.Join(closeErr, injected)
			}
			if closeErr != nil {
				data = nil
				err = errors.Join(err, fmt.Errorf("%w: close: %w", errCanonicalUnavailable, closeErr))
			}
		}
	}()
	first, e := openCanonicalConfigPath(root, hook)
	files = append(files, first...)
	if e != nil {
		return nil, e
	}
	file := first[len(first)-1]
	var before unix.Stat_t
	if unix.Fstat(int(file.Fd()), &before) != nil {
		return nil, fmt.Errorf("%w: initial file stat", errCanonicalUnavailable)
	}
	if e := canonicalReadPoint(hook, "opened"); e != nil {
		return nil, fmt.Errorf("%w: %w", errCanonicalUnavailable, e)
	}
	data, e = io.ReadAll(io.LimitReader(file, canonicalConfigMaxBytes+1))
	if injected := canonicalReadPoint(hook, "read"); injected != nil {
		e = errors.Join(e, injected)
	}
	if e != nil || len(data) > canonicalConfigMaxBytes {
		return nil, fmt.Errorf("%w: bounded read: %v", errCanonicalUnavailable, e)
	}
	if e := canonicalReadPoint(hook, "after-read"); e != nil {
		return nil, fmt.Errorf("%w: %w", errCanonicalUnavailable, e)
	}
	second, e := openCanonicalConfigPath(root, hook)
	files = append(files, second...)
	if e != nil {
		return nil, e
	}
	if len(first) != len(second) {
		return nil, fmt.Errorf("%w: path changed", errCanonicalUnavailable)
	}
	for i := range first {
		var a, b unix.Stat_t
		if unix.Fstat(int(first[i].Fd()), &a) != nil || unix.Fstat(int(second[i].Fd()), &b) != nil || a.Dev != b.Dev || a.Ino != b.Ino {
			return nil, fmt.Errorf("%w: directory or file identity changed", errCanonicalUnavailable)
		}
	}
	var after unix.Stat_t
	if unix.Fstat(int(file.Fd()), &after) != nil || !canonicalStableFile(before, after) || int64(len(data)) != after.Size {
		return nil, fmt.Errorf("%w: file metadata changed", errCanonicalUnavailable)
	}
	if callback != nil {
		if err := callback(file, first[len(first)-2], data); err != nil {
			return nil, err
		}
	}
	return data, nil
}

func recheckCanonicalConfigPath(root string, pinned *os.File, raw []byte) error {
	return withCanonicalConfigPath(root, nil, func(current, _ *os.File, data []byte) error {
		pinnedStat, pinnedErr := pinned.Stat()
		currentStat, currentErr := current.Stat()
		if pinnedErr != nil || currentErr != nil || !os.SameFile(pinnedStat, currentStat) {
			return fmt.Errorf("%w: canonical descriptor identity changed", errCanonicalUnavailable)
		}
		if !bytes.Equal(raw, data) {
			return fmt.Errorf("%w: canonical bytes changed", errCanonicalUnavailable)
		}
		return nil
	})
}

func withCanonicalConfigPath(root string, hook canonicalReadHook, callback func(*os.File, *os.File, []byte) error) error {
	_, err := readCanonicalConfigUsing(root, hook, callback)
	return err
}

func canonicalStableFile(a, b unix.Stat_t) bool {
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Uid == b.Uid && a.Gid == b.Gid && a.Mode == b.Mode && a.Nlink == b.Nlink && a.Size == b.Size && a.Mtim == b.Mtim && a.Ctim == b.Ctim && a.Flags == b.Flags
}

// Every returned descriptor, including partial failures, belongs to the caller.
func openCanonicalConfigPath(root string, hook canonicalReadHook) ([]*os.File, error) {
	var files []*os.File
	fail := func(reason string) ([]*os.File, error) {
		return files, fmt.Errorf("%w: %s", errCanonicalUnavailable, reason)
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return fail("noncanonical primary path")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fail("open filesystem root")
	}
	files = append(files, os.NewFile(uintptr(fd), "/"))
	if err := checkCanonicalFD(fd, 0, hook); err != nil {
		return files, err
	}
	parts := append(strings.Split(strings.TrimPrefix(root, "/"), "/"), ".ce", "task-runtime.yaml")
	for i, name := range parts {
		kind := 0 // ancestor, including repository root
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		if i == len(parts)-1 {
			kind = 2
		} else {
			flags |= unix.O_DIRECTORY
			if i == len(parts)-2 {
				kind = 1 // owner-private .ce
			}
		}
		next, err := unix.Openat(fd, name, flags, 0)
		if err != nil {
			return fail("open no-follow path")
		}
		fd = next
		files = append(files, os.NewFile(uintptr(fd), name))
		if err := checkCanonicalFD(fd, kind, hook); err != nil {
			return files, err
		}
	}
	return files, nil
}

func checkCanonicalFD(fd, kind int, hook canonicalReadHook) error {
	var st unix.Stat_t
	err := unix.Fstat(fd, &st)
	if injected := canonicalReadPoint(hook, "stat"); injected != nil {
		err = errors.Join(err, injected)
	}
	if err != nil || !canonicalSafeStat(st, kind, uint32(os.Geteuid())) {
		return fmt.Errorf("%w: owner, mode, type, hardlink or stat", errCanonicalUnavailable)
	}
	if err := canonicalReadPoint(hook, "acl-query"); err != nil {
		return fmt.Errorf("%w: ACL query: %w", errCanonicalUnavailable, err)
	}
	if status := canonicalACLStatus(fd, st.Uid); status != 0 {
		return fmt.Errorf("%w: ACL classification %d", errCanonicalUnavailable, status)
	}
	return nil
}

func canonicalSafeStat(st unix.Stat_t, kind int, effectiveUID uint32) bool {
	if kind == 2 {
		return st.Mode&unix.S_IFMT == unix.S_IFREG && st.Uid == effectiveUID && st.Mode&0o7777 == 0o600 && st.Nlink == 1 && st.Size >= 0 && st.Size <= canonicalConfigMaxBytes
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false
	}
	if kind == 1 {
		return st.Uid == effectiveUID && st.Mode&0o7777 == 0o700
	}
	return (st.Uid == effectiveUID || st.Uid == 0) && st.Mode&(unix.S_ISUID|unix.S_ISGID) == 0 && (st.Mode&0o022 == 0 || (st.Uid == 0 && st.Mode&unix.S_ISVTX != 0))
}

// retireOwnedMutationLock claims a contested public pathname into an exclusive,
// operation-private name before inspecting and unlinking it. No cooperative
// writer touches these staging names. This is not same-UID hostile-process
// authentication. A mismatch is restored without overwriting, or retained with
// an explicit residual path; a foreign inode is never unlinked.
func retireOwnedMutationLock(path string, owned os.FileInfo, expected []byte, remove func(string) error, syncDir func() error, hook canonicalReadHook) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("release staging identity: %w", err)
	}
	staging := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.release-%x", filepath.Base(path), nonce))
	if err := unix.RenameatxNp(unix.AT_FDCWD, path, unix.AT_FDCWD, staging, unix.RENAME_EXCL); err != nil {
		return fmt.Errorf("claim lock for release (original retained): %w", err)
	}
	restore := func(cause error) error {
		if err := unix.RenameatxNp(unix.AT_FDCWD, staging, unix.AT_FDCWD, path, unix.RENAME_EXCL); err != nil {
			return errors.Join(cause, fmt.Errorf("lock release residual retained at %s: %w", staging, err), syncDir())
		}
		return errors.Join(cause, syncDir())
	}
	if err := canonicalReadPoint(hook, "lock-retired"); err != nil {
		return restore(err)
	}
	st, err := os.Lstat(staging)
	if err != nil || !st.Mode().IsRegular() || owned == nil || !os.SameFile(owned, st) {
		return restore(fmt.Errorf("refusing to remove replaced mutation lock: %v", err))
	}
	fd, err := unix.Open(staging, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return restore(fmt.Errorf("open release staging: %w", err))
	}
	file := os.NewFile(uintptr(fd), staging)
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(owned, opened) {
		return restore(errors.Join(fmt.Errorf("release staging identity changed: %v", statErr), file.Close()))
	}
	var readErr error
	if expected != nil {
		var data []byte
		data, readErr = io.ReadAll(io.LimitReader(file, int64(len(expected))+1))
		if !bytes.Equal(data, expected) {
			readErr = errors.Join(readErr, errors.New("release staging bytes changed"))
		}
	}
	if err := errors.Join(readErr, file.Close()); err != nil {
		return restore(err)
	}
	last, err := os.Lstat(staging)
	if err != nil || !last.Mode().IsRegular() || !os.SameFile(owned, last) {
		return restore(fmt.Errorf("release staging replaced: %v", err))
	}
	if err := remove(staging); err != nil {
		return restore(fmt.Errorf("remove owned release staging: %w", err))
	}
	return nil
}
