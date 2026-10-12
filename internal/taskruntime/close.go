package taskruntime

import (
	"context"
	"os"
	"strings"
)

// Close records an administrator's explicit closure without asserting that
// Git work landed. It never removes a worktree or mutates a Git ref.
// An empty administrator list denies the caller.
func (s *Service) Close(ctx context.Context, task, reason string) (Response, int) {
	task, reason = NormalizeIdentity(task), strings.TrimSpace(reason)
	if task == "" || reason == "" {
		return response(StatusBlocked, task, "exact task and nonblank reason are required", "ce task run-close <task> --reason <reason>"), 1
	}
	cfg, err := s.config()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "configure task runtime administrator policy"), 1
	}
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor"), 1
	}
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata"), 1
	}
	observed, found := findExecution(executions, task)
	if !found {
		return response(StatusUnknown, task, "task execution was not found", "ce task run-list"), 1
	}
	performer, err := s.owner(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "repair local task identity"), 1
	}
	if !executionAdministrator(cfg, performer) {
		return response(StatusBlocked, task, "caller is not a repository-designated execution administrator", "run as a declared administrator"), 1
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect mutation lock"), 1
	}
	defer unlock()
	executions, err = store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata"), 1
	}
	current, found := findExecution(executions, task)
	if !found || !containsExecutionGeneration(executions, observed) {
		return response(StatusBlocked, task, "execution generation changed before closure", "inspect run-status"), 1
	}
	cfg, err = s.config()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "repair repository administrator policy"), 1
	}
	if !executionAdministrator(cfg, performer) {
		return response(StatusBlocked, task, "administrator policy changed before closure", "inspect repository administrator policy"), 1
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task receipts"), 1
	}
	previous := receipts[task]
	if validTerminalReceipt(current, previous) {
		r := response(previous.Status, task, "execution is already terminal", "no action required", "run-status")
		r.Execution, r.Receipt = &current, &previous
		return r, 0
	}
	if TerminalStatus(previous.Status) {
		return response(StatusBlocked, task, "terminal receipt is not valid for this execution", "inspect task receipt binding"), 1
	}
	if previous.Status == StatusFinishing {
		return response(StatusBlocked, task, "run-close refuses an execution with a FINISHING claim", "wait for the finishing claimant or use run-status"), 1
	}
	if _, err := os.Stat(current.Worktree); !os.IsNotExist(err) {
		return response(StatusBlocked, task, "recorded worktree is present or cannot be inspected", "resolve the live execution before administrative closure"), 1
	}
	startedAt := current.StartedAt
	receipt := Receipt{
		ExecutionStartedAt: &startedAt, ExecutionType: current.Type, Source: current.Source,
		Task: task, Operation: "close", Status: StatusAborted, Owner: current.Owner, PerformedBy: &performer,
		Branch: current.Branch, Worktree: current.Worktree,
		Reason:    "repository administrator closed execution without Git integration evidence: " + reason,
		CreatedAt: s.now().UTC(), ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
	if err := store.appendReceipt(receipt); err != nil {
		return response(StatusBlocked, task, err.Error(), "repair task receipt storage"), 1
	}
	r := response(StatusAborted, task, receipt.Reason, "no action required", "run-status")
	r.Execution, r.Receipt = &current, &receipt
	return r, 0
}
