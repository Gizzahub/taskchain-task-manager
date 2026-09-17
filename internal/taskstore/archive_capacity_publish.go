package taskstore

import (
	"errors"
	"io"
	"os"
)

func publishArchiveCapacityTarget(r *os.Root, target []byte, mode uint32, digest string) error {
	info, err := r.Lstat(archivesFile)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("archive capacity journal is not regular")
	}
	name, err := stageWith(r, target, func(f *os.File, raw []byte) error {
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
	if err := r.Rename(name, archivesFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	if err := syncRoot(r); err != nil {
		return err
	}
	return verifyArchiveCapacityTarget(r, target, mode, digest)
}

func verifyArchiveCapacityTarget(r *os.Root, target []byte, mode uint32, digest string) error {
	info, err := r.Lstat(archivesFile)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != mode || info.Size() != int64(len(target)) {
		return errors.New("archive capacity target type, mode or size invalid")
	}
	raw, err := boundedSnapshotFile(r, archivesFile, maxArchiveCapacityBytes)
	if err != nil {
		return err
	}
	if bytesDigest(raw) != digest || bytesDigest(target) != digest {
		return errors.New("archive capacity target digest mismatch")
	}
	return nil
}
