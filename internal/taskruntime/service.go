package taskruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Service answers the run commands for one checkout. root is that checkout's
// top directory, the place .ce/task-runtime.yaml and .gz-git.yaml are read.
type Service struct {
	root   string
	runner CommandRunner
	now    func() time.Time
	// provider, when set, replaces the gz-git command line. Production leaves
	// it nil so Check and Run are the documented integrate invocations.
	provider IntegrationProvider
	// sourcePollInterval is how often run-finish re-reads the source tip
	// during the integration check. Zero selects the default.
	sourcePollInterval time.Duration
}

func NewService(root string, runner CommandRunner) *Service {
	return &Service{root: root, runner: runner, now: time.Now}
}

func response(status, task, reason, next string, actions ...string) Response {
	return Response{SchemaVersion: 1, Status: status, Task: task, AllowedActions: actions, Reason: reason, NextAction: next}
}

func diagnostic(result CommandResult) CommandDiagnostic {
	return CommandDiagnostic{
		Command: result.Command, Executable: result.Executable, Args: result.Args,
		Cwd: result.WorkDir, ExitCode: result.ExitCode, Stdout: result.Stdout,
		Stderr: result.Stderr, Error: result.Error, NotStarted: result.NotStarted,
	}
}

func (s *Service) command(ctx context.Context, dir, name string, args ...string) (CommandResult, error) {
	return s.runner.Run(ctx, CommandRequest{Command: name, Args: args, WorkDir: dir})
}

// store resolves the shared ledger. The git common dir is outside every
// worktree, so a reclaimed worktree does not take the ledger with it.
func (s *Service) store(ctx context.Context) (stateStore, error) {
	r, err := s.command(ctx, s.root, "git", "rev-parse", "--git-common-dir")
	if err != nil {
		return stateStore{}, err
	}
	if r.ExitCode != 0 {
		return stateStore{}, fmt.Errorf("resolve git common dir: %s", strings.TrimSpace(r.Stderr))
	}
	path := strings.TrimSpace(r.Stdout)
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return stateStore{}, err
	}
	return newStateStore(path), nil
}

// EnclosingRepository is the checkout that contains dir. A directory that
// is not inside a checkout is returned unchanged so the caller still has a
// place to read the declaration and report that git itself failed.
func EnclosingRepository(dir string) string {
	for current := dir; ; {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}
		current = parent
	}
}
