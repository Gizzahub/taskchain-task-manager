package taskstore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// relocateFiles is the filesystem phase of a journaled relocation, not a public
// writer. The caller must hold board/common locks, persist a pending record and
// provide an authority check that verifies that exact record before mutation.
// A matching target is accepted only for recovery of that recorded operation.
func relocateFiles(r *os.Root, source, target string, original, patched []byte, mode uint32, authorize func() error, step func(string) error) error {
	if authorize == nil || source == target || len(original) == 0 || len(patched) == 0 || len(original) > maxCardBytes || len(patched) > maxCardBytes || mode == 0 || mode&^0777 != 0 {
		return errors.New("invalid journaled relocation filesystem request")
	}
	if err := authorize(); err != nil {
		return err
	}
	sourceExists, err := matchingRelocationFile(r, source, original, mode)
	if err != nil {
		return err
	}
	targetExists, err := matchingRelocationFile(r, target, patched, mode)
	if err != nil {
		return err
	}
	if !sourceExists && !targetExists {
		return errors.New("relocation source and target are both absent")
	}
	if !targetExists {
		if err := relocationParents(r, target, true); err != nil {
			return err
		}
		name, err := stageWith(r, patched, func(f *os.File, raw []byte) error {
			n, err := f.Write(raw)
			if err != nil {
				return err
			}
			if n != len(raw) {
				return io.ErrShortWrite
			}
			if err := f.Chmod(os.FileMode(mode)); err != nil {
				return err
			}
			return f.Sync()
		})
		if err != nil {
			return err
		}
		if err := storageStep(step, "after-stage"); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := authorize(); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := relocationParents(r, target, false); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		// Link refuses any existing target, including one created after preflight.
		if err := r.Link(name, target); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := errors.Join(r.Remove(name), syncRelocationDirectory(r, path.Dir(target)), syncRoot(r)); err != nil {
			return err
		}
		if err := storageStep(step, "after-target"); err != nil {
			return err
		}
	}
	if err := authorize(); err != nil {
		return err
	}
	targetExists, err = matchingRelocationFile(r, target, patched, mode)
	if err != nil {
		return err
	}
	if !targetExists {
		return errors.New("published relocation target disappeared")
	}
	sourceExists, err = matchingRelocationFile(r, source, original, mode)
	if err != nil {
		return err
	}
	if sourceExists {
		if err := r.Remove(source); err != nil {
			return err
		}
		if err := syncRelocationDirectory(r, path.Dir(source)); err != nil {
			return err
		}
	}
	return storageStep(step, "after-source")
}

func matchingRelocationFile(r *os.Root, name string, raw []byte, mode uint32) (bool, error) {
	if err := relocationParents(r, name, false); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return matchingTransitionFile(r, name, raw, mode)
}

// Check every ancestor, not only the immediate parent: os.Root confines symlink
// resolution to the board but does not itself forbid internal symlink aliases.
func relocationParents(r *os.Root, name string, create bool) error {
	if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return errors.New("invalid relocation file path")
	}
	parts := strings.Split(name, "/")
	for _, part := range parts {
		if strings.HasPrefix(part, ".") {
			return errors.New("invalid relocation path component")
		}
	}
	for i := 1; i < len(parts); i++ {
		dir := strings.Join(parts[:i], "/")
		info, err := r.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) && create {
			if err := r.Mkdir(dir, 0o755); err != nil {
				return err
			}
			if err := syncRelocationDirectory(r, path.Dir(dir)); err != nil {
				return err
			}
			info, err = r.Lstat(dir)
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("relocation ancestor is not a real directory: %s", dir)
		}
	}
	return nil
}

func syncRelocationDirectory(r *os.Root, dir string) error {
	f, err := r.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
