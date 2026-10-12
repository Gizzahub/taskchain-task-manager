package taskruntime

import (
	"context"
	"fmt"
	"os"
	"strings"
)

func (s *Service) deriveState(ctx context.Context, execution Execution, receipt Receipt, inventory worktrunkInventory, locked bool) DerivedState {
	state := DerivedState{Execution: execution, Status: StatusActive,
		AllowedActions: []string{"run-status", "run-abort"}, Reason: "task execution is active", NextAction: "continue in the task worktree"}
	block := func(reason, next string) DerivedState {
		state.Status, state.Reason, state.NextAction = StatusBlocked, reason, next
		state.AllowedActions = []string{"run-status", "run-abort"}
		return state
	}
	if TerminalStatus(receipt.Status) {
		state.Status, state.Reason, state.NextAction = receipt.Status, receipt.Reason, "no action required"
		state.AllowedActions = []string{"run-status"}
		return state
	}
	if isPartialCleanupReceipt(execution, receipt) {
		state.Status = StatusBlocked
		state.AllowedActions = []string{"run-status", "run-recover", "run-abort"}
		state.Reason = receipt.Reason
		state.NextAction = "run ce task run-recover " + execution.Task
		return state
	}
	if receipt.Status == StatusFinishing {
		state.FinishReady = false
		state.AllowedActions = []string{"run-status"}
		state.Reason = "run-finish is waiting for the integration provider"
		state.NextAction = "wait for the finishing claimant or inspect run-status"
		if s.claimLiveness(ctx, receipt) == claimDead {
			state.Status = StatusBlocked
			state.AllowedActions = []string{"run-status", "run-recover"}
			state.Reason = "run-finish claimant ended without recording an integration outcome"
			state.NextAction = "run-recover " + execution.Task
		}
		return state
	}
	if locked {
		return block("repository task runtime mutation is in progress or stale", "inspect the mutation lock with run-doctor")
	}
	cfg, err := s.config()
	if err != nil {
		return block("load task runtime configuration: "+err.Error(), "repair task runtime configuration")
	}
	root, err := s.worktreeRoot(cfg, execution.Owner.Host)
	if err != nil || execution.Worktree != expectedWorktreePath(root, execution.Branch) {
		return block("execution worktree is outside its configured owner path", "inspect stale or tampered execution metadata")
	}
	actual, ok := inventory.Paths[execution.Branch]
	if !ok || actual != execution.Worktree {
		return block("Worktrunk inventory does not match execution metadata", "inspect Worktrunk and runtime metadata")
	}
	if _, err := os.Stat(execution.Worktree); err != nil {
		return block("execution metadata points to an unavailable worktree", "inspect the recorded worktree path")
	}
	probes := []struct {
		args []string
		name string
	}{
		{[]string{"branch", "--show-current"}, "branch identity"},
		{[]string{"status", "--porcelain", "--untracked-files=all"}, "clean worktree"},
		{[]string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"}, "upstream"},
		{[]string{"rev-list", "--left-right", "--count", "@{u}...HEAD"}, "upstream parity"},
	}
	for index, probe := range probes {
		result, err := s.command(ctx, execution.Worktree, "git", probe.args...)
		if err != nil {
			return block(fmt.Sprintf("derive %s: %v", probe.name, err), "inspect Git state")
		}
		state.Diagnostics = append(state.Diagnostics, diagnostic(result))
		if result.ExitCode != 0 {
			if index == 2 {
				state.Reason, state.NextAction = "task branch has no upstream", "configure and push the task branch upstream"
				return state
			}
			return block(probe.name+" is unresolved", "repair the task branch Git state")
		}
		switch index {
		case 0:
			if strings.TrimSpace(result.Stdout) != execution.Branch {
				return block("recorded branch does not match the worktree branch", "inspect stale execution metadata")
			}
		case 1:
			if strings.TrimSpace(result.Stdout) != "" {
				state.Reason, state.NextAction = "task worktree has uncommitted changes", "commit or remove task-owned changes"
				return state
			}
			state.AllowedActions = append(state.AllowedActions, "run-discard")
		case 3:
			fields := strings.Fields(result.Stdout)
			if len(fields) != 2 || fields[0] != "0" || fields[1] != "0" {
				state.Reason, state.NextAction = "task branch is ahead of or behind its upstream", "synchronize and push the task branch"
				return state
			}
		}
	}
	state.FinishReady = true
	state.AllowedActions = []string{"run-status", "run-finish", "run-abort", "run-discard"}
	state.Reason, state.NextAction = "task execution is ready to finish", "run ce task run-finish "+execution.Task
	return state
}

func stateResponse(state DerivedState) Response {
	res := response(state.Status, state.Execution.Task, state.Reason, state.NextAction, state.AllowedActions...)
	res.Execution, res.Diagnostics, res.FinishReady = &state.Execution, state.Diagnostics, state.FinishReady
	return res
}

func (s *Service) verifiedExecutionState(ctx context.Context, execution Execution, receipt Receipt, inventory worktrunkInventory, locked bool) DerivedState {
	if receipt.Status == StatusAborted && !validTerminalAbortReceipt(execution, receipt) {
		return DerivedState{Execution: execution, Status: StatusBlocked, AllowedActions: []string{"run-status", "run-abort"},
			Reason: "abort receipt does not bind and close this execution", NextAction: "inspect task runtime receipt evidence"}
	}
	if receipt.Status == StatusDone {
		if err := validateDoneReceipt(execution.Task, &execution, &receipt); err != nil {
			return DerivedState{Execution: execution, Status: StatusBlocked, AllowedActions: []string{"run-status", "run-abort"},
				Reason: "finish receipt does not prove this execution completed: " + err.Error(), NextAction: "inspect task runtime receipt evidence"}
		}
	}
	return s.deriveState(ctx, execution, receipt, inventory, locked)
}
