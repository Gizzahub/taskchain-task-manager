package taskruntime

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// Settle records a local Git proof that a missing worktree's surviving
// branch tip is contained in its named source branch. It never fetches
// and never mutates a ref. There is no owner check.
func (s *Service) Settle(ctx context.Context, task string) (Response, int) {
	task = NormalizeIdentity(task)
	if task == "" {
		return response(StatusBlocked, task, "task is required", "ce task run-settle <task>"), 1
	}
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor"), 1
	}
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata"), 1
	}
	execution, found := findExecution(executions, task)
	if !found {
		return response(StatusUnknown, task, "task execution was not found", "run ce task run-list"), 1
	}
	if refused, invalid := purposeMutationRefusal(execution); invalid {
		return refused, 1
	}
	observedReceipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task receipts"), 1
	}
	if observedReceipts[task].Status == StatusFinishing {
		return response(StatusBlocked, task, "run-settle refuses an execution with a FINISHING claim", "wait for the finishing claimant or use run-status"), 1
	}
	var taskHead, sourceHead string
	if !TerminalStatus(observedReceipts[task].Status) {
		taskHead, sourceHead, err = s.settlementEvidence(ctx, execution)
		if err != nil {
			return response(StatusBlocked, task, err.Error(), "inspect the surviving task and source refs"), 1
		}
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect the mutation lock with run-doctor"), 1
	}
	defer unlock()
	executions, err = store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata"), 1
	}
	current, found := findExecution(executions, task)
	if !found || !containsExecutionGeneration(executions, execution) {
		return response(StatusBlocked, task, "execution changed while settlement evidence was collected", "run-status"), 1
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, "read task receipts: "+err.Error(), "inspect task runtime receipt evidence"), 1
	}
	if refused, blocked := requestDispositionRefusal(current, receipts[task]); blocked {
		return refused, 1
	}
	if previous := receipts[task]; TerminalStatus(previous.Status) &&
		(validTerminalAbortReceipt(current, previous) || (previous.Status == StatusDone && validateDoneReceipt(task, &current, &previous) == nil)) {
		r := response(previous.Status, task, "task execution is already "+previous.Status+": "+previous.Reason, "no action required", "run-status")
		r.Execution, r.Receipt = &current, &previous
		return r, 0
	}
	if previous := receipts[task]; TerminalStatus(previous.Status) {
		return response(StatusBlocked, task, "terminal receipt is not valid for this execution", "inspect task receipt binding"), 1
	}
	if previous := receipts[task]; previous.Status == StatusFinishing {
		return response(StatusBlocked, task, "run-settle refuses an execution with a FINISHING claim", "wait for the finishing claimant or use run-status"), 1
	}
	if taskHead == "" || sourceHead == "" {
		return response(StatusBlocked, task, "terminal evidence changed before settlement", "retry after inspecting receipts"), 1
	}
	if _, err := os.Stat(current.Worktree); !os.IsNotExist(err) {
		return response(StatusBlocked, task, "recorded worktree still exists", "finish or discard the live execution"), 1
	}
	verifiedTask, verifiedSource, err := s.settlementEvidence(ctx, current)
	if err != nil || verifiedTask != taskHead || verifiedSource != sourceHead {
		return response(StatusBlocked, task, "settlement evidence changed before recording", "retry after inspecting refs"), 1
	}
	performedBy, err := s.owner(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "fix local task identity"), 1
	}
	startedAt := current.StartedAt
	receipt := Receipt{
		Purpose: EffectivePurpose(current.Purpose),
		Task:    task, Operation: "settle", Status: StatusAborted, Owner: current.Owner, PerformedBy: &performedBy,
		Branch: current.Branch, Worktree: current.Worktree,
		Reason:    "CE recorded settlement: the surviving task branch tip is contained in the named source branch",
		CreatedAt: s.now().UTC(), ExecutionStartedAt: &startedAt, ExecutionType: current.Type, Source: current.Source,
		TaskHead: taskHead, SourceHead: sourceHead, ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
	if err := store.appendReceipt(receipt); err != nil {
		return response(StatusBlocked, task, "persist settlement receipt failed: "+err.Error(), "repair task runtime storage and retry"), 1
	}
	r := response(StatusAborted, task, receipt.Reason, "no action required", "run-status")
	r.Execution, r.Receipt = &current, &receipt
	return r, 0
}

func (s *Service) settlementEvidence(ctx context.Context, execution Execution) (string, string, error) {
	if strings.TrimSpace(execution.Source) == "" || strings.TrimSpace(execution.Branch) == "" {
		return "", "", fmt.Errorf("settlement requires recorded task and source branch names")
	}
	resolve := func(ref string) (string, error) {
		result, err := s.command(ctx, s.root, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", ref, err)
		}
		if result.ExitCode == 1 {
			return "", nil
		}
		if result.ExitCode != 0 || !validOID(strings.TrimSpace(result.Stdout)) {
			return "", fmt.Errorf("recorded ref %s cannot be resolved to a full commit OID", ref)
		}
		return strings.TrimSpace(result.Stdout), nil
	}
	resolveNamed := func(branch string) (string, error) {
		local, err := resolve("refs/heads/" + branch)
		if err != nil {
			return "", err
		}
		remote, err := resolve("refs/remotes/origin/" + branch)
		if err != nil {
			return "", err
		}
		if local != "" && remote != "" && local != remote {
			return "", fmt.Errorf("locally visible refs for %s diverge", branch)
		}
		if local != "" {
			return local, nil
		}
		if remote != "" {
			return remote, nil
		}
		return "", fmt.Errorf("recorded branch %s has no surviving local or remote-tracking ref", branch)
	}
	task, err := resolveNamed(execution.Branch)
	if err != nil {
		return "", "", err
	}
	source, err := resolveNamed(execution.Source)
	if err != nil {
		return "", "", err
	}
	result, err := s.command(ctx, s.root, "git", "merge-base", "--is-ancestor", task, source)
	if err != nil || result.ExitCode != 0 {
		return "", "", fmt.Errorf("recorded task tip is not contained in named source ref")
	}
	return task, source, nil
}
