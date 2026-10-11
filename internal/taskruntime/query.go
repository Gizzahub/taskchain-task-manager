package taskruntime

import (
	"context"
	"sort"
)

// Status derives one execution, or the whole list when task is empty.
// It does not take the mutation lock. The human surface for an empty task
// is not the run-list surface: the caller chooses list mode.
func (s *Service) Status(ctx context.Context, task string) Response {
	return s.status(ctx, task, true)
}

func (s *Service) status(ctx context.Context, task string, observeLock bool) Response {
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
	}
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect task runtime metadata")
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, task, "read task receipts: "+err.Error(), "inspect task runtime receipt evidence")
	}
	inventory, err := s.worktrunkList(ctx)
	if err != nil {
		res := response(StatusBlocked, task, err.Error(), "run ce task run-doctor")
		res.Diagnostics = []CommandDiagnostic{diagnostic(inventory.Diagnostic)}
		return res
	}
	locked := false
	if observeLock {
		locked = store.lockBlocks(s.now())
	}
	task = NormalizeIdentity(task)
	if task == "" {
		return s.listResponse(ctx, executions, receipts, inventory, locked)
	}
	for i := range executions {
		if executions[i].Task == task {
			receipt := receipts[task]
			result := stateResponse(s.verifiedExecutionState(ctx, executions[i], receipt, inventory, locked))
			if receipt.Task != "" {
				result.Receipt = &receipt
			}
			return result
		}
	}
	return response(StatusUnknown, task, "task execution was not found", "ce task run-list", "run-list")
}

// List derives every execution. One blocked state makes the whole answer
// BLOCKED. activeCount counts ACTIVE states only.
func (s *Service) List(ctx context.Context) Response {
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, "", err.Error(), "run ce task run-doctor")
	}
	executions, err := store.load()
	if err != nil {
		return response(StatusBlocked, "", err.Error(), "inspect task runtime metadata")
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return response(StatusBlocked, "", "read task receipts: "+err.Error(), "inspect task runtime receipt evidence")
	}
	inventory, err := s.worktrunkList(ctx)
	if err != nil {
		res := response(StatusBlocked, "", err.Error(), "run ce task run-doctor")
		res.Diagnostics = []CommandDiagnostic{diagnostic(inventory.Diagnostic)}
		return res
	}
	return s.listResponse(ctx, executions, receipts, inventory, store.lockBlocks(s.now()))
}

func (s *Service) listResponse(ctx context.Context, executions []Execution, receipts map[string]Receipt, inventory worktrunkInventory, locked bool) Response {
	sort.Slice(executions, func(i, j int) bool { return executions[i].Task < executions[j].Task })
	res := response(StatusReady, "", "task execution states returned", "select a task", "run-list", "run-status")
	res.Executions = executions
	for _, execution := range executions {
		state := s.verifiedExecutionState(ctx, execution, receipts[execution.Task], inventory, locked)
		res.States = append(res.States, state)
		if state.Status == StatusActive {
			res.ActiveCount++
		}
		if state.Status == StatusBlocked {
			res.Status, res.Reason = StatusBlocked, "one or more task executions are blocked"
		}
	}
	return res
}

// HasActiveRun reports whether taskID has a non-terminal execution record.
// An empty id is false. The read does not take the lock and does not derive
// Git state. Lint is the intended caller; a failure to construct the service
// is that caller's signal to skip the check.
func (s *Service) HasActiveRun(ctx context.Context, taskID string) (bool, error) {
	key := NormalizeIdentity(taskID)
	if key == "" {
		return false, nil
	}
	store, err := s.store(ctx)
	if err != nil {
		return false, err
	}
	executions, err := store.load()
	if err != nil {
		return false, err
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return false, err
	}
	for i := range executions {
		if executions[i].Task != key {
			continue
		}
		if receipt, ok := receipts[key]; ok && validTerminalReceipt(executions[i], receipt) {
			return false, nil
		}
		return true, nil
	}
	return false, nil
}
