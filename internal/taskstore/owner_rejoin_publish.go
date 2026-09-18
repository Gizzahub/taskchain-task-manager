package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

const ownerRejoinReceiptFile = ".task-manager-owner-rejoin.json"

func ownerRejoinPlanArtifactName(digest string) (string, error) {
	if !sharedHex64.MatchString(digest) {
		return "", errors.New("invalid owner rejoin plan digest")
	}
	return ".task-manager-owner-rejoin-plan-" + digest + ".json", nil
}
func ownerRejoinPayloadArtifactName(digest string) (string, error) {
	if !sharedHex64.MatchString(digest) {
		return "", errors.New("invalid owner rejoin payload digest")
	}
	return ".task-manager-owner-rejoin-payload-" + digest + ".bin", nil
}

// publishOwnerRejoinArtifact is intentionally exclusive: a pre-existing name
// is accepted only after a full exact reload.  This makes retries safe without
// allowing a symlink, wrong mode, or third byte sequence to become authority.
func publishOwnerRejoinArtifact(r *os.Root, name string, raw []byte, limit int) error {
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("owner rejoin artifact size invalid")
	}
	if _, err := r.Lstat(name); err == nil {
		got, err := loadOwnerRejoinArtifact(r, name, limit)
		if err != nil || !bytes.Equal(got, raw) {
			return errors.New("owner rejoin artifact conflicts with digest name")
		}
		f, err := r.Open(name)
		if err != nil {
			return err
		}
		return errors.Join(f.Sync(), f.Close(), syncRoot(r))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	staged, err := stage(r, raw)
	if err != nil {
		return err
	}
	if err := r.Link(staged, name); err != nil {
		return errors.Join(err, r.Remove(staged))
	}
	if err := r.Remove(staged); err != nil {
		return err
	}
	if err := syncRoot(r); err != nil {
		return err
	}
	got, err := loadOwnerRejoinArtifact(r, name, limit)
	if err != nil || !bytes.Equal(got, raw) {
		return errors.New("owner rejoin artifact changed after publication")
	}
	return nil
}

func loadOwnerRejoinArtifact(r *os.Root, name string, limit int) ([]byte, error) {
	before, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, errors.New("owner rejoin artifact type, mode or size invalid")
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, errors.Join(errors.New("owner rejoin artifact identity changed"), err, f.Close())
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	closeErr := f.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	after, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || after.Mode().Perm() != 0600 || !os.SameFile(before, after) || after.Size() != before.Size() || int64(len(raw)) != before.Size() {
		return nil, errors.New("owner rejoin artifact changed during read")
	}
	return raw, nil
}

