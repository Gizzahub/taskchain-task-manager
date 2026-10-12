package taskruntime

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Finish persists a claim, runs the provider outside the mutation lock,
// and conditionally records its outcome after reacquiring that lock.
// Interrupts cancel outcome-lock acquisition, not the integration already in
// progress. The provider retains the caller's context, matching CE's contract.
func (s *Service) Finish(ctx context.Context, task string) (Response, int) {
	finishCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor"), 1
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect the mutation lock with run-doctor"), 1
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	if NormalizeIdentity(task) == "" {
		selected, refused, code := s.selectBareFinish(ctx)
		if selected == "" {
			return refused, code
		}
		task = selected
	}
	status := s.status(ctx, task, false)
	if status.Execution == nil {
		return status, 1
	}
	if status.Status != StatusActive {
		if status.Status == StatusDone {
			return status, 0
		}
		return status, 1
	}
	if !status.FinishReady {
		return status, 1
	}
	execution := *status.Execution
	task = execution.Task
	owner, err := s.owner(ctx)
	if err != nil || !sameOwner(execution.Owner, owner) {
		res := response(StatusBlocked, task, "only "+execution.Owner.String()+" may finish this task", "run as the recorded owner")
		appendIdentityDiagnostic(&res, err)
		return res, 1
	}
	cwd, _ := os.Getwd()
	if within(cwd, execution.Worktree) {
		return response(StatusBlocked, task, "run-finish refuses to remove its current worktree", "run from the primary/control checkout"), 1
	}
	checks := []struct {
		args   []string
		reason string
	}{
		{[]string{"status", "--porcelain"}, "task worktree is dirty"},
		{[]string{"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"}, "task branch has no upstream"},
		{[]string{"rev-list", "--left-right", "--count", "@{u}...HEAD"}, "task branch must be fully pushed"},
	}
	diagnostics := []CommandDiagnostic{}
	for i, check := range checks {
		result, runErr := s.command(ctx, execution.Worktree, "git", check.args...)
		if runErr != nil {
			return response(StatusBlocked, task, runErr.Error(), "inspect the task worktree"), 1
		}
		diagnostics = append(diagnostics, diagnostic(result))
		bad := result.ExitCode != 0
		if i == 0 {
			bad = bad || strings.TrimSpace(result.Stdout) != ""
		}
		if i == 2 {
			fields := strings.Fields(result.Stdout)
			bad = bad || len(fields) != 2 || fields[0] != "0" || fields[1] != "0"
		}
		if bad {
			r := response(StatusBlocked, task, check.reason, "resolve the Git precondition and retry")
			r.Diagnostics = diagnostics
			return r, 1
		}
	}
	taskHeadResult, taskHeadErr := s.command(ctx, execution.Worktree, "git", "rev-parse", "HEAD")
	sourceHeadResult, sourceHeadErr := s.command(ctx, s.root, "git", "rev-parse", "--verify", sourceTrackingRef(execution.Source))
	if taskHeadErr != nil || sourceHeadErr != nil || taskHeadResult.ExitCode != 0 || sourceHeadResult.ExitCode != 0 {
		return response(StatusBlocked, task, "capture pre-integration Git evidence failed", "inspect task and source branches"), 1
	}
	diagnostics = append(diagnostics, diagnostic(taskHeadResult), diagnostic(sourceHeadResult))
	preTaskHead, preSourceHead := strings.TrimSpace(taskHeadResult.Stdout), strings.TrimSpace(sourceHeadResult.Stdout)
	baseHeadResult, baseHeadErr := s.command(ctx, execution.Worktree, "git", "merge-base", sourceTrackingRef(execution.Source), "HEAD")
	diagnostics = append(diagnostics, diagnostic(baseHeadResult))
	baseHead := strings.TrimSpace(baseHeadResult.Stdout)
	if baseHeadErr != nil || baseHeadResult.ExitCode != 0 {
		return response(StatusBlocked, task, "capture task branch base failed", "inspect task and source branches"), 1
	}
	cfg, configErr := s.config()
	if configErr != nil {
		return response(StatusBlocked, task, configErr.Error(), "configure "+ConfigFile), 1
	}
	provider := s.integrator()
	noFetch := cfg.IntegrationNetworkPolicy == NetworkNoFetch
	if noFetch {
		_, probeDiagnostics, probeErr := provider.DiagnoseNoFetch(ctx, execution.Worktree)
		diagnostics = append(diagnostics, probeDiagnostics...)
		if probeErr != nil {
			reason := "integration provider gz-git has no declared no-fetch finish capability: " + probeErr.Error()
			diagnostics = append(diagnostics, CommandDiagnostic{
				Command: providerGZGit, Args: integrateArgs("check", integrationTarget(execution), true),
				Cwd: execution.Worktree, Error: reason, NotStarted: true,
			})
			receipt := Receipt{
				Task: execution.Task, Operation: "finish", Status: StatusBlocked, Owner: execution.Owner,
				Branch: execution.Branch, Worktree: execution.Worktree, Reason: reason, CreatedAt: s.now().UTC(),
				Diagnostics: diagnostics, SourceHeadBefore: preSourceHead, BaseHead: baseHead, TaskHead: preTaskHead,
				ToolVersion: toolVersion(), ToolRevision: toolRevision(),
			}
			if err := store.appendReceipt(receipt); err != nil {
				reason += "; persist receipt failed: " + err.Error()
			}
			r := response(StatusBlocked, task, reason, "use a provider with a declared no-fetch finish capability or change integration-network-policy")
			r.Receipt, r.Diagnostics = &receipt, receipt.Diagnostics
			return r, 1
		}
	}
	if s.branchBaseIsStale(ctx, execution) {
		return s.refuseStaleFinish(ctx, store, execution, "", diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
	}
	// The claim is durable before releasing the repository-wide mutation lock.
	// Provider admission can wait for an arbitrary time; holding that lock there
	// used to make unrelated starts and status-changing maintenance unavailable.
	claim := Receipt{
		Task: execution.Task, Operation: "finish", Status: StatusFinishing,
		Owner: execution.Owner, Branch: execution.Branch, Worktree: execution.Worktree,
		CreatedAt: s.now().UTC(), SourceHeadBefore: preSourceHead, BaseHead: baseHead, TaskHead: preTaskHead,
		ExecutionStartedAt: &execution.StartedAt, ExecutionType: execution.Type, Source: execution.Source,
		FinishPID: os.Getpid(), FinishHost: lockHost(), ToolVersion: toolVersion(), ToolRevision: toolRevision(),
	}
	if err := store.appendReceipt(claim); err != nil {
		return response(StatusBlocked, task, "persist FINISHING claim failed: "+err.Error(), "repair task runtime storage and retry"), 1
	}
	unlock()
	unlock = nil
	// All post-provider mutations must prove that this exact generation and
	// claim are still current.  A competing terminal action therefore cannot be
	// overwritten by a late provider result.
	relock := func(providerExit int, providerFailed bool) (Response, int, bool) {
		var lockErr error
		wait := 10 * time.Minute
		if s.finishRelockWait > 0 {
			wait = s.finishRelockWait
		}
		unlock, lockErr = store.lockWait(finishCtx, wait, 20*time.Millisecond)
		if lockErr != nil {
			code := 1
			if !providerFailed && (providerExit == 0 || providerExit == 3) {
				code = 3
			}
			r := response(StatusBlocked, task, "integration provider completed but the mutation lock could not be reacquired: "+lockErr.Error(), "run-status", "run-status")
			r.Execution, r.Diagnostics = &execution, diagnostics
			return r, code, false
		}
		current, loadErr := store.load()
		receipts, receiptErr := store.latestReceipts()
		if loadErr != nil || receiptErr != nil || !containsExecutionGeneration(current, execution) || !sameFinishClaim(receipts[task], claim) {
			code := 1
			if !providerFailed && (providerExit == 0 || providerExit == 3) {
				code = 3
			}
			reason := "integration provider completed but the FINISHING claim or execution generation changed"
			if loadErr != nil {
				reason = "integration provider completed but read executions failed: " + loadErr.Error()
			}
			if receiptErr != nil {
				reason = "integration provider completed but read receipts failed: " + receiptErr.Error()
			}
			r := response(StatusBlocked, task, reason, "run-status", "run-status")
			r.Execution, r.Diagnostics = &execution, diagnostics
			return r, code, false
		}
		return Response{}, 0, true
	}
	// gz-git owns readiness, admission, integration, and cleanup in one run.
	// A separate check would duplicate its gate and hold no useful protection.
	result, runErr := provider.Run(ctx, execution.Worktree, integrationTarget(execution), noFetch)
	if runErr != nil && result.Error == "" {
		result.Error = runErr.Error()
	}
	diagnostics = append(diagnostics, diagnostic(result))
	if runErr != nil {
		if blocked, code, ok := relock(result.ExitCode, true); !ok {
			return blocked, code
		}
		reason := "integration provider invocation failed: " + runErr.Error()
		receipt, evidenceErr := s.receipt(ctx, execution, StatusBlocked, reason, diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
		if evidenceErr != nil {
			reason += "; post-operation evidence failed: " + evidenceErr.Error()
		}
		if err := store.appendReceipt(receipt); err != nil {
			reason += "; persist receipt failed: " + err.Error()
		}
		r := response(StatusBlocked, task, reason, "inspect integration provider diagnostics and retry")
		r.Receipt, r.Diagnostics = &receipt, receipt.Diagnostics
		return r, 1
	}
	if result.ExitCode != 0 {
		if blocked, code, ok := relock(result.ExitCode, false); !ok {
			return blocked, code
		}
		reason, exitCode := "integration did not complete", result.ExitCode
		next := "resolve gz-git diagnostics and retry"
		if result.ExitCode == 3 {
			reason = partialCleanupReason
		} else if exitCode < 1 || exitCode > 2 {
			exitCode = 2
		}
		failing := ""
		if reason == "integration did not complete" {
			failing = failingCheckReason(1, result)
		}
		receipt, evidenceErr := s.receipt(ctx, execution, StatusBlocked, reason, diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
		if failing != "" {
			reason, receipt.Reason = failing, failing
		} else if evidenceErr != nil {
			reason += "; post-operation evidence failed: " + evidenceErr.Error()
		}
		if err := store.appendReceipt(receipt); err != nil {
			reason += "; persist receipt failed: " + err.Error()
		}
		r := response(StatusBlocked, task, reason, next)
		r.Receipt, r.Diagnostics = &receipt, diagnostics
		return r, exitCode
	}
	if blocked, code, ok := relock(0, false); !ok {
		return blocked, code
	}
	receipt, evidenceErr := s.receipt(ctx, execution, StatusDone, "integration and recovery completed", diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
	if evidenceErr != nil {
		r := response(StatusBlocked, task, "integration completed but evidence verification failed: "+evidenceErr.Error(), "inspect source push and recovery state")
		r.Receipt, r.Diagnostics = &receipt, receipt.Diagnostics
		return r, 3
	}
	if err := validateIntegratedFinish(task, &execution, &receipt); err != nil {
		r := response(StatusBlocked, task, "integration completed but finish receipt validation failed: "+err.Error(), "inspect task runtime receipt evidence")
		r.Receipt, r.Diagnostics = &receipt, receipt.Diagnostics
		return r, 3
	}
	if err := store.appendReceipt(receipt); err != nil {
		r := response(StatusBlocked, task, "integration completed but receipt persistence failed: "+err.Error(), "preserve diagnostics and repair runtime storage")
		r.Receipt, r.Diagnostics = &receipt, receipt.Diagnostics
		return r, 3
	}
	r := response(StatusDone, task, receipt.Reason, "no action required")
	r.Execution, r.Receipt, r.Diagnostics = &execution, &receipt, diagnostics
	return r, 0
}

// refuseStaleFinish records a BLOCKED finish whose base is no longer the
// source tip. note, when set, says how the staleness was discovered.
// The provider exit code is not used here: this refusal is always 1.
func (s *Service) refuseStaleFinish(ctx context.Context, store stateStore, execution Execution, note string, diagnostics []CommandDiagnostic, preSourceHead, baseHead, preTaskHead string, noFetch bool) (Response, int) {
	receipt, _ := s.receipt(ctx, execution, StatusBlocked, "", diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
	tip := receipt.SourceHead
	if tip == "" {
		tip = preSourceHead
	}
	reason := fmt.Sprintf("task branch is based on %s, but the current source tip is %s; the branch is based on a commit that is no longer the source tip", baseHead, tip)
	if note != "" {
		reason += "; " + note
	}
	receipt.Reason = reason
	if err := store.appendReceipt(receipt); err != nil {
		reason += "; persist receipt failed: " + err.Error()
	}
	r := response(StatusBlocked, execution.Task, reason, "rebase onto the source tip in the worktree, then retry run-finish")
	r.Receipt, r.Diagnostics = &receipt, diagnostics
	return r, 1
}
