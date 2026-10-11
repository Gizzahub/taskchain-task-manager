package taskruntime

import (
	"fmt"
	"strings"
)

// receiptBindsExecution names the execution being closed. A zero CreatedAt
// still binds; a dated receipt cannot predate the execution it closes.
func receiptBindsExecution(execution Execution, receipt Receipt) bool {
	return receipt.Task == execution.Task && receipt.Owner == execution.Owner &&
		receipt.Branch == execution.Branch && receipt.Worktree == execution.Worktree &&
		!receipt.CreatedAt.Before(execution.StartedAt)
}

func terminalAbortReceipt(receipt Receipt) bool {
	if receipt.Status != StatusAborted {
		return false
	}
	if receipt.Operation == "abort" {
		return true
	}
	return receipt.Operation == "discard" && receipt.PerformedBy != nil && receipt.PerformedBy.Valid() &&
		receipt.PerformedBy.Host == receipt.Owner.Host && receipt.WorktreeRemoved &&
		receipt.LocalBranchRemoved && receipt.RemoteBranchRemoved
}

func validTerminalAbortReceipt(execution Execution, receipt Receipt) bool {
	return receiptBindsExecution(execution, receipt) && (terminalAbortReceipt(receipt) || validSettlementReceipt(execution, receipt) || validCloseReceipt(execution, receipt))
}

func validTerminalReceipt(execution Execution, receipt Receipt) bool {
	if !receiptBindsExecution(execution, receipt) {
		return false
	}
	return terminalAbortReceipt(receipt) || validSettlementReceipt(execution, receipt) || validCloseReceipt(execution, receipt) ||
		(receipt.Status == StatusDone && validateDoneReceipt(execution.Task, &execution, &receipt) == nil)
}

func validSettlementReceipt(execution Execution, receipt Receipt) bool {
	return receiptBindsExecution(execution, receipt) && receipt.Operation == "settle" && receipt.Status == StatusAborted &&
		validNewTerminalGeneration(execution, receipt) && validOID(receipt.TaskHead) && validOID(receipt.SourceHead) &&
		receipt.SourceHeadBefore == "" && receipt.BaseHead == "" && !receipt.SourcePushed && !receipt.WorktreeRemoved &&
		!receipt.LocalBranchRemoved && !receipt.RemoteBranchRemoved
}

func validCloseReceipt(execution Execution, receipt Receipt) bool {
	return receiptBindsExecution(execution, receipt) && receipt.Operation == "close" && receipt.Status == StatusAborted &&
		validNewTerminalGeneration(execution, receipt) && receipt.SourceHeadBefore == "" && receipt.BaseHead == "" &&
		!receipt.SourcePushed && !receipt.WorktreeRemoved && !receipt.LocalBranchRemoved && !receipt.RemoteBranchRemoved &&
		receipt.TaskHead == "" && receipt.SourceHead == ""
}

func validNewTerminalGeneration(execution Execution, receipt Receipt) bool {
	return receipt.ExecutionStartedAt != nil && receipt.ExecutionStartedAt.Equal(execution.StartedAt) &&
		receipt.ExecutionType == execution.Type && receipt.Source == execution.Source &&
		receipt.PerformedBy != nil && receipt.PerformedBy.Valid() &&
		NormalizeIdentity(receipt.PerformedBy.Actor) == receipt.PerformedBy.Actor &&
		NormalizeIdentity(receipt.PerformedBy.Host) == receipt.PerformedBy.Host &&
		(receipt.PerformedBy.Kind == "human" || receipt.PerformedBy.Kind == "agent") &&
		strings.TrimSpace(receipt.Reason) != "" && strings.TrimSpace(receipt.ToolVersion) != "" &&
		strings.TrimSpace(receipt.ToolRevision) != ""
}

func validOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range strings.ToLower(value) {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validateIntegratedFinish(task string, execution *Execution, receipt *Receipt) error {
	if NormalizeIdentity(task) == "" {
		return fmt.Errorf("requested task is empty")
	}
	if execution == nil {
		return fmt.Errorf("execution is missing")
	}
	if execution.Task == "" {
		return fmt.Errorf("execution task is empty")
	}
	if execution.Task != task {
		return fmt.Errorf("execution task %q does not match requested task %q", execution.Task, task)
	}
	if receipt == nil {
		return fmt.Errorf("receipt is missing")
	}
	if receipt.Task == "" {
		return fmt.Errorf("receipt task is empty")
	}
	if receipt.Task != execution.Task {
		return fmt.Errorf("receipt task %q does not match execution task %q", receipt.Task, execution.Task)
	}
	if receipt.Owner != execution.Owner || receipt.Branch != execution.Branch || receipt.Worktree != execution.Worktree {
		return fmt.Errorf("receipt identity does not match execution identity")
	}
	if receipt.Operation != "finish" || receipt.Status != StatusDone {
		return fmt.Errorf("receipt is not a successful terminal finish")
	}
	if strings.TrimSpace(receipt.SourceHeadBefore) == "" || strings.TrimSpace(receipt.TaskHead) == "" || strings.TrimSpace(receipt.SourceHead) == "" {
		return fmt.Errorf("receipt Git head evidence is incomplete")
	}
	if !receipt.SourcePushed || !receipt.WorktreeRemoved || !receipt.LocalBranchRemoved || !receipt.RemoteBranchRemoved {
		return fmt.Errorf("receipt integration or recovery evidence is incomplete")
	}
	return nil
}

// validateDoneReceipt accepts a normal finish receipt and the narrow recovery
// receipt that proves integration while keeping a cleanup residual.
func validateDoneReceipt(task string, execution *Execution, receipt *Receipt) error {
	if err := validateIntegratedFinish(task, execution, receipt); err == nil {
		return nil
	}
	if execution == nil || receipt == nil || NormalizeIdentity(task) == "" || execution.Task != task || receipt.Task != execution.Task ||
		receipt.Owner != execution.Owner || receipt.Branch != execution.Branch || receipt.Worktree != execution.Worktree ||
		receipt.Operation != "recover" || receipt.Status != StatusDone ||
		strings.TrimSpace(receipt.SourceHeadBefore) == "" || strings.TrimSpace(receipt.TaskHead) == "" || strings.TrimSpace(receipt.SourceHead) == "" ||
		!receipt.SourcePushed || (receipt.WorktreeRemoved && receipt.LocalBranchRemoved && receipt.RemoteBranchRemoved) {
		return fmt.Errorf("receipt is not a verified terminal completion")
	}
	return nil
}

func isPartialCleanupReceipt(execution Execution, receipt Receipt) bool {
	if receipt.Task != execution.Task || receipt.Owner != execution.Owner || receipt.Branch != execution.Branch || receipt.Worktree != execution.Worktree ||
		receipt.Operation != "finish" || receipt.Status != StatusBlocked || receipt.Reason != partialCleanupReason ||
		strings.TrimSpace(receipt.SourceHeadBefore) == "" || strings.TrimSpace(receipt.TaskHead) == "" || !receipt.SourcePushed {
		return false
	}
	for _, item := range receipt.Diagnostics {
		if item.Command == providerGZGit && isIntegrateRun(item.Args, integrationTarget(execution)) && item.ExitCode == 3 {
			return true
		}
	}
	return false
}

func isIntegrateRun(args []string, target string) bool {
	if len(args) < 2 || args[0] != "integrate" || args[1] != "run" {
		return false
	}
	rest := args[2:]
	if len(rest) >= 2 && rest[0] == integrationTargetFlag && rest[1] == target {
		rest = rest[2:]
	}
	return len(rest) == 0 || (len(rest) == 1 && rest[0] == integrationNoFetchFlag)
}

func containsExecutionGeneration(executions []Execution, captured Execution) bool {
	for _, current := range executions {
		if current.Task == captured.Task {
			return current.Type == captured.Type && current.Source == captured.Source && current.Owner == captured.Owner &&
				current.Branch == captured.Branch && current.Worktree == captured.Worktree && current.StartedAt.Equal(captured.StartedAt)
		}
	}
	return false
}