func publishOwnerRejoinReceipt(r *os.Root, receipt OwnerRejoinReceipt) error {
	raw, err := OwnerRejoinReceiptBytes(receipt)
	if err != nil {
		return err
	}
	if _, err := r.Lstat(ownerRejoinReceiptFile); err == nil {
		got, readErr := loadOwnerRejoinArtifact(r, ownerRejoinReceiptFile, 4096)
		if readErr != nil || !bytes.Equal(got, raw) {
			return fmt.Errorf("owner rejoin receipt conflicts: %w", readErr)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return publishOwnerRejoinArtifact(r, ownerRejoinReceiptFile, raw, 4096)
}

func ensureOwnerRejoinReceipt(r *os.Root, p OwnerRejoinPlan, want OwnerRejoinReceipt) error {
	if _, err := r.Lstat(ownerRejoinReceiptFile); errors.Is(err, fs.ErrNotExist) {
		return publishOwnerRejoinReceipt(r, want)
	} else if err != nil {
		return err
	}
	raw, err := loadOwnerRejoinArtifact(r, ownerRejoinReceiptFile, 4096)
	if err != nil {
		return err
	}
	got, err := DecodeOwnerRejoinReceipt(raw, p)
	if err != nil || got != want {
		return errors.New("owner rejoin receipt does not match recovery")
	}
	return nil
}

func replaceOwnerRejoinReceipt(r *os.Root, p OwnerRejoinPlan, old, next OwnerRejoinReceipt) error {
	raw, err := loadOwnerRejoinArtifact(r, ownerRejoinReceiptFile, 4096)
	if err != nil {
		return err
	}
	got, err := DecodeOwnerRejoinReceipt(raw, p)
	if err != nil || got != old {
		return errors.New("owner rejoin pending receipt changed")
	}
	target, err := OwnerRejoinReceiptBytes(next)
	if err != nil {
		return err
	}
	name, err := stage(r, target)
	if err != nil {
		return err
	}
	if err := r.Rename(name, ownerRejoinReceiptFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}

func publishOwnerRejoinTargets(r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, step func(string) error) error {
	order, err := ownerRejoinPublicationOrder(p)
	if err != nil {
		return err
	}
	for position, i := range order {
		class, _ := ownerRejoinFileClass(p.Files[i])
		if class == 5 {
			if err := publishOwnerRejoinCapacityArtifact(r, p, files); err != nil {
				return err
			}
			if err := storageStep(step, "after-owner-rejoin-capacity-artifact"); err != nil {
				return err
			}
		}
		if err := publishOwnerRejoinFile(r, p.Files[i], files[i]); err != nil {
			return err
		}
		if class == 0 {
			j, err := decodeTransitionJournal(files[i].Target)
			if err != nil || j.StorageProtocol != 6 {
				return errors.New("owner rejoin transition target lacks protocol 6")
			}
			if err := storageStep(step, "after-owner-rejoin-local-protocol"); err != nil {
				return err
			}
		}
		nextClass := -1
		if position+1 < len(order) {
			nextClass, _ = ownerRejoinFileClass(p.Files[order[position+1]])
		}
		if nextClass != class {
			if err := storageStep(step, "after-owner-rejoin-class-"+ownerRejoinClassName(class)); err != nil {
				return err
			}
		}
	}
	return nil
}

func publishOwnerRejoinCapacityArtifact(r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes) error {
	if len(p.Artifacts) != 1 {
		return errors.New("owner rejoin capacity artifact inventory missing")
	}
	var archive OwnerRejoinFileBytes
	var mode uint32
	for i, meta := range p.Files {
		if meta.Role == "archive" {
			archive, mode = files[i], meta.Mode
		}
	}
	if archive.Role == "" {
		return errors.New("owner rejoin archive bytes missing")
	}
	raw, err := ownerRejoinCapacityPayloadBytes(archive.Original, archive.Target, p.SourceOwner, p.TargetOwner, mode)
	if err != nil {
		return err
	}
	artifact := p.Artifacts[0]
	if artifact.Role != "archive-capacity-payload" || artifact.Length != len(raw) || artifact.SHA256 != bytesDigest(raw) || artifact.Mode != 0600 {
		return errors.New("owner rejoin capacity artifact binding mismatch")
	}
	return publishOwnerRejoinArtifact(r, artifact.Path, raw, maxOwnerRejoinCapacityArtifactBytes)
}

func publishOwnerRejoinFile(r *os.Root, meta OwnerRejoinFile, data OwnerRejoinFileBytes) error {
	if err := verifyOwnerRejoinFileStage(r, meta, data, false); err != nil {
		return err
	}
	if !meta.TargetPresent {
		if err := r.Remove(meta.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return syncRoot(r)
	}
	name, err := stageWith(r, data.Target, func(f *os.File, raw []byte) error {
		if _, err := f.Write(raw); err != nil {
			return err
		}
		if err := f.Chmod(os.FileMode(meta.Mode)); err != nil {
			return err
		}
		return f.Sync()
	})
	if err != nil {
		return err
	}
	if err := r.Rename(name, meta.Path); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}

func verifyOwnerRejoinFileStage(r *os.Root, meta OwnerRejoinFile, data OwnerRejoinFileBytes, targetOnly bool) error {
	info, err := r.Lstat(meta.Path)
	if errors.Is(err, fs.ErrNotExist) {
		if (!targetOnly && (!meta.OriginalPresent || !meta.TargetPresent)) || (targetOnly && !meta.TargetPresent) {
			return nil
		}
		return errors.New("owner rejoin file unexpectedly missing")
	}
	if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != meta.Mode {
		return errors.New("owner rejoin file type or mode mismatch")
	}
	raw, err := boundedSnapshotFile(r, meta.Path, maxArchiveCapacityBytes)
	if err != nil {
		return err
	}
	if meta.TargetPresent && bytes.Equal(raw, data.Target) {
		return nil
	}
	if !targetOnly && meta.OriginalPresent && bytes.Equal(raw, data.Original) {
		return nil
	}
	return errors.New("owner rejoin file has third bytes")
}
