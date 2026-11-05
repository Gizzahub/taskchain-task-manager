package taskflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func entryRequestKey(id, generation string) string {
	return "entry\n" + id + "\n" + generation
}

func closeRequestKey(requestID, generation string) string {
	return "close\n" + requestID + "\n" + generation
}

func evidencePartial(final, requestKey string) string {
	sum := sha256.Sum256([]byte(requestKey))
	return final + ".partial-" + hex.EncodeToString(sum[:8])
}

// readContainedRegular reads one regular file without following a symlink
// component. A missing file returns the operating-system not-exist error.
func readContainedRegular(root, abs string) ([]byte, error) {
	if err := refuseSymlinkAncestors(root, abs); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(abs, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), abs)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, errors.New("evidence reader did not open a file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errSymlinkOrSpecial
	}
	return io.ReadAll(file)
}

func readOptionalContained(root, abs string) ([]byte, error) {
	info, err := os.Lstat(abs)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errSymlinkOrSpecial
	}
	return readContainedRegular(root, abs)
}

func writeContainedReplace(root, abs string, content []byte) error {
	if err := refuseSymlinkAncestors(root, abs); err != nil {
		return err
	}
	flags := syscall.O_WRONLY | syscall.O_CREAT | syscall.O_TRUNC | syscall.O_NOFOLLOW | syscall.O_CLOEXEC
	fd, err := syscall.Open(abs, flags, 0o644)
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), abs)
	if file == nil {
		_ = syscall.Close(fd)
		return errors.New("evidence writer did not open a file")
	}
	_, writeErr := file.Write(content)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

// stageRequestPartial writes this request's same-directory partial and reads
// it back. A final file with the same bytes is already published. A different
// final, or another request's partial for that final, is refused.
func stageRequestPartial(root, final, requestKey string, content []byte) error {
	if err := refuseSymlinkAncestors(root, final); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	existing, err := readOptionalContained(root, final)
	if err != nil {
		return err
	}
	if existing != nil {
		if bytes.Equal(existing, content) {
			return nil
		}
		return errors.New("evidence already exists with different bytes")
	}
	if err := refuseForeignPartials(final, requestKey); err != nil {
		return err
	}
	partial := evidencePartial(final, requestKey)
	if err := writeContainedReplace(root, partial, content); err != nil {
		return err
	}
	got, err := readContainedRegular(root, partial)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, content) {
		return errors.New("evidence partial did not match the request")
	}
	return nil
}

// finalizeRequestPartial renames this request's partial onto the final name
// only after the partial bytes match. An existing matching final is kept.
func finalizeRequestPartial(root, final, requestKey string, content []byte) error {
	existing, err := readOptionalContained(root, final)
	if err != nil {
		return err
	}
	partial := evidencePartial(final, requestKey)
	if existing != nil {
		if !bytes.Equal(existing, content) {
			return errors.New("evidence already exists with different bytes")
		}
		removeRegularFile(partial)
		return nil
	}
	got, err := readContainedRegular(root, partial)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, content) {
		return errors.New("evidence partial did not match the request")
	}
	if err := os.Rename(partial, final); err != nil {
		return err
	}
	confirmed, err := readContainedRegular(root, final)
	if err != nil {
		return err
	}
	if !bytes.Equal(confirmed, content) {
		return errors.New("published evidence does not match the request")
	}
	return nil
}

func refuseForeignPartials(final, requestKey string) error {
	dir := filepath.Dir(final)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	ours := filepath.Base(evidencePartial(final, requestKey))
	prefix := filepath.Base(final) + ".partial-"
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && name != ours {
			return errors.New("a different request owns the pending evidence partial")
		}
	}
	return nil
}

func removeRegularFile(abs string) {
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	_ = os.Remove(abs)
}
