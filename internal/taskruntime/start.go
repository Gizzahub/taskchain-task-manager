package taskruntime

import (
	"context"
	"fmt"
	"time"
)

// Start creates one execution. An unfinished execution is never handed back
// unless resume is set: actor, host, and kind cannot tell two sessions of
// the same owner apart.
func (s *Service) Start(ctx context.Context, task, kind string, resume bool) Response {
	task = NormalizeIdentity(task)
	if task == "" || !ValidType(kind) {
		return response(StatusBlocked, task, "task and a valid --type are required", "use feat|fix|refactor|docs|test|chore|perf")
	}
	owner, _, err := s.resolveOwner(ctx)
	if err != nil {
		res := response(StatusBlocked, task, err.Error(), identityNextAction(err))
		appendIdentityDiagnostic(&res, err)
		return res
	}
	cfg, err := s.config()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
	}
	doctor := s.Doctor(ctx)
	if doctor.Status == StatusBlocked {
		doctor.Task = task
		return doctor
	}
	identityWarnings := doctor.Warnings
	worktreeRoot, err := s.worktreeRoot(cfg, owner.Host)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "declare this host in worktree-roots")
	}
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "retry after the current mutation completes")
	}
	defer unlock()
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata")
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata")
	}
	// A closed execution does not hold its task. The record is replaced in
	// place and the earlier receipts stay. The execution is saved before the
	// start receipt so a failed append cannot make the old record look live.
	replace := -1
	for i := range executions {
		if executions[i].Task != task {
			continue
		}
		previous := receipts[task]
		if previous.Status == StatusAborted && !validTerminalAbortReceipt(executions[i], previous) {
			res := response(StatusBlocked, task, "abort receipt does not bind and close this execution", "inspect task runtime receipt evidence", "run-status", "run-abort")
			res.Execution, res.Receipt = &executions[i], &previous
			return res
		}
		validDone := previous.Status == StatusDone && validateDoneReceipt(task, &executions[i], &previous) == nil
		if validTerminalAbortReceipt(executions[i], previous) || validDone {
			replace = i
			break
		}
		if previous.Status == StatusDone {
			res := response(StatusBlocked, task, "finish receipt does not prove this execution completed", "inspect task runtime receipt evidence", "run-status", "run-abort")
			res.Execution, res.Receipt = &executions[i], &previous
			return res
		}
		if !sameOwner(executions[i].Owner, owner) {
			return response(StatusBlocked, task, "task is owned by "+executions[i].Owner.String(), "ask the owner to finish the task")
		}
		if !resume {
			existing := executions[i]
			reason := fmt.Sprintf("task already has an unfinished execution (branch %s, worktree %s, started %s, owner %s); it may belong to another session of the same owner",
				existing.Branch, existing.Worktree, existing.StartedAt.UTC().Format(time.RFC3339), existing.Owner.String())
			res := response(StatusBlocked, task, reason,
				"if this execution is yours, run ce task run-start "+task+" --type "+kind+" --resume; otherwise inspect it with run-status or end it with run-abort",
				"run-status", "run-abort")
			res.Execution = &executions[i]
			return res
		}
		res := response(StatusActive, task, "existing execution returned", "run ce task run-status "+task+" to evaluate finish readiness", "run-status")
		res.Execution = &executions[i]
		res.Warnings = identityWarnings
		return res
	}
	source, err := s.sourceBranch(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "declare branch.integrationBranch in .gz-git.yaml")
	}
	base, err := s.sourceTip(ctx, source)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "fetch origin "+source+" in the primary checkout and retry")
	}
	branch := fmt.Sprintf("dev/%s/%s/%s/%s", owner.Actor, owner.Host, kind, task)
	worktree := expectedWorktreePath(worktreeRoot, branch)
	result, err := s.runner.Run(ctx, CommandRequest{Command: "wt", Args: []string{"--config-set", worktreePathOverride(worktree), "switch", "--create", "--base", base, "--no-cd", "--format=json", branch}, WorkDir: s.root})
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
	}
	if result.ExitCode != 0 {
		res := response(StatusBlocked, task, "Worktrunk could not create the worktree", "resolve Worktrunk diagnostics and retry")
		res.Diagnostics = []CommandDiagnostic{diagnostic(result)}
		return res
	}
	inventory, inventoryErr := s.worktrunkList(ctx)
	if inventoryErr != nil {
		res := response(StatusBlocked, task, "worktree was created but its Worktrunk inventory could not be verified: "+inventoryErr.Error(), "inspect the orphaned worktree before retrying")
		res.Diagnostics = []CommandDiagnostic{diagnostic(result), diagnostic(inventory.Diagnostic)}
		return res
	}
	actual, listed := inventory.Paths[branch]
	switch {
	case !listed:
		return response(StatusBlocked, task,
			"Worktrunk did not list the worktree it just created for "+branch,
			"inspect "+worktree+" and the Worktrunk inventory; an item with no branch, such as a detached worktree, is skipped")
	case !within(actual, worktreeRoot):
		return response(StatusBlocked, task,
			"Worktrunk returned a worktree outside CE's configured path: "+actual,
			"remove the unexpected worktree manually and inspect Worktrunk configuration")
	case actual != worktree:
		return response(StatusBlocked, task,
			"Worktrunk created the worktree at "+actual+", not at the configured "+worktree,
			"remove the unexpected worktree manually and inspect Worktrunk configuration")
	}
	now := s.now().UTC()
	execution := Execution{Task: task, Type: kind, Source: source, Owner: owner, Branch: branch, Worktree: worktree, StartedAt: now, UpdatedAt: now}
	if replace >= 0 {
		executions[replace] = execution
	} else {
		executions = append(executions, execution)
	}
	startReceipt := Receipt{Task: task, Operation: "start", Status: StatusActive, Owner: owner, Branch: branch, Worktree: worktree, Reason: "worktree created and inventory verified", CreatedAt: now, Diagnostics: []CommandDiagnostic{diagnostic(result), diagnostic(inventory.Diagnostic)}}
	if err := store.save(executions); err != nil {
		startReceipt.Status, startReceipt.Reason, startReceipt.CreatedAt = StatusBlocked, "worktree was created but execution metadata persistence failed: "+err.Error(), s.now().UTC()
		if receiptErr := store.appendReceipt(startReceipt); receiptErr != nil {
			startReceipt.Reason += "; persist failure receipt failed: " + receiptErr.Error()
		}
		res := response(StatusBlocked, task, startReceipt.Reason, "inspect the orphaned worktree and runtime evidence before retrying")
		res.Receipt, res.Diagnostics = &startReceipt, startReceipt.Diagnostics
		return res
	}
	if err := store.appendReceipt(startReceipt); err != nil {
		res := response(StatusBlocked, task, "execution metadata was saved but start receipt persistence failed: "+err.Error(), "retry run-start with --resume to recover the existing execution")
		res.Execution, res.Receipt, res.Diagnostics = &execution, &startReceipt, startReceipt.Diagnostics
		return res
	}
	res := response(StatusActive, task, "task execution started", "work in the task worktree; use run-status to evaluate finish readiness; run-finish runs from the primary checkout, after the repository lint passes and the branch is current with the source tip", "run-status")
	res.Execution, res.Diagnostics = &execution, []CommandDiagnostic{diagnostic(result), diagnostic(inventory.Diagnostic)}
	res.Warnings = identityWarnings
	return res
}
