package boardfiles

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// readDeclaredFile reads one declared root leaf through a pinned board root.
// Missing and stable non-regular shapes are structural findings; inability to
// perform or verify the bounded no-follow read is an unavailable error.
func readDeclaredFile(r *os.Root, name string, limit int64) ([]byte, string, error) {
	return readDeclaredFileWithHook(r, name, limit, nil)
}

// readDeclaredFileWithHook supplies one narrow test seam between the
// no-follow open and read. Production always calls readDeclaredFile.
func readDeclaredFileWithHook(r *os.Root, name string, limit int64, afterOpen func() error) ([]byte, string, error) {
	if !declaredRootLeaf(name) {
		return nil, "", fmt.Errorf("declared file path must be one root filename: %q", name)
	}
	if limit < 0 {
		return nil, "", errors.New("declared file read limit is negative")
	}

	before, err := r.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "declared file is missing", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("lstat declared file: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, "declared file is a symlink", nil
	}
	if !before.Mode().IsRegular() {
		return nil, "declared file is not a regular file", nil
	}

	file, err := openProjection(r, name)
	if err != nil {
		return nil, "", fmt.Errorf("open declared file without following links: %w", err)
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !sameDeclaredState(before, opened) {
		return nil, "", errors.Join(errors.New("declared file changed while opening"), err, file.Close())
	}
	if afterOpen != nil {
		if err := afterOpen(); err != nil {
			return nil, "", errors.Join(err, file.Close())
		}
	}

	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	afterOpenInfo, statErr := file.Stat()
	afterRoot, lstatErr := r.Lstat(name)
	closeErr := file.Close()
	if err := errors.Join(readErr, statErr, lstatErr, closeErr); err != nil {
		return nil, "", fmt.Errorf("read declared file: %w", err)
	}
	if !afterOpenInfo.Mode().IsRegular() || !afterRoot.Mode().IsRegular() ||
		!os.SameFile(before, afterOpenInfo) || !os.SameFile(before, afterRoot) ||
		!sameDeclaredState(before, afterOpenInfo) || !sameDeclaredState(before, afterRoot) {
		return nil, "", errors.New("declared file changed while reading")
	}
	if int64(len(data)) > limit {
		return nil, fmt.Sprintf("declared file exceeds %d bytes", limit), nil
	}
	return data, "", nil
}

func declaredRootLeaf(name string) bool {
	return name != "" && name != "." && name != ".." && fs.ValidPath(name) &&
		filepath.Base(name) == name && !strings.ContainsAny(name, "/\\")
}

func sameDeclaredState(left, right fs.FileInfo) bool {
	return left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
