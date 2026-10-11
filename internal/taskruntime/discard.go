package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Discard removes the exact clean worktree recorded for an unintegrated task.
// It writes an intent receipt before asking Worktrunk to mutate anything.
// no-fetch cannot prove a remote branch is absent, so that policy is refused.
func (s *Service) Discard(ctx context.Context, task, reason, takeOverFrom string) (Response, int) {
	task, reason = NormalizeIdentity(task), strings.TrimSpace(reason)
	if task == "" || reason == "" {
		return response(StatusBlocked, task, "task and non-empty discard reason are required", "ce task run-discard <task> --reason R"), 1
	}
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor"), 1
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect the mutation lock with run-doctor"), 1
	}
	defer unlock()
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata"), 1
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, "read task receipts: "+err.Error(), "inspect task runtime receipt evidence"), 1
	}
	execution, found := findExecution(executions, task)
	if !found {
		return response(StatusUnknown, task, "task execution was not found", "ce task run-list", "run-list"), 1
	}
	if previous := receipts[task]; terminalAbortReceipt(previous) && previous.Operation == "discard" && receiptBindsExecution(execution, previous) {
		r := response(StatusAborted, task, "task execution is already discarded: "+previous.Reason, "no action required", "run-status")
		r.Execution, r.Receipt = &execution, &previous
		return r, 0
	}
	if previous := receipts[task]; previous.Status == StatusDone {
		return response(StatusBlocked, task, "run-discard refuses an execution with a DONE receipt", "inspect task runtime receipt evidence"), 1
	}
	if previous := receipts[task]; isPartialCleanupReceipt(execution, previous) {
		return response(StatusBlocked, task, "run-discard refuses an execution with recorded integration cleanup failure", "use ce task run-recover to preserve the integrated outcome"), 1
	}
	current, err := s.owner(ctx)
	if err != nil {
		res := response(StatusBlocked, task, "resolve current task owner: "+err.Error(), "repair the CE local identity configuration")
		appendIdentityDiagnostic(&res, err)
		return res, 1
	}
	performedBy, err := discardPerformer(execution.Owner, current, takeOverFrom)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run as the recorded owner or provide the exact --take-over-from actor/host"), 1
	}
	cfg, err := s.config()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "repair task runtime configuration"), 1
	}
	if cfg.IntegrationNetworkPolicy == NetworkNoFetch {
		return response(StatusBlocked, task, "run-discard requires integration-network-policy allow because no-fetch cannot prove remote branch absence", "set integration-network-policy to allow, then retry run-discard"), 1
	}
	worktreeRoot, err := s.worktreeRoot(cfg, execution.Owner.Host)
	if err != nil {
		return response(StatusBlocked, task, "resolve recorded owner worktree root: "+err.Error(), "repair task runtime configuration"), 1
	}
	worktree, err := canonicalizePotentialPath(execution.Worktree)
	if err != nil {
		return response(StatusBlocked, task, "resolve recorded worktree path: "+err.Error(), "inspect task runtime metadata"), 1
	}
	expected := expectedWorktreePath(worktreeRoot, execution.Branch)
	if worktree != expected || !within(worktree, worktreeRoot) {
		return response(StatusBlocked, task, "execution worktree is outside its configured owner path", "inspect stale or tampered execution metadata"), 1
	}
	if conflict := conflictingDiscardExecution(executions, receipts, execution); conflict != "" {
		return response(StatusBlocked, task, "task execution conflicts with live execution "+conflict, "resolve duplicate runtime metadata before discarding"), 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		return response(StatusBlocked, task, "resolve control working directory: "+err.Error(), "run from the primary/control checkout"), 1
	}
	cwd, err = canonicalizePotentialPath(cwd)
	if err != nil {
		return response(StatusBlocked, task, "resolve control working directory: "+err.Error(), "run from the primary/control checkout"), 1
	}
	if within(cwd, worktree) {
		return response(StatusBlocked, task, "run-discard refuses to remove its current worktree", "run from the primary/control checkout"), 1
	}
	_, worktreeErr := os.Stat(worktree)
	if worktreeErr != nil && !os.IsNotExist(worktreeErr) {
		return response(StatusBlocked, task, "inspect recorded worktree: "+worktreeErr.Error(), "inspect the recorded worktree before retrying"), 1
	}
	if os.IsNotExist(worktreeErr) && isDiscardIntent(execution, receipts[task]) {
		verified, verifyErr := s.discardRemovalEvidence(ctx, execution, nil, cfg)
		if verifyErr != nil {
			blocked := discardReceipt(execution, StatusBlocked, verifyErr.Error(), performedBy, verified, "discard removal is incomplete or unproven", s.now().UTC())
			if err := store.appendReceipt(blocked); err != nil {
				verifyErr = fmt.Errorf("%v; persist failure evidence failed: %w", verifyErr, err)
			}
			r := response(StatusBlocked, task, verifyErr.Error(), "inspect residual worktree or refs before retrying")
			r.Execution, r.Receipt, r.Diagnostics = &execution, &blocked, verified
			return r, 1
		}
		final := discardReceipt(execution, StatusAborted, reason, performedBy, verified, "discard completion recovered from prior intent", s.now().UTC())
		final.WorktreeRemoved, final.LocalBranchRemoved, final.RemoteBranchRemoved = true, true, true
		if err := store.appendReceipt(final); err != nil {
			r := response(StatusBlocked, task, "discard cleanup is proven but receipt persistence failed: "+err.Error(), "repair task runtime storage and retry")
			r.Execution, r.Receipt, r.Diagnostics = &execution, &final, verified
			return r, 1
		}
		r := response(StatusAborted, task, final.Reason, "no action required", "run-status")
		r.Execution, r.Receipt, r.Diagnostics = &execution, &final, verified
		return r, 0
	}
	if worktreeErr != nil {
		return response(StatusBlocked, task, "recorded worktree is unavailable: "+worktreeErr.Error(), "inspect the recorded worktree before retrying"), 1
	}
	inventory, err := s.worktrunkList(ctx)
	if err != nil {
		r := response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
		r.Diagnostics = []CommandDiagnostic{diagnostic(inventory.Diagnostic)}
		return r, 1
	}
	if actual, ok := inventory.Paths[execution.Branch]; !ok || actual != execution.Worktree {
		return response(StatusBlocked, task, "Worktrunk inventory does not exactly match execution branch and path", "inspect Worktrunk and runtime metadata"), 1
	}
	diagnostics, err := s.discardPreconditions(ctx, execution, worktree, cfg)
	if err != nil {
		r := response(StatusBlocked, task, err.Error(), "resolve the Git precondition and retry")
		r.Diagnostics = diagnostics
		return r, 1
	}
	intent := discardReceipt(execution, StatusBlocked, reason, performedBy, diagnostics, "discard intent recorded before Worktrunk removal", s.now().UTC())
	if err := store.appendReceipt(intent); err != nil {
		return response(StatusBlocked, task, "persist discard intent receipt failed: "+err.Error(), "repair task runtime storage and retry"), 1
	}
	removed, runErr := s.command(ctx, s.root, "wt", "remove", "--no-hooks", "--foreground", "--format=json", "-y", "-D", execution.Worktree)
	diagnostics = append(diagnostics, diagnostic(removed))
	if runErr != nil || removed.ExitCode != 0 {
		reason := "Worktrunk removal did not complete"
		if runErr != nil {
			reason += ": " + runErr.Error()
		}
		blocked := discardReceipt(execution, StatusBlocked, reason, performedBy, diagnostics, "discard intent retained after Worktrunk removal failure", s.now().UTC())
		if err := store.appendReceipt(blocked); err != nil {
			reason += "; persist failure evidence failed: " + err.Error()
		}
		r := response(StatusBlocked, task, reason, "inspect Worktrunk diagnostics and retry")
		r.Execution, r.Receipt, r.Diagnostics = &execution, &blocked, diagnostics
		return r, 1
	}
	verified, verifyErr := s.discardRemovalEvidence(ctx, execution, diagnostics, cfg)
	if verifyErr != nil {
		blocked := discardReceipt(execution, StatusBlocked, verifyErr.Error(), performedBy, verified, "discard removal is incomplete or unproven", s.now().UTC())
		if err := store.appendReceipt(blocked); err != nil {
			verifyErr = fmt.Errorf("%v; persist failure evidence failed: %w", verifyErr, err)
		}
		r := response(StatusBlocked, task, verifyErr.Error(), "inspect residual worktree or refs before retrying")
		r.Execution, r.Receipt, r.Diagnostics = &execution, &blocked, verified
		return r, 1
	}
	final := discardReceipt(execution, StatusAborted, reason, performedBy, verified, "discard completed", s.now().UTC())
	final.WorktreeRemoved, final.LocalBranchRemoved, final.RemoteBranchRemoved = true, true, true
	if err := store.appendReceipt(final); err != nil {
		r := response(StatusBlocked, task, "discard completed but receipt persistence failed: "+err.Error(), "repair task runtime storage and retry ce task run-discard; it will re-verify removal")
		r.Execution, r.Receipt, r.Diagnostics = &execution, &final, verified
		return r, 1
	}
	r := response(StatusAborted, task, final.Reason, "no action required", "run-status")
	r.Execution, r.Receipt, r.Diagnostics = &execution, &final, verified
	return r, 0
}

