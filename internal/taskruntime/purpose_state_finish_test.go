package taskruntime

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func purposeWorld(t *testing.T, purpose Purpose) *endWorld {
	t.Helper()
	w := newEndWorld(t)
	old := w.worktree
	w.execution.Purpose = purpose
	w.branch = branchForPurpose(purpose, w.execution.Owner, string(w.execution.Type), w.execution.Task)
	w.worktree = expectedWorktreePath(filepath.Dir(old), w.branch)
	if old != w.worktree {
		if err := os.Rename(old, w.worktree); err != nil {
			t.Fatal(err)
		}
	}
	w.execution.Branch, w.execution.Worktree = w.branch, w.worktree
	w.oids["refs/heads/"+w.branch] = oidTask
	w.oids["refs/remotes/origin/"+w.branch] = ""
	if err := newStateStore(w.common).save([]Execution{w.execution}); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestPurposeRootNonDeliveryStateAndOutput(t *testing.T) {
	for _, purpose := range []Purpose{PurposeExperiment, PurposeReview} {
		t.Run(string(purpose), func(t *testing.T) {
			w := purposeWorld(t, purpose)
			w.noUpstream = true
			svc := w.service()
			state := svc.verifiedExecutionState(context.Background(), w.execution, Receipt{}, worktrunkInventory{Paths: map[string]string{w.branch: w.worktree}}, false)
			if state.Status != StatusActive || state.FinishReady || containsArg(state.AllowedActions, "run-finish") || !containsArg(state.AllowedActions, "run-discard") {
				t.Fatalf("state=%+v", state)
			}
			if len(state.Diagnostics) != 2 {
				t.Fatalf("non-delivery must stop after identity/clean probes: %+v", state.Diagnostics)
			}
			var human, list bytes.Buffer
			if err := WriteResponse(&human, stateResponse(state), false, false); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(human.String(), "purpose: "+string(purpose)) {
				t.Fatalf("human=%s", human.String())
			}
			if err := writeExecutionStates(&list, []DerivedState{state}); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(list.String(), "purpose="+string(purpose)) {
				t.Fatalf("list=%s", list.String())
			}
		})
	}
}

func TestPurposeRootFinishAndRecoveryRefuseBeforeIntegration(t *testing.T) {
	for _, purpose := range []Purpose{PurposeExperiment, PurposeReview} {
		t.Run(string(purpose), func(t *testing.T) {
			w := purposeWorld(t, purpose)
			before := w.receipts()
			result, code := w.service().Finish(context.Background(), w.execution.Task)
			if code != 1 || result.Status != StatusBlocked || !strings.Contains(result.Reason, "only available for delivery") || result.Receipt != nil {
				t.Fatalf("finish=%+v code=%d", result, code)
			}
			recovered, code := w.service().Recover(context.Background(), w.execution.Task)
			if code != 1 || recovered.Status != StatusBlocked || !strings.Contains(recovered.Reason, "non-delivery") {
				t.Fatalf("recover=%+v code=%d", recovered, code)
			}
			if w.provider.checks != 0 || w.provider.runs != 0 || w.receipts() != before {
				t.Fatalf("integration or receipt mutation: checks=%d runs=%d receipt=%s", w.provider.checks, w.provider.runs, w.receipts())
			}
		})
	}
}

func TestPurposeRootBareFinishSelectsDeliveryOnly(t *testing.T) {
	w := newEndWorld(t)
	experiment := w.execution
	experiment.Task, experiment.Purpose = "experiment-task", PurposeExperiment
	experiment.Branch = branchForPurpose(experiment.Purpose, experiment.Owner, string(experiment.Type), experiment.Task)
	experiment.Worktree = expectedWorktreePath(filepath.Dir(w.worktree), experiment.Branch)
	if err := os.Mkdir(experiment.Worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	w.extraBranch, w.extraWorktree = experiment.Branch, experiment.Worktree
	if err := newStateStore(w.common).save([]Execution{w.execution, experiment}); err != nil {
		t.Fatal(err)
	}
	selected, result, code := w.service().selectBareFinish(context.Background())
	if code != 0 || selected != w.execution.Task {
		t.Fatalf("selected=%s result=%+v code=%d", selected, result, code)
	}
	if err := newStateStore(w.common).save([]Execution{experiment}); err != nil {
		t.Fatal(err)
	}
	selected, result, code = w.service().selectBareFinish(context.Background())
	if code != 1 || selected != "" || !strings.Contains(result.Reason, "no active execution") {
		t.Fatalf("selected=%s result=%+v code=%d", selected, result, code)
	}
}

func TestPurposeRootReasonedAbortPreservesResources(t *testing.T) {
	for _, purpose := range []Purpose{PurposeExperiment, PurposeReview} {
		t.Run(string(purpose), func(t *testing.T) {
			w := purposeWorld(t, purpose)
			before := w.receipts()
			refused, code := w.service().Abort(context.Background(), w.execution.Task, " ")
			if code != 1 || !strings.Contains(refused.Reason, "non-empty reason") || w.receipts() != before {
				t.Fatalf("refused=%+v code=%d", refused, code)
			}
			result, code := w.service().Abort(context.Background(), w.execution.Task, "observed bounded outcome")
			if code != 0 || result.Status != StatusAborted || result.Receipt == nil || result.Receipt.Purpose != purpose || result.Receipt.WorktreeRemoved {
				t.Fatalf("abort=%+v code=%d", result, code)
			}
			if _, err := os.Stat(w.worktree); err != nil {
				t.Fatal(err)
			}
			recorded := w.receipts()
			result, code = w.service().Abort(context.Background(), w.execution.Task, "")
			if code != 0 || result.Status != StatusAborted || w.receipts() != recorded {
				t.Fatalf("terminal noop=%+v code=%d", result, code)
			}
		})
	}
}

func TestPurposeRootDiscardKeepsNormalGuards(t *testing.T) {
	for _, purpose := range []Purpose{PurposeExperiment, PurposeReview} {
		t.Run(string(purpose), func(t *testing.T) {
			for _, guard := range []string{"dirty", "remote", "clean"} {
				t.Run(guard, func(t *testing.T) {
					w := purposeWorld(t, purpose)
					if guard == "dirty" {
						w.derivePorcelain = " M sample.go"
					}
					if guard == "remote" {
						w.lsRemote = 0
					}
					result, code := w.service().Discard(context.Background(), w.execution.Task, "finished bounded probe", "")
					if guard == "clean" {
						if code != 0 || result.Status != StatusAborted || result.Receipt == nil || result.Receipt.Purpose != purpose || !result.Receipt.WorktreeRemoved || !result.Receipt.LocalBranchRemoved || !result.Receipt.RemoteBranchRemoved {
							t.Fatalf("discard=%+v code=%d", result, code)
						}
					} else {
						if code != 1 {
							t.Fatalf("guard %s accepted %+v", guard, result)
						}
						if _, err := os.Stat(w.worktree); err != nil {
							t.Fatalf("guard removed resource: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestPurposeRootReceiptMismatchBlocksEveryStatus(t *testing.T) {
	for _, status := range []string{StatusActive, StatusBlocked, StatusFinishing, StatusAborted, StatusDone} {
		t.Run(status, func(t *testing.T) {
			w := purposeWorld(t, PurposeExperiment)
			receipt := Receipt{Task: w.execution.Task, Purpose: PurposeReview, Branch: branchForPurpose(PurposeReview, w.execution.Owner, string(w.execution.Type), w.execution.Task), Worktree: w.worktree, Owner: w.execution.Owner, Status: status, CreatedAt: w.execution.StartedAt}
			state := w.service().verifiedExecutionState(context.Background(), w.execution, receipt, worktrunkInventory{Paths: map[string]string{w.branch: w.worktree}}, false)
			if state.Status != StatusBlocked || !strings.Contains(state.Reason, "receipt purpose") || state.FinishReady {
				t.Fatalf("mismatch state=%+v", state)
			}
		})
	}
}

func TestPurposeRootInvalidPrefixCannotMutate(t *testing.T) {
	for _, verb := range []string{"abort", "discard", "recover", "settle", "close"} {
		t.Run(verb, func(t *testing.T) {
			w := newEndWorld(t)
			w.execution.Purpose = PurposeExperiment // dev branch deliberately does not match.
			if err := newStateStore(w.common).save([]Execution{w.execution}); err != nil {
				t.Fatal(err)
			}
			if verb == "settle" || verb == "close" {
				if err := os.Remove(w.worktree); err != nil {
					t.Fatal(err)
				}
			}
			if verb == "close" {
				cfg := "schema-version: 3\nrepository-id: sample-repo\nworktree-roots:\n  testhost: " + filepath.Dir(w.worktree) + "\nintegration-provider: gz-git\nintegration-network-policy: allow\nexecution-administrators:\n  - {actor: tester, host: testhost, kind: agent}\n"
				if err := os.WriteFile(filepath.Join(w.root, ConfigFile), []byte(cfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before := w.receipts()
			var result Response
			var code int
			switch verb {
			case "abort":
				result, code = w.service().Abort(context.Background(), w.execution.Task, "retain")
			case "discard":
				result, code = w.service().Discard(context.Background(), w.execution.Task, "cleanup", "")
			case "recover":
				result, code = w.service().Recover(context.Background(), w.execution.Task)
			case "settle":
				result, code = w.service().Settle(context.Background(), w.execution.Task)
			case "close":
				result, code = w.service().Close(context.Background(), w.execution.Task, "administrative")
			}
			if code != 1 || result.Status != StatusBlocked || !strings.Contains(result.Reason, "purpose or branch is invalid") || w.receipts() != before {
				t.Fatalf("invalid prefix %s=%+v code=%d", verb, result, code)
			}
		})
	}
}

func TestPurposeRootNonDeliveryDonePolicies(t *testing.T) {
	for _, purpose := range []Purpose{PurposeDelivery, PurposeExperiment, PurposeReview} {
		t.Run(string(purpose), func(t *testing.T) {
			w := purposeWorld(t, purpose)
			receipt := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusDone, Purpose: purpose, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: w.execution.StartedAt, SourceHeadBefore: oidBase, BaseHead: oidBase, TaskHead: oidTask, SourceHead: oidSource, SourcePushed: true, WorktreeRemoved: true, LocalBranchRemoved: true, RemoteBranchRemoved: true}
			err := validateIntegratedFinish(w.execution.Task, &w.execution, &receipt)
			if (err == nil) != (purpose == PurposeDelivery) {
				t.Fatalf("integrated policy purpose=%s err=%v", purpose, err)
			}
			receipt.Operation = "recover"
			receipt.WorktreeRemoved = false
			err = validateDoneReceipt(w.execution.Task, &w.execution, &receipt)
			if (err == nil) != (purpose == PurposeDelivery) {
				t.Fatalf("recovery done policy purpose=%s err=%v", purpose, err)
			}
		})
	}
}

func TestPurposeRootDeadClaimRecoveryDoesNotCompleteNonDelivery(t *testing.T) {
	w := purposeWorld(t, PurposeReview)
	started := w.execution.StartedAt
	claim := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusFinishing, Purpose: PurposeReview, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: started, ExecutionStartedAt: &started, ExecutionType: w.execution.Type, Source: w.execution.Source, FinishPID: 1 << 29, FinishHost: lockHost()}
	if err := newStateStore(w.common).appendReceipt(claim); err != nil {
		t.Fatal(err)
	}
	result, code := w.service().Recover(context.Background(), w.execution.Task)
	if code != 0 || result.Receipt == nil || result.Receipt.Status != StatusBlocked || result.Receipt.Purpose != PurposeReview || result.Status == StatusDone || w.provider.runs != 0 {
		t.Fatalf("recovery=%+v code=%d", result, code)
	}
}
