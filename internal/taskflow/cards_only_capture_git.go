package taskflow

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"unicode"
)

// captureBinding observes Git's actual worktree, private gitdir and index path.
// It detects observed identity changes, not transient ABA substitutions.
type captureBinding struct {
	root, gitdir, index          string
	rootInfo, gitInfo, indexInfo fs.FileInfo
}

func newCaptureBinding(ctx context.Context, root string) (captureBinding, error) {
	b := captureBinding{}
	if strings.ContainsFunc(root, unicode.IsControl) {
		return b, fmt.Errorf("unsafe repository root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return b, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return b, err
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return b, fmt.Errorf("repository root must be a real directory")
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return b, err
	}
	top, err := captureGit(ctx, abs, 64<<10, "rev-parse", "--show-toplevel")
	if err != nil {
		return b, err
	}
	if filepath.Clean(strings.TrimSuffix(string(top), "\n")) != abs {
		return b, fmt.Errorf("capture requires the actual Git worktree root")
	}
	gitdir, err := captureGit(ctx, abs, 64<<10, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return b, err
	}
	index, err := captureGit(ctx, abs, 64<<10, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return b, err
	}
	b = captureBinding{root: abs, gitdir: strings.TrimSuffix(string(gitdir), "\n"), index: strings.TrimSuffix(string(index), "\n"), rootInfo: info}
	b.gitInfo, err = os.Lstat(b.gitdir)
	if err != nil {
		return b, err
	}
	if !b.gitInfo.IsDir() || b.gitInfo.Mode()&fs.ModeSymlink != 0 {
		return b, fmt.Errorf("Git directory must be a real directory")
	}
	b.indexInfo, err = os.Lstat(b.index)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	} else if err == nil && !b.indexInfo.Mode().IsRegular() {
		return b, fmt.Errorf("Git index is not a regular file")
	}
	return b, err
}

func (b captureBinding) verify(ctx context.Context, index bool) error {
	after, err := newCaptureBinding(ctx, b.root)
	if err != nil {
		return err
	}
	if b.gitdir != after.gitdir || b.index != after.index || !os.SameFile(b.rootInfo, after.rootInfo) || !os.SameFile(b.gitInfo, after.gitInfo) {
		return fmt.Errorf("Git authority changed during capture")
	}
	if index {
		if (b.indexInfo == nil) != (after.indexInfo == nil) {
			return fmt.Errorf("Git index appeared or disappeared during capture")
		}
		if b.indexInfo != nil && (!os.SameFile(b.indexInfo, after.indexInfo) || b.indexInfo.Size() != after.indexInfo.Size() || !b.indexInfo.ModTime().Equal(after.indexInfo.ModTime())) {
			return fmt.Errorf("Git index identity changed during capture")
		}
	}
	return nil
}

func captureGitEnvironment() error {
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if value == "" || !strings.HasPrefix(key, "GIT_") {
			continue
		}
		// These are CE branch identity, not Git authority or command options.
		if key == "GIT_WORK_ACTOR" || key == "GIT_WORK_HOST" {
			continue
		}
		if (key == "GIT_PAGER" && value == "cat") || ((key == "GIT_OPTIONAL_LOCKS" || key == "GIT_TERMINAL_PROMPT") && value == "0") {
			continue
		}
		return fmt.Errorf("board capture refuses Git environment override %s", key)
	}
	return nil
}

func captureGit(ctx context.Context, root string, limit int, args ...string) ([]byte, error) {
	if err := captureGitEnvironment(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	base := []string{"--no-pager", "--no-optional-locks", "--no-replace-objects", "--no-lazy-fetch", "--literal-pathspecs", "-c", "core.fsmonitor=false", "-c", "log.showSignature=false"}
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0")
	out := &captureOutput{limit: limit, cancel: cancel}
	stderr := &captureOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("board capture git %s: %w: %s", args[0], err, stderr.String())
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("board capture git %s produced diagnostics: %s", args[0], stderr.String())
	}
	return out.Bytes(), nil
}
