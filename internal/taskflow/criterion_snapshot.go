package taskflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Snapshot content the spec reports as admission unavailable, not as an
// unmaterializable base. Other snapshot failures stay exit 2.
const (
	snapshotSubmoduleRefusal = "submodule"
	snapshotSymlinkRefusal   = "absolute, empty, cyclic, or escaping symlink"
)

// snapshotContentError is a tree the snapshot refuses. detail keeps the
// parser wording tests already pin; warning is the admission phrase.
type snapshotContentError struct {
	detail  string
	warning string
}

func (e snapshotContentError) Error() string { return e.detail }

func refuseSubmodule(detail string) error {
	return snapshotContentError{detail: detail, warning: snapshotSubmoduleRefusal}
}

func refuseSymlink(detail string) error {
	return snapshotContentError{detail: detail, warning: snapshotSymlinkRefusal}
}

func snapshotRefusalReason(err error) (string, bool) {
	var content snapshotContentError
	if err == nil || !errors.As(err, &content) || content.warning == "" {
		return "", false
	}
	return content.warning, true
}

func refusedCriterionTree(sha, root, reason string) CriterionTree {
	return CriterionTree{
		Description: fmt.Sprintf("commit %s (tracked snapshot; repository %s)", sha, root),
		Selected:    true,
		Refusal:     reason,
	}
}

const (
	criterionSnapshotEntryLimit = 50_000
	criterionSnapshotBlobLimit  = 16 << 20
	criterionSnapshotTotalLimit = 256 << 20
	criterionTreeListingLimit   = 32 << 20
	criterionTreeRecordLimit    = 64 << 10
	criterionSnapshotTimeout    = 30 * time.Second
	criterionSymlinkDepthLimit  = 40
)

// snapshotBounds are the entry and blob caps one materialization enforces.
// Production calls pass productionSnapshotBounds so a test of that wrapper
// fails when the caps drift from the constants above.
type snapshotBounds struct {
	entries int
	blob    int64
	total   int64
}

func productionSnapshotBounds() snapshotBounds {
	return snapshotBounds{
		entries: criterionSnapshotEntryLimit,
		blob:    criterionSnapshotBlobLimit,
		total:   criterionSnapshotTotalLimit,
	}
}

// pathInside reports whether target is root or a path lexically under it.
// It does not resolve symlinks; callers that follow links check each hop.
func pathInside(root, target string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// SelectCriterionTree resolves --base to a tracked snapshot, or describes the
// mutable work tree. base empty selects the work tree. The snapshot is only
// for criterion probes; cards are still read from root.
func SelectCriterionTree(ctx context.Context, root, base string) (CriterionTree, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if strings.TrimSpace(base) == "" {
		return currentCriterionTree(ctx, abs), nil
	}
	tree, err := materializeBaseSnapshot(ctx, abs, strings.TrimSpace(base))
	if err != nil {
		return CriterionTree{}, fmt.Errorf("criterion base unavailable: %w", err)
	}
	return tree, nil
}

func currentCriterionTree(ctx context.Context, abs string) CriterionTree {
	desc := fmt.Sprintf("current tree %s (HEAD unavailable; mutable working tree)", abs)
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	head, err := gitOutput(probeCtx, abs, "--no-replace-objects", "--no-lazy-fetch", "rev-parse", "--verify", "HEAD^{commit}")
	if err == nil && head != "" {
		desc = fmt.Sprintf("current tree %s (HEAD %s; mutable working tree)", abs, strings.TrimSpace(head))
	}
	return CriterionTree{Description: desc, ProbeDir: abs}
}

func materializeBaseSnapshot(ctx context.Context, root, base string) (tree CriterionTree, err error) {
	ctx, cancel := context.WithTimeout(ctx, criterionSnapshotTimeout)
	defer cancel()
	sha, err := resolveSnapshotCommit(ctx, root, base)
	if err != nil {
		return CriterionTree{}, err
	}
	entries, err := listSnapshotEntries(ctx, root, sha)
	if err != nil {
		if reason, ok := snapshotRefusalReason(err); ok {
			return refusedCriterionTree(sha, root, reason), nil
		}
		return CriterionTree{}, err
	}
	baseDir, err := criterionSnapshotBase(root)
	if err != nil {
		return CriterionTree{}, err
	}
	dir, err := os.MkdirTemp(baseDir, "criterion-")
	if err != nil {
		return CriterionTree{}, fmt.Errorf("create criterion snapshot: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	if err = writeSnapshotBlobs(ctx, root, dir, entries); err != nil {
		return CriterionTree{}, err
	}
	if err = validateCriterionLinks(entries); err != nil {
		if reason, ok := snapshotRefusalReason(err); ok {
			_ = os.RemoveAll(dir)
			return refusedCriterionTree(sha, root, reason), nil
		}
		return CriterionTree{}, err
	}
	if err = writeCriterionLinks(dir, entries); err != nil {
		return CriterionTree{}, err
	}
	if err = verifySnapshotLinks(dir, entries); err != nil {
		if reason, ok := snapshotRefusalReason(err); ok {
			_ = os.RemoveAll(dir)
			return refusedCriterionTree(sha, root, reason), nil
		}
		return CriterionTree{}, err
	}
	return CriterionTree{
		Description: fmt.Sprintf("commit %s (tracked snapshot; repository %s)", sha, root),
		ProbeDir:    dir,
		Selected:    true,
		cleanup:     dir,
	}, nil
}

func resolveSnapshotCommit(ctx context.Context, root, base string) (string, error) {
	out, err := snapshotGit(ctx, root, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(out)
	if sha == "" || strings.ContainsAny(sha, "\r\n") {
		return "", fmt.Errorf("resolve criterion revision %q: Git returned invalid commit", base)
	}
	return sha, nil
}

func snapshotGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := snapshotGitCommand(ctx, root, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return string(out), nil
}

func snapshotGitCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	full := append([]string{"-C", root, "--no-optional-locks", "-c", "core.quotepath=false", "--no-replace-objects", "--no-lazy-fetch"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

// criterionSnapshotBase is build/tmp/criterion-snapshots under the repository.
// An ancestor that is itself a symlink is refused. The path is not
// EvalSymlinks'd: resolving it would turn a macOS /var prefix into /private/var
// and look like an escape.
func criterionSnapshotBase(repoRoot string) (string, error) {
	current := repoRoot
	for _, part := range []string{"build", "tmp", "criterion-snapshots"} {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("criterion snapshot ancestor is a symlink: %q", current)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("criterion snapshot ancestor is not a directory: %q", current)
			}
		case os.IsNotExist(err):
			if mkErr := os.Mkdir(current, 0o700); mkErr != nil && !os.IsExist(mkErr) {
				return "", fmt.Errorf("create criterion snapshot base: %w", mkErr)
			}
		default:
			return "", fmt.Errorf("inspect criterion snapshot ancestor: %w", err)
		}
	}
	return current, nil
}

func listSnapshotEntries(ctx context.Context, root, sha string) ([]snapshotEntry, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := snapshotGitCommand(child, root, "ls-tree", "-r", "-z", "--full-tree", sha)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("git ls-tree: %w", err)
	}
	entries, readErr := readSnapshotListing(stdout)
	if readErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("git ls-tree: %w: %s", waitErr, msg)
		}
		return nil, fmt.Errorf("git ls-tree: %w", waitErr)
	}
	return entries, nil
}
