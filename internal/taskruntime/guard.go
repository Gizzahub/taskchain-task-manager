package taskruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// CleanupBlocker reports runtime ownership without taking the mutation lock.
// A live lock is "task runtime mutation is active". An execution is skipped
// only when a valid terminal receipt binds it.
func (s *Service) CleanupBlocker(ctx context.Context, aliases []string) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	if lock, err := store.lockInfo(s.now()); err == nil {
		if !lock.Reclaimable {
			return fmt.Errorf("task runtime mutation is active")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return store.cleanupBlocker(aliases)
}

// ProtectCleanup holds the mutation lock for one cleanup group. It does not
// rewrite execution history. The lock-active observation is not repeated
// while this process holds the lock.
func (s *Service) ProtectCleanup(ctx context.Context, aliases []string, apply func() error) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.cleanupBlocker(aliases); err != nil {
		return err
	}
	return apply()
}

// ProtectLock holds the mutation lock around fn. Maintenance and reconcile
// apply use it. The unlock removes only the lock this call created.
func (s *Service) ProtectLock(ctx context.Context, fn func() error) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

// LockPath is the mutation lock file, used to recheck that the lock is still
// held immediately before a board exchange.
func (s *Service) LockPath(ctx context.Context) (string, error) {
	store, err := s.store(ctx)
	if err != nil {
		return "", err
	}
	return store.lockPath(), nil
}

// RejectUnresolved refuses while any execution lacks a valid terminal receipt.
func (s *Service) RejectUnresolved(ctx context.Context) error {
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	return store.rejectUnresolved()
}

// ReadLedger returns the execution rows and the latest receipt for each task.
func (s *Service) ReadLedger(ctx context.Context) ([]Execution, map[string]Receipt, error) {
	store, err := s.store(ctx)
	if err != nil {
		return nil, nil, err
	}
	executions, err := store.load()
	if err != nil {
		return nil, nil, err
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return nil, nil, err
	}
	return executions, receipts, nil
}

// BoardRecoveryDir is <git-common-dir>/ce/task-runtime/v1/board-recovery.
func (s *Service) BoardRecoveryDir(ctx context.Context) (string, error) {
	store, err := s.store(ctx)
	if err != nil {
		return "", err
	}
	return filepath.Join(store.dir, "board-recovery"), nil
}

// AbortReceiptBinds reports a valid terminal abort bound to execution.
func AbortReceiptBinds(execution Execution, receipt Receipt) bool {
	return validTerminalAbortReceipt(execution, receipt)
}

// DoneReceiptBinds reports a valid terminal done receipt bound to execution.
func DoneReceiptBinds(execution Execution, receipt Receipt) bool {
	return validateDoneReceipt(execution.Task, &execution, &receipt) == nil
}

// ReceiptBindsExecution reports the receipt's identity matches the execution
// and the receipt does not predate it.
func ReceiptBindsExecution(execution Execution, receipt Receipt) bool {
	return receiptBindsExecution(execution, receipt)
}

func (store stateStore) cleanupBlocker(aliases []string) error {
	executions, err := store.load()
	if err != nil {
		return err
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return err
	}
	keys := map[string]bool{}
	for _, key := range aliases {
		keys[NormalizeIdentity(key)] = true
	}
	for _, execution := range executions {
		if !keys[NormalizeIdentity(execution.Task)] {
			continue
		}
		receipt, exists := receipts[execution.Task]
		if exists && validTerminalReceipt(execution, receipt) {
			continue
		}
		return fmt.Errorf("active or unresolved runtime execution: %s", execution.Task)
	}
	return nil
}

func (store stateStore) rejectUnresolved() error {
	executions, err := store.load()
	if err != nil {
		return err
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return err
	}
	for _, execution := range executions {
		receipt, exists := receipts[execution.Task]
		if exists && validTerminalReceipt(execution, receipt) {
			continue
		}
		return fmt.Errorf("active or unresolved runtime execution: %s", execution.Task)
	}
	return nil
}
