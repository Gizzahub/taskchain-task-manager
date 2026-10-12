package taskruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPurposeJSONAcceptsLegacyAndRejectsExplicitInvalidValues(t *testing.T) {
	for _, target := range []struct {
		name string
		body string
		into any
	}{
		{"execution legacy", `{"task":"one","branch":"dev/a/h/feat/one"}`, &Execution{}},
		{"receipt legacy", `{"task":"one","branch":"dev/a/h/feat/one"}`, &Receipt{}},
	} {
		t.Run(target.name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(target.body), target.into); err != nil {
				t.Fatal(err)
			}
			switch value := target.into.(type) {
			case *Execution:
				if value.Purpose != PurposeDelivery {
					t.Fatalf("purpose = %q", value.Purpose)
				}
			case *Receipt:
				if value.Purpose != PurposeDelivery {
					t.Fatalf("purpose = %q", value.Purpose)
				}
			}
		})
	}

	for _, body := range []string{
		`{"purpose":""}`, `{"purpose":null}`, `{"purpose":12}`, `{"purpose":"scratch"}`,
	} {
		for _, target := range []struct {
			name string
			into any
		}{{"execution", &Execution{}}, {"receipt", &Receipt{}}} {
			t.Run(target.name+body, func(t *testing.T) {
				if err := json.Unmarshal([]byte(body), target.into); err == nil {
					t.Fatalf("invalid purpose decoded: %s", body)
				}
			})
		}
	}
}

func TestValidatePurposeBranch(t *testing.T) {
	for _, test := range []struct {
		purpose Purpose
		branch  string
		valid   bool
	}{
		{PurposeDelivery, "dev/a/h/feat/task", true},
		{PurposeExperiment, "exp/a/h/feat/task", true},
		{PurposeReview, "review/a/h/feat/task", true},
		{"", "dev/a/h/feat/task", true},
		{PurposeDelivery, "exp/a/h/feat/task", false},
		{PurposeExperiment, "review/a/h/feat/task", false},
		{PurposeReview, "dev/a/h/feat/task", false},
		{Purpose("scratch"), "scratch/a/h/feat/task", false},
	} {
		err := ValidatePurposeBranch(test.purpose, test.branch)
		if (err == nil) != test.valid {
			t.Fatalf("ValidatePurposeBranch(%q, %q) error = %v", test.purpose, test.branch, err)
		}
	}
}

func TestReceiptBindingAndCompletionRequireMatchingDeliveryPurpose(t *testing.T) {
	execution := Execution{Task: "task", Type: TypeFeat, Purpose: PurposeDelivery, Source: "master", Owner: Owner{Actor: "a", Host: "h"}, Branch: "dev/a/h/feat/task", Worktree: "/work/task"}
	receipt := Receipt{Task: execution.Task, Purpose: PurposeExperiment, Owner: execution.Owner, Branch: execution.Branch, Worktree: execution.Worktree}
	if receiptBindsExecution(execution, receipt) {
		t.Fatal("receipt with a different purpose bound execution")
	}

	execution.Purpose = PurposeExperiment
	receipt.Purpose = PurposeExperiment
	execution.Branch, receipt.Branch = "exp/a/h/feat/task", "exp/a/h/feat/task"
	receipt.Operation, receipt.Status = "finish", StatusDone
	receipt.SourceHeadBefore, receipt.TaskHead, receipt.SourceHead = oidBase, oidTask, oidSource
	receipt.SourcePushed, receipt.WorktreeRemoved, receipt.LocalBranchRemoved, receipt.RemoteBranchRemoved = true, true, true, true
	if err := validateDoneReceipt(execution.Task, &execution, &receipt); err == nil || !strings.Contains(err.Error(), "delivery") {
		t.Fatalf("experiment DONE error = %v", err)
	}
}

func TestStartRefusesPersistedPurposeBranchMismatchBeforeResume(t *testing.T) {
	w := newEndWorld(t)
	w.execution.Purpose = PurposeExperiment
	if err := newStateStore(w.common).save([]Execution{w.execution}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(newStateStore(w.common).executionsPath())
	if err != nil {
		t.Fatal(err)
	}
	beforeReceipts := w.receipts()

	service := w.service()
	service.runner = scriptRunner{fn: func(ctx context.Context, request CommandRequest) (CommandResult, error) {
		if request.Command == providerGZGit {
			result := CommandResult{Command: request.Command, Args: request.Args, WorkDir: request.WorkDir, Executable: "/usr/bin/gz-git"}
			switch {
			case sameArgs(request.Args, "--version"):
				result.Stdout = "gz-git version 1.0.0\n"
			case sameArgs(request.Args, "integrate", "--help"):
				result.Stdout = "Usage:\n  gz-git integrate [command]\nAvailable Commands:\n  check inspect\n  run integrate\n"
			case len(request.Args) == 3 && request.Args[0] == "integrate" && request.Args[2] == "--help":
				result.Stdout = "Usage:\n  gz-git integrate " + request.Args[1] + " [flags]\nFlags:\n  --no-fetch no fetch\n  --release release\n  --target target\n  --expect-source source\n"
			default:
				result.ExitCode = 1
			}
			return result, nil
		}
		if request.Command == "wt" && sameArgs(request.Args, "--version") {
			return CommandResult{Command: "wt", Args: request.Args, WorkDir: request.WorkDir, Stdout: "wt 0.80.0\n"}, nil
		}
		return w.run(ctx, request)
	}}
	for _, purposes := range [][]string{nil, {string(PurposeExperiment)}} {
		result := service.Start(context.Background(), w.execution.Task, w.execution.Type, true, purposes...)
		if result.Status != StatusBlocked || !strings.Contains(result.Reason, "execution purpose or branch is invalid") {
			t.Fatalf("purposes=%v result=%+v", purposes, result)
		}
		after, err := os.ReadFile(newStateStore(w.common).executionsPath())
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("purposes=%v resume rewrote invalid execution\nbefore=%s\nafter=%s", purposes, before, after)
		}
		if afterReceipts := w.receipts(); afterReceipts != beforeReceipts {
			t.Fatalf("purposes=%v resume rewrote receipts\nbefore=%s\nafter=%s", purposes, beforeReceipts, afterReceipts)
		}
	}
}

func TestDoneReceiptBindsRejectsRecoverPurposePrefixMismatch(t *testing.T) {
	started := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	execution := Execution{Task: "task", Type: TypeFeat, Purpose: PurposeDelivery, Source: "master", Owner: Owner{Actor: "a", Host: "h"}, Branch: "dev/a/h/feat/task", Worktree: "/work/task", StartedAt: started}
	receipt := Receipt{
		Task: execution.Task, Operation: "recover", Status: StatusDone, Purpose: PurposeExperiment,
		Owner: execution.Owner, Branch: "exp/a/h/feat/task", Worktree: execution.Worktree,
		SourceHeadBefore: oidBase, TaskHead: oidTask, SourceHead: oidSource, SourcePushed: true,
	}
	if DoneReceiptBinds(execution, receipt) {
		t.Fatal("recover receipt with experiment purpose/prefix bound a delivery execution")
	}

	// Even if both persisted records share the malformed exp prefix, recovery
	// must reject it rather than bypassing purpose validation in its fallback.
	execution.Branch, receipt.Purpose = "exp/a/h/feat/task", PurposeDelivery
	if DoneReceiptBinds(execution, receipt) {
		t.Fatal("recover receipt bypassed delivery branch validation")
	}
}