func discardPerformer(recorded, current Owner, takeOverFrom string) (*Owner, error) {
	if sameOwner(recorded, current) {
		if strings.TrimSpace(takeOverFrom) != "" {
			return nil, fmt.Errorf("--take-over-from is only valid for a different actor")
		}
		copy := current
		return &copy, nil
	}
	if current.Host != recorded.Host || strings.TrimSpace(takeOverFrom) != recorded.String() {
		return nil, fmt.Errorf("only %s may discard this task; cross-actor takeover requires exact --take-over-from %s on host %s", recorded.String(), recorded.String(), recorded.Host)
	}
	copy := current
	return &copy, nil
}

func isDiscardIntent(execution Execution, receipt Receipt) bool {
	return receipt.Operation == "discard" && receipt.Status == StatusBlocked && receiptBindsExecution(execution, receipt)
}

func conflictingDiscardExecution(executions []Execution, receipts map[string]Receipt, target Execution) string {
	for _, other := range executions {
		if other.Task == target.Task || validTerminalReceipt(other, receipts[other.Task]) {
			continue
		}
		if other.Branch == target.Branch || filepath.Clean(other.Worktree) == filepath.Clean(target.Worktree) {
			return other.Task
		}
	}
	return ""
}

func (s *Service) discardPreconditions(ctx context.Context, execution Execution, worktree string, cfg Config) ([]CommandDiagnostic, error) {
	checks := []struct {
		args   []string
		reason string
	}{
		{[]string{"status", "--porcelain", "--untracked-files=all"}, "task worktree is dirty"},
		{[]string{"branch", "--show-current"}, "recorded branch does not match the worktree branch"},
		{discardRemoteProbe(execution.Branch, cfg), "task branch still exists on origin"},
	}
	diagnostics := make([]CommandDiagnostic, 0, len(checks))
	for i, check := range checks {
		dir := worktree
		if i == 2 {
			dir = s.root
		}
		result, err := s.command(ctx, dir, "git", check.args...)
		diagnostics = append(diagnostics, diagnostic(result))
		if err != nil {
			return diagnostics, fmt.Errorf("%s: %w", check.reason, err)
		}
		switch i {
		case 0:
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "" {
				return diagnostics, errors.New(check.reason)
			}
		case 1:
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != execution.Branch {
				return diagnostics, errors.New(check.reason)
			}
		case 2:
			if result.ExitCode != discardRemoteAbsentExit(cfg) {
				return diagnostics, errors.New(check.reason)
			}
		}
	}
	return diagnostics, nil
}

