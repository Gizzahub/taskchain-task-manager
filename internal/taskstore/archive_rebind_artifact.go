package taskstore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

func archiveRebindArtifactName(digest string) (string, error) {
	if !sharedHex64.MatchString(digest) {
		return "", errors.New("invalid archive rebind artifact digest")
	}
	return ".task-manager-archive-rebind-" + digest + ".bin", nil
}

// publishArchiveRebindArtifact requires the caller's common/board locks.
// It publishes immutable preparation data, never an activation phase. Callers
// must preflight all participants and bind the returned digest in the existing
// activation authority only after every required artifact is durable.
func publishArchiveRebindArtifact(r *os.Root, plan ArchiveNamespaceRebindPlan) (string, error) {
	return publishArchiveRebindArtifactStep(r, plan, nil)
}

func publishArchiveRebindArtifactStep(r *os.Root, plan ArchiveNamespaceRebindPlan, step func(string) error) (string, error) {
	raw, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		return "", err
	}
	digest := bytesDigest(raw)
	name, err := archiveRebindArtifactName(digest)
	if err != nil {
		return "", err
	}
	if _, err := r.Lstat(name); err == nil {
		if _, err := loadArchiveRebindArtifact(r, digest); err != nil {
			return "", err
		}
		// Retrying after a directory-sync failure must still prove durability.
		f, err := r.Open(name)
		if err != nil {
			return "", err
		}
		if err := errors.Join(f.Sync(), f.Close(), syncRoot(r)); err != nil {
			return "", err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	} else {
		staged, err := stage(r, raw)
		if err != nil {
			return "", err
		}
		if err := storageStep(step, "after-rebind-artifact-stage"); err != nil {
			return "", errors.Join(err, r.Remove(staged))
		}
		// Exclusive linking cannot replace a conflicting file or symlink.
		if err := r.Link(staged, name); err != nil {
			return "", errors.Join(err, r.Remove(staged))
		}
		if err := r.Remove(staged); err != nil {
			return "", err
		}
		if err := storageStep(step, "after-rebind-artifact-link"); err != nil {
			return "", err
		}
		if err := syncRoot(r); err != nil {
			// Preserve the published artifact for exact retry, never roll it back.
			return "", err
		}
	}
	if _, err := loadArchiveRebindArtifact(r, digest); err != nil {
		return "", err
	}
	return digest, nil
}

// loadArchiveRebindArtifact does not infer authority from a file's existence.
// Recovery supplies the digest from its durable participant plan and separately
// verifies board, namespace, journal mode and original/target hashes against it.
// Missing or changed artifacts are restore-only; this function never recreates.
func loadArchiveRebindArtifact(r *os.Root, digest string) (ArchiveNamespaceRebindPlan, error) {
	var zero ArchiveNamespaceRebindPlan
	name, err := archiveRebindArtifactName(digest)
	if err != nil {
		return zero, err
	}
	before, err := r.Lstat(name)
	if err != nil {
		return zero, err
	}
	if !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() < int64(archiveRebindPayloadHeader) || before.Size() > int64(maxArchiveRebindPayloadBytes) {
		return zero, errors.New("archive rebind artifact type, mode or size invalid")
	}
	f, err := r.Open(name)
	if err != nil {
		return zero, err
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(before, opened) {
		return zero, errors.Join(fmt.Errorf("archive rebind artifact identity changed"), statErr, f.Close())
	}
	raw, readErr := io.ReadAll(io.LimitReader(f, int64(maxArchiveRebindPayloadBytes)+1))
	if err := errors.Join(readErr, f.Close()); err != nil {
		return zero, err
	}
	after, err := r.Lstat(name)
	if err != nil {
		return zero, err
	}
	if !after.Mode().IsRegular() || after.Mode().Perm() != 0600 || !os.SameFile(before, after) || int64(len(raw)) != before.Size() || after.Size() != before.Size() {
		return zero, errors.New("archive rebind artifact changed during read")
	}
	return decodeArchiveRebindPayload(raw, digest)
}
