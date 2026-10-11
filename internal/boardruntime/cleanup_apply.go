package boardruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// ApplyCleanupGroup rechecks the proof, quarantines the exact files, and
// deletes them only after the bytes still match the pinned source commit.
// Errors before that deletion restore the original paths. Later errors use
// the "cleanup committed;" prefix. The git index is not updated.
func ApplyCleanupGroup(ctx context.Context, root, source string, expected CleanupEntry) error {
	lock := filepath.Join(root, ".ce-task-cleanup.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return fmt.Errorf("cleanup lock unavailable: %w", err)
	}
	defer func() { _ = os.Remove(lock) }()
	if err := sourceUnchanged(ctx, root, source, expected.SourceOID); err != nil {
		return err
	}
	current, err := CleanupCandidates(ctx, root, source)
	if err != nil {
		return err
	}
	found := false
	for _, candidate := range current {
		if candidate.Path == expected.Path {
			found = reflect.DeepEqual(candidate, expected) && candidate.Removable
			break
		}
	}
	if !found {
		return fmt.Errorf("cleanup proof changed for %s; preview again", expected.Path)
	}
	paths := append(append([]string{}, expected.Artifacts...), expected.Path)
	identities := map[string]os.FileInfo{}
	for _, path := range paths {
		if err := contained(root, path, false); err != nil {
			return err
		}
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return err
		}
		identities[path] = info
	}
	quarantine, err := os.MkdirTemp(root, ".ce-task-cleanup-")
	if err != nil {
		return err
	}
	var moved []string
	rollback := func(cause error) error {
		var failures []string
		for i := len(moved) - 1; i >= 0; i-- {
			path := moved[i]
			target := filepath.Join(root, filepath.FromSlash(path))
			if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				failures = append(failures, path+": original path occupied")
				continue
			}
			if err := os.Rename(filepath.Join(quarantine, filepath.FromSlash(path)), target); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) != 0 {
			return fmt.Errorf("%w; recovery files at %s: %s", cause, quarantine, strings.Join(failures, "; "))
		}
		if err := os.RemoveAll(quarantine); err != nil {
			return fmt.Errorf("%w; remove empty quarantine %s: %v", cause, quarantine, err)
		}
		return cause
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return rollback(err)
		}
		if err := contained(root, path, false); err != nil {
			return rollback(err)
		}
		full := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Lstat(full)
		if err != nil || !os.SameFile(identities[path], info) {
			return rollback(fmt.Errorf("cleanup file identity changed: %s", path))
		}
		same, cmpErr := matchesSource(ctx, root, expected.SourceOID, path)
		if cmpErr != nil || !same {
			return rollback(fmt.Errorf("cleanup file changed: %s", path))
		}
		target := filepath.Join(quarantine, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return rollback(err)
		}
		if err := os.Rename(full, target); err != nil {
			return rollback(err)
		}
		moved = append(moved, path)
	}
	for _, path := range moved {
		data, err := os.ReadFile(filepath.Join(quarantine, filepath.FromSlash(path)))
		if err != nil {
			return rollback(err)
		}
		want, err := git(ctx, root, "show", expected.SourceOID+":"+filepath.ToSlash(path))
		if err != nil || string(data) != string(want) {
			return rollback(fmt.Errorf("quarantined bytes changed: %s", path))
		}
	}
	if err := sourceUnchanged(ctx, root, source, expected.SourceOID); err != nil {
		return rollback(err)
	}
	tracked, err := trackedSet(ctx, root)
	if err != nil {
		return rollback(err)
	}
	refs, err := referenceFiles(ctx, root, tracked)
	if err != nil {
		return rollback(err)
	}
	quarantineRel, err := filepath.Rel(root, quarantine)
	if err != nil {
		return rollback(err)
	}
	tasksDir := taskflow.TasksDirName()
	for _, ref := range refs {
		if ref.path == expected.Path || containsPath(expected.Artifacts, ref.path) ||
			strings.HasPrefix(ref.path, filepath.ToSlash(quarantineRel)+"/") {
			continue
		}
		if ref.tracked && durableReviewReceipt(root, tasksDir, ref.path) {
			continue
		}
		if blocked, reason := namesEntry(tasksDir, ref, expected); blocked {
			return rollback(fmt.Errorf("cleanup reference added during apply: %s (%s)", ref.path, reason))
		}
	}
	if err := sourceUnchanged(ctx, root, source, expected.SourceOID); err != nil {
		return rollback(err)
	}
	if err := os.RemoveAll(quarantine); err != nil {
		return fmt.Errorf("cleanup committed; quarantine reclaim failed at %s: %w", quarantine, err)
	}
	for _, dir := range expected.ArtifactDirs {
		if err := contained(root, dir, true); err != nil {
			return fmt.Errorf("cleanup committed; directory cleanup: %w", err)
		}
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(dir))); err != nil {
			return fmt.Errorf("cleanup committed; nonempty/protected directory %s: %w", dir, err)
		}
	}
	return nil
}

func sourceUnchanged(ctx context.Context, root, source, oid string) error {
	if err := gitEnv(); err != nil {
		return err
	}
	if oid == "" {
		return fmt.Errorf("cleanup requires a pinned source commit")
	}
	now, err := revParse(ctx, root, source)
	if err != nil || now != oid {
		return fmt.Errorf("cleanup source changed; preview again")
	}
	if err := SourceReady(ctx, root, source); err != nil {
		return err
	}
	if declaredPresent(ctx, root, oid) {
		same, err := matchesSource(ctx, root, oid, ".gz-git.yaml")
		if err != nil || !same {
			return fmt.Errorf("cleanup source declaration is not delivered unchanged")
		}
	}
	branch, err := git(ctx, root, "symbolic-ref", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(branch)) == source {
		return fmt.Errorf("cleanup apply requires a non-source task branch")
	}
	if !gitOK(ctx, root, "merge-base", "--is-ancestor", oid, "HEAD") {
		return fmt.Errorf("cleanup branch does not contain pinned source")
	}
	return nil
}

func declaredPresent(ctx context.Context, root, oid string) bool {
	if gitOK(ctx, root, "cat-file", "-e", oid+":.gz-git.yaml") {
		return true
	}
	if _, err := os.Lstat(filepath.Join(root, ".gz-git.yaml")); err == nil {
		return true
	}
	return gitOK(ctx, root, "cat-file", "-e", ":.gz-git.yaml")
}