func (s *Service) discardRemovalEvidence(ctx context.Context, execution Execution, diagnostics []CommandDiagnostic, cfg Config) ([]CommandDiagnostic, error) {
	if _, err := os.Stat(execution.Worktree); err == nil || !os.IsNotExist(err) {
		if err != nil {
			return diagnostics, fmt.Errorf("inspect removed worktree: %w", err)
		}
		return diagnostics, fmt.Errorf("recorded worktree still exists after Worktrunk removal")
	}
	checks := [][]string{{"show-ref", "--verify", "--quiet", "refs/heads/" + execution.Branch}, discardRemoteProbe(execution.Branch, cfg)}
	for index, args := range checks {
		result, err := s.command(ctx, s.root, "git", args...)
		diagnostics = append(diagnostics, diagnostic(result))
		if err != nil {
			return diagnostics, fmt.Errorf("verify discarded refs: %w", err)
		}
		wanted := 1
		if args[0] != "show-ref" {
			wanted = discardRemoteAbsentExit(cfg)
		} else if strings.Contains(args[len(args)-1], "refs/remotes/") {
			wanted = discardRemoteAbsentExit(cfg)
		}
		if result.ExitCode != wanted {
			name := "remote branch"
			if index == 0 {
				name = "local branch"
			}
			return diagnostics, fmt.Errorf("discarded %s still exists or could not be verified", name)
		}
	}
	inventory, err := s.worktrunkList(ctx)
	diagnostics = append(diagnostics, diagnostic(inventory.Diagnostic))
	if err != nil {
		return diagnostics, fmt.Errorf("verify Worktrunk removal: %w", err)
	}
	if _, exists := inventory.Paths[execution.Branch]; exists {
		return diagnostics, fmt.Errorf("worktrunk still lists the discarded branch")
	}
	return diagnostics, nil
}

func discardRemoteProbe(branch string, cfg Config) []string {
	if cfg.IntegrationNetworkPolicy == NetworkNoFetch {
		return []string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/" + branch}
	}
	return []string{"ls-remote", "--exit-code", "--heads", "origin", branch}
}

func discardRemoteAbsentExit(cfg Config) int {
	if cfg.IntegrationNetworkPolicy == NetworkNoFetch {
		return 1
	}
	return 2
}

func discardReceipt(execution Execution, status, reason string, performedBy *Owner, diagnostics []CommandDiagnostic, prefix string, createdAt time.Time) Receipt {
	return Receipt{
		Task: execution.Task, Operation: "discard", Status: status, Owner: execution.Owner, PerformedBy: performedBy,
		Branch: execution.Branch, Worktree: execution.Worktree, Reason: prefix + ": " + reason, CreatedAt: createdAt,
		Diagnostics: diagnostics, ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
}
