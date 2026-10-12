package taskruntime

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

type claimLiveness int

const (
	claimUnknown claimLiveness = iota
	claimAlive
	claimDead
)

func processStarted(ctx context.Context, pid int) (time.Time, error) {
	cmd := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "lstart=")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.Output()
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("Mon Jan _2 15:04:05 2006", strings.TrimSpace(string(out)), time.Local)
}

// Only positive local evidence of death releases a claim. A foreign host,
// missing metadata, or failed process probe remains protected.
func (s *Service) claimLiveness(ctx context.Context, claim Receipt) claimLiveness {
	if claim.FinishPID <= 0 || claim.FinishHost == "" || claim.FinishHost != lockHost() {
		return claimUnknown
	}
	if !pidRunning(claim.FinishPID) {
		return claimDead
	}
	probe := s.claimProcessStart
	if probe == nil {
		probe = processStarted
	}
	started, err := probe(ctx, claim.FinishPID)
	if err != nil || started.IsZero() || claim.CreatedAt.IsZero() {
		return claimUnknown
	}
	if started.After(claim.CreatedAt.Add(5 * time.Second)) {
		return claimDead
	}
	return claimAlive
}

func (s *Service) finishClaimWarnings(ctx context.Context, receipts map[string]Receipt) []string {
	names := make([]string, 0)
	for task, claim := range receipts {
		if claim.Status == StatusFinishing {
			names = append(names, task)
		}
	}
	sort.Strings(names)
	warnings := make([]string, 0, len(names))
	for _, task := range names {
		claim := receipts[task]
		action := "wait for run-finish; if the claimant is gone, the recorded owner may declare --claim-dead"
		if s.claimLiveness(ctx, claim) == claimDead {
			action = "run-recover " + task + " without rerunning the provider"
		}
		warnings = append(warnings, fmt.Sprintf("task %s has FINISHING pid=%d host=%s; %s", task, claim.FinishPID, claim.FinishHost, action))
	}
	return warnings
}

func (s *Service) recoverFinishClaim(ctx context.Context, store stateStore, execution Execution, claim Receipt, cfg Config, declared bool, owner Owner) (Response, int) {
	task := execution.Task
	if !receiptBindsExecution(execution, claim) {
		return response(StatusBlocked, task, "FINISHING claim does not bind this execution", "inspect task runtime receipt evidence", "run-status"), 1
	}
	live := s.claimLiveness(ctx, claim)
	if live == claimAlive || (!declared && live != claimDead) {
		return response(StatusBlocked, task, "run-recover refuses a live or unproven FINISHING claimant", "wait for run-finish or confirm a dead foreign claimant with --claim-dead", "run-status"), 1
	}
	reason := "run-finish ended without recording an integration outcome"
	if declared {
		reason += "; " + owner.String() + " declared the claim dead with --claim-dead"
	}
	// Git observations are evidence, not an invented successful provider result.
	receipt, evidenceErr := s.receipt(ctx, execution, StatusBlocked, reason, nil, claim.SourceHeadBefore, claim.BaseHead, claim.TaskHead, cfg.IntegrationNetworkPolicy == NetworkNoFetch)
	receipt.Operation = "recover"
	if declared {
		receipt.PerformedBy = &owner
	}
	if evidenceErr != nil {
		receipt.Reason += "; observed evidence: " + evidenceErr.Error()
	}
	if err := store.appendReceipt(receipt); err != nil {
		return response(StatusBlocked, task, "persist recovery receipt failed: "+err.Error(), "repair task runtime storage"), 1
	}
	status, next := StatusActive, "inspect Git evidence before retrying run-finish"
	if receipt.WorktreeRemoved {
		status, next = StatusBlocked, "inspect the source and use run-settle or run-abort"
	}
	r := response(status, task, receipt.Reason, next, "run-status")
	r.Execution, r.Receipt, r.Diagnostics = &execution, &receipt, receipt.Diagnostics
	return r, 0
}
