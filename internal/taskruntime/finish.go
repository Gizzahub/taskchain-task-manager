package taskruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Finish verifies a pushed clean task, asks the integration provider to
// check and then run, and records whether recovery removed the task.
// An empty task selects the single active execution owned by this actor
// and host. The check may be stopped when the source tip moves. The run
// is never stopped.
func (s *Service) Finish(ctx context.Context, task string) (Response, int) {
	store, err := s.store(ctx)
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "run ce task run-doctor"), 1
	}
	unlock, err := store.lock()
	if err != nil {
		return response(StatusBlocked, task, err.Error(), "inspect the mutation lock with run-doctor"), 1
	}
	defer unlock()
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
	for index, call := range []func(context.Context, string, string, bool) (CommandResult, error){provider.Check, provider.Run} {
		callCtx, movedTo, stopWatch := ctx, func() string { return "" }, func() {}
		if index == 0 {
			callCtx, movedTo, stopWatch = s.watchSourceTip(ctx, execution, preSourceHead)
		}
		result, runErr := call(callCtx, execution.Worktree, integrationTarget(execution), noFetch)
		stopWatch()
		diagnostics = append(diagnostics, diagnostic(result))
		if moved := movedTo(); moved != "" {
			note := fmt.Sprintf("the source tip moved from %s to %s during the integration check, which was stopped early", preSourceHead, moved)
			return s.refuseStaleFinish(ctx, store, execution, note, diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
		}
		if runErr != nil {
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
			reason, exitCode := "integration did not complete", result.ExitCode
			next := "resolve gz-git diagnostics and retry"
			if index == 1 && result.ExitCode == 3 {
				reason = partialCleanupReason
			} else if exitCode < 1 || exitCode > 2 {
				exitCode = 2
			}
			stale := index == 0 && s.branchBaseIsStale(ctx, execution)
			failing := ""
			if !stale && reason == "integration did not complete" {
				failing = failingCheckReason(index, result)
			}
			receipt, evidenceErr := s.receipt(ctx, execution, StatusBlocked, reason, diagnostics, preSourceHead, baseHead, preTaskHead, noFetch)
			switch {
			case stale:
				tip := receipt.SourceHead
				if tip == "" {
					tip = preSourceHead
				}
				reason = fmt.Sprintf("task branch is based on %s, but the current source tip is %s; the branch is based on a commit that is no longer the source tip", baseHead, tip)
				receipt.Reason = reason
				next = "rebase onto the source tip in the worktree, then retry run-finish"
			case failing != "":
				reason = failing
				if evidenceErr != nil && !errors.Is(evidenceErr, errRecoveryIncomplete) {
					reason += "; post-operation evidence failed: " + evidenceErr.Error()
				}
				receipt.Reason = reason
			case evidenceErr != nil:
				reason += "; post-operation evidence failed: " + evidenceErr.Error()
			}
			if err := store.appendReceipt(receipt); err != nil {
				reason += "; persist receipt failed: " + err.Error()
			}
			r := response(StatusBlocked, task, reason, next)
			r.Receipt, r.Diagnostics = &receipt, diagnostics
			return r, exitCode
		}
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
