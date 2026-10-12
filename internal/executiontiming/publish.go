package executiontiming

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Publish atomically creates the immutable sidecar for r. An equal prior record is a no-op.
func Publish(ledgerDir string, r Record) (string, error) {
	if err := Validate(r); err != nil {
		return "", err
	}
	if !filepath.IsAbs(ledgerDir) {
		return "", errors.New("execution timing ledger directory must be absolute")
	}
	root := filepath.Clean(ledgerDir)
	if err := ensureLedgerDir(root); err != nil {
		return "", fmt.Errorf("execution timing ledger: %w", err)
	}
	dir, err := safeChild(root, "execution-timing")
	if err != nil {
		return "", err
	}
	dir, err = safeChild(dir, "v1")
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(r)
	if err != nil {
		return "", fmt.Errorf("encode execution timing: %w", err)
	}
	digest := sha256.Sum256([]byte(r.RunID))
	path := filepath.Join(dir, hex.EncodeToString(digest[:])+".json")
	if same, exists, err := sameArtifact(path, body); err != nil {
		return "", err
	} else if exists {
		if same {
			return path, nil
		}
		return "", errors.New("execution timing artifact collision")
	}
	tmp, err := os.CreateTemp(dir, ".execution-timing-")
	if err != nil {
		return "", fmt.Errorf("create execution timing temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(body)
	}
	if err == nil {
		err = tmp.Sync()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("write execution timing temp: %w", err)
	}
	if err := os.Link(tmpName, path); err == nil {
		if err := os.Remove(tmpName); err != nil {
			return "", fmt.Errorf("remove published execution timing temp: %w", err)
		}
		return path, nil
	} else if !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("publish execution timing: %w", err)
	}
	if same, _, err := sameArtifact(path, body); err != nil {
		return "", err
	} else if !same {
		return "", errors.New("execution timing artifact collision")
	}
	return path, nil
}

// ensureLedgerDir preserves an existing bound ledger root and creates only its
// missing descendants after checking every newly addressed component.
func ensureLedgerDir(root string) error {
	missing := []string{}
	current := root
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return errors.New("path is not a non-symlink directory")
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return err
		}
		missing = append(missing, current)
		current = parent
	}
	for i := len(missing) - 1; i >= 0; i-- {
		if err := os.Mkdir(missing[i], 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(missing[i])
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("path is not a non-symlink directory")
		}
	}
	return nil
}

func safeChild(parent, name string) (string, error) {
	child := filepath.Join(parent, name)
	if err := os.Mkdir(child, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create execution timing directory: %w", err)
	}
	if err := safeDir(child); err != nil {
		return "", fmt.Errorf("unsafe execution timing directory: %w", err)
	}
	return child, nil
}

func safeDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("path is not a non-symlink directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("directory permissions are not private")
	}
	return nil
}

func sameArtifact(path string, body []byte) (bool, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, true, errors.New("execution timing artifact is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return false, true, errors.New("execution timing artifact permissions are not private")
	}
	f, err := os.Open(path)
	if err != nil {
		return false, true, err
	}
	defer f.Close()
	got, err := io.ReadAll(io.LimitReader(f, int64(len(body)+1)))
	if err != nil {
		return false, true, err
	}
	return bytes.Equal(got, body), true, nil
}
