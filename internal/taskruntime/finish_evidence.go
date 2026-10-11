package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultSourcePollInterval = 10 * time.Second

// errRecoveryIncomplete is the expected consequence of a refused finish: the
// worktree and branches are still there. Other evidence errors stay visible.
var errRecoveryIncomplete = errors.New("task recovery is incomplete")

func (s *Service) receipt(ctx context.Context, execution Execution, status, reason string, diagnostics []CommandDiagnostic, sourceHeadBefore, baseHead, taskHead string, noFetch bool) (Receipt, error) {
	recorded := Receipt{
		Task: execution.Task, Operation: "finish", Status: status, Owner: execution.Owner,
		Branch: execution.Branch, Worktree: execution.Worktree, Reason: reason, CreatedAt: s.now().UTC(),
		Diagnostics: diagnostics, SourceHeadBefore: sourceHeadBefore, BaseHead: baseHead, TaskHead: taskHead,
		ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
	probe := func(dir string, args ...string) (CommandResult, error) {
		result, err := s.command(ctx, dir, "git", args...)
		recorded.Diagnostics = append(recorded.Diagnostics, diagnostic(result))
		return result, err
	}
	source, err := probe(s.root, "rev-parse", "--verify", sourceTrackingRef(execution.Source))
	if err != nil || source.ExitCode != 0 {
		return recorded, fmt.Errorf("verify source head")
	}
	recorded.SourceHead = strings.TrimSpace(source.Stdout)
	pushed, err := sourceIsPushed(probe, s.root, execution.Source)
	if err != nil {
		return recorded, err
	}
	recorded.SourcePushed = pushed
	if !recorded.SourcePushed {
		return recorded, fmt.Errorf("source branch is not pushed")
	}
	_, err = os.Stat(execution.Worktree)
	recorded.WorktreeRemoved = os.IsNotExist(err)
	if err != nil && !os.IsNotExist(err) {
		return recorded, fmt.Errorf("verify worktree removal: %w", err)
	}
	local, err := probe(s.root, "show-ref", "--verify", "--quiet", "refs/heads/"+execution.Branch)
	if err != nil {
		return recorded, fmt.Errorf("verify local branch removal: %w", err)
	}
	if local.ExitCode != 0 && local.ExitCode != 1 {
		return recorded, fmt.Errorf("verify local branch removal: exit %d", local.ExitCode)
	}
	recorded.LocalBranchRemoved = local.ExitCode == 1
	remoteArgs, removedExit := []string{"ls-remote", "--exit-code", "--heads", "origin", execution.Branch}, 2
	if noFetch {
		remoteArgs, removedExit = []string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/" + execution.Branch}, 1
	}
	remote, err := probe(s.root, remoteArgs...)
	if err != nil {
		return recorded, fmt.Errorf("verify remote branch removal: %w", err)
	}
	if remote.ExitCode != 0 && remote.ExitCode != removedExit {
		return recorded, fmt.Errorf("verify remote branch removal: exit %d", remote.ExitCode)
	}
	recorded.RemoteBranchRemoved = remote.ExitCode == removedExit
	if !recorded.WorktreeRemoved || !recorded.LocalBranchRemoved || !recorded.RemoteBranchRemoved {
		return recorded, errRecoveryIncomplete
	}
	return recorded, nil
}

func sourceIsPushed(probe func(string, ...string) (CommandResult, error), dir, source string) (bool, error) {
	local, err := probe(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+source)
	if err != nil || (local.ExitCode != 0 && local.ExitCode != 1) {
		return false, fmt.Errorf("verify source push")
	}
	if local.ExitCode == 1 {
		return true, nil
	}
	pushed, err := probe(dir, "rev-list", "--count", sourceTrackingRef(source)+"..refs/heads/"+source)
	if err != nil || pushed.ExitCode != 0 {
		return false, fmt.Errorf("verify source push")
	}
	return strings.TrimSpace(pushed.Stdout) == "0", nil
}

func (s *Service) branchBaseIsStale(ctx context.Context, execution Execution) bool {
	if execution.Worktree == "" || execution.Source == "" {
		return false
	}
	result, err := s.command(ctx, execution.Worktree, "git", "merge-base", "--is-ancestor", sourceTrackingRef(execution.Source), "HEAD")
	if err != nil {
		return false
	}
	return result.ExitCode == 1
}

// watchSourceTip polls the source tracking ref while the integration check
// runs and cancels the returned context once the ref leaves startTip. An
// unreadable ref is never treated as movement.
func (s *Service) watchSourceTip(ctx context.Context, execution Execution, startTip string) (context.Context, func() string, func()) {
	watched, cancel := context.WithCancel(ctx)
	var (
		mu    sync.Mutex
		moved string
		done  = make(chan struct{})
	)
	interval := s.sourcePollInterval
	if interval <= 0 {
		interval = defaultSourcePollInterval
	}
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-watched.Done():
				return
			case <-ticker.C:
			}
			result, err := s.command(watched, s.root, "git", "rev-parse", "--verify", sourceTrackingRef(execution.Source))
			if err != nil || result.ExitCode != 0 {
				continue
			}
			if tip := strings.TrimSpace(result.Stdout); tip != "" && tip != startTip {
				mu.Lock()
				moved = tip
				mu.Unlock()
				cancel()
				return
			}
		}
	}()
	stop := func() {
		cancel()
		<-done
	}
	movedTo := func() string {
		mu.Lock()
		defer mu.Unlock()
		return moved
	}
	return watched, movedTo, stop
}
