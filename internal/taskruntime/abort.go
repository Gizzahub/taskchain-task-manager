package taskruntime

import (
	"context"
	"os"
)

// Abort closes one execution as ABORTED by appending a receipt. It does not
// integrate the task and does not remove a worktree, branch, or remote ref.
// A second abort of an already terminal execution is a no-op.
func (s *Service) Abort(ctx context.Context, task, reason string) (Response, int) {
	task = NormalizeIdentity(task)
	if task == "" {
		return response(StatusBlocked, task, "task is required", "ce task run-abort <task>"), 1
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
	validDone := previous.Status == StatusDone && validateDoneReceipt(task, &execution, &previous) == nil
	if validTerminalAbortReceipt(execution, previous) || validDone {
		r := response(previous.Status, task, "task execution is already "+previous.Status+": "+previous.Reason, "no action required", "run-status")
		r.Execution, r.Receipt = &execution, &previous
		return r, 0
	}
	owner, err := s.owner(ctx)
	if err != nil || !sameOwner(execution.Owner, owner) {
		res := response(StatusBlocked, task, "only "+execution.Owner.String()+" may abort this task", "run as the recorded owner")
		appendIdentityDiagnostic(&res, err)
		return res, 1
	}
	receipt := Receipt{
		Task: task, Operation: "abort", Status: StatusAborted,
		Owner: execution.Owner, Branch: execution.Branch, Worktree: execution.Worktree,
		Reason: abortReason(reason), CreatedAt: s.now().UTC(),
	}
	_, statErr := os.Stat(execution.Worktree)
	receipt.WorktreeRemoved = os.IsNotExist(statErr)
	if err := store.appendReceipt(receipt); err != nil {
		return response(StatusBlocked, task, "persist abort receipt failed: "+err.Error(), "repair task runtime storage and retry"), 1
	}
	r := response(StatusAborted, task, receipt.Reason, "no action required", "run-status")
	r.Execution, r.Receipt = &execution, &receipt
	if !receipt.WorktreeRemoved {
		r.Warnings = append(r.Warnings, "task worktree still exists at "+execution.Worktree+"; abort does not remove worktrees, branches, or remote refs")
	}
	return r, 0
}

// abortReason keeps a caller's words distinguishable from the runtime's own.
// Abort does not look at Git, so the prefix does not claim the work never landed.
func abortReason(reason string) string {
	if reason == "" {
		return "execution closed by run-abort; CE did not integrate it"
	}
	return "execution closed by run-abort; CE did not integrate it: " + reason
}
