package taskruntime

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Recover closes the narrow exit-3 outcome from run-finish. It never invokes
// the integration provider and never removes a worktree or a ref. It verifies
// that the recorded task commit is in the pushed source branch, then appends
// a DONE receipt naming whatever cleanup left behind.
func (s *Service) Recover(ctx context.Context, task string, claimDead ...bool) (Response, int) {
	task = NormalizeIdentity(task)
	if task == "" {
		return response(StatusBlocked, task, "task is required", "ce task run-recover <task>"), 1
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
	previous := receipts[task]
	if previous.Status == StatusDone && validateDoneReceipt(task, &execution, &previous) == nil {
		r := response(StatusDone, task, "task execution is already DONE: "+previous.Reason, "no action required", "run-status")
		r.Execution, r.Receipt = &execution, &previous
		return r, 0
	}
	declared := len(claimDead) > 0 && claimDead[0]
	claimed := previous.Status == StatusFinishing
	if declared && !claimed {
		return response(StatusBlocked, task, "--claim-dead requires a FINISHING claim as the latest receipt", "run-status", "run-status"), 1
	}
	if !claimed && !isPartialCleanupReceipt(execution, previous) {
		return response(StatusBlocked, task, "run-recover requires the recorded exit-3 provider cleanup failure", "inspect task runtime receipt evidence"), 1
	}
	owner, err := s.owner(ctx)
	if err != nil || !sameOwner(execution.Owner, owner) {
		res := response(StatusBlocked, task, "only "+execution.Owner.String()+" may recover this task", "run as the recorded owner")
		appendIdentityDiagnostic(&res, err)
		return res, 1
	}
	cfg, err := s.config()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "repair task runtime configuration"), 1
	}
	if claimed {
		return s.recoverFinishClaim(ctx, store, execution, previous, cfg, declared, owner)
	}
	receipt, err := s.recoveryReceipt(ctx, execution, previous, cfg)
	if err != nil {
		return response(StatusBlocked, task, "partial cleanup recovery is not proven: "+err.Error(), "inspect source delivery and residual task paths"), 1
	}
	if err := store.appendReceipt(receipt); err != nil {
		return response(StatusBlocked, task, "persist recovery receipt failed: "+err.Error(), "repair task runtime storage and retry"), 1
	}
	r := response(StatusDone, task, receipt.Reason, "no action required", "run-status")
	r.Execution, r.Receipt, r.Diagnostics = &execution, &receipt, receipt.Diagnostics
	return r, 0
}

func (s *Service) recoveryReceipt(ctx context.Context, execution Execution, previous Receipt, cfg Config) (Receipt, error) {
	recorded := Receipt{
		Task: execution.Task, Operation: "recover", Status: StatusDone,
		Owner: execution.Owner, Branch: execution.Branch, Worktree: execution.Worktree,
		CreatedAt: s.now().UTC(), SourceHeadBefore: previous.SourceHeadBefore, BaseHead: previous.BaseHead,
		TaskHead: previous.TaskHead, ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
	probe := func(args ...string) (CommandResult, error) {
		result, err := s.command(ctx, s.root, "git", args...)
		recorded.Diagnostics = append(recorded.Diagnostics, diagnostic(result))
		return result, err
	}
	source, err := probe("rev-parse", "--verify", sourceTrackingRef(execution.Source))
	if err != nil || source.ExitCode != 0 || strings.TrimSpace(source.Stdout) == "" {
		return recorded, fmt.Errorf("resolve current source head")
	}
	recorded.SourceHead = strings.TrimSpace(source.Stdout)
	contained, err := probe("merge-base", "--is-ancestor", previous.TaskHead, recorded.SourceHead)
	if err != nil || contained.ExitCode != 0 {
		return recorded, fmt.Errorf("recorded task head is not contained in the current source")
	}
	pushed, err := sourceIsPushed(func(_ string, args ...string) (CommandResult, error) { return probe(args...) }, s.root, execution.Source)
	if err != nil || !pushed {
		return recorded, fmt.Errorf("current source branch is not pushed")
	}
	recorded.SourcePushed = true
	_, statErr := os.Stat(execution.Worktree)
	if statErr != nil && !os.IsNotExist(statErr) {
		return recorded, fmt.Errorf("inspect residual worktree: %w", statErr)
	}
	recorded.WorktreeRemoved = os.IsNotExist(statErr)
	local, err := probe("show-ref", "--verify", "--quiet", "refs/heads/"+execution.Branch)
	if err != nil || (local.ExitCode != 0 && local.ExitCode != 1) {
		return recorded, fmt.Errorf("inspect residual local branch")
	}
	recorded.LocalBranchRemoved = local.ExitCode == 1
	remoteArgs, removedExit := []string{"ls-remote", "--exit-code", "--heads", "origin", execution.Branch}, 2
	remoteName := "origin branch " + execution.Branch
	if cfg.IntegrationNetworkPolicy == NetworkNoFetch {
		remoteArgs, removedExit = []string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/" + execution.Branch}, 1
		remoteName = "origin tracking ref refs/remotes/origin/" + execution.Branch
	}
	remote, err := probe(remoteArgs...)
	if err != nil || (remote.ExitCode != 0 && remote.ExitCode != removedExit) {
		return recorded, fmt.Errorf("inspect residual remote branch")
	}
	recorded.RemoteBranchRemoved = remote.ExitCode == removedExit
	residuals := make([]string, 0, 3)
	if !recorded.WorktreeRemoved {
		residuals = append(residuals, "worktree "+execution.Worktree)
	}
	if !recorded.LocalBranchRemoved {
		residuals = append(residuals, "local branch refs/heads/"+execution.Branch)
	}
	if !recorded.RemoteBranchRemoved {
		residuals = append(residuals, remoteName)
	}
	if len(residuals) == 0 {
		return recorded, fmt.Errorf("provider cleanup left no residual path or ref to recover")
	}
	recorded.Reason = "integration confirmed after partial provider cleanup; residual " + strings.Join(residuals, ", ") + " left untouched"
	return recorded, nil
}
