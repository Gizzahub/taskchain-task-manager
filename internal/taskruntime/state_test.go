package taskruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestDerivedStateOrder(t *testing.T) {
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	owner := Owner{Actor: "tester", Host: "testhost", Kind: "agent"}
	execution := Execution{Task: "sample-task", Type: TypeFeat, Source: "master", Owner: owner, Branch: "dev/tester/testhost/feat/sample-task", Worktree: "/work/sample-task", StartedAt: now, UpdatedAt: now}
	svc := &Service{root: t.TempDir(), runner: forbidRunner{t}, now: time.Now}
	cases := []struct {
		name    string
		receipt Receipt
		locked  bool
		status  string
		reason  string
		next    string
		actions string
	}{
		{name: "terminal done", receipt: doneReceipt(execution), status: StatusDone, reason: "integrated", next: "no action required", actions: "run-status"},
		{name: "invalid abort", receipt: Receipt{Task: execution.Task, Operation: "abort", Status: StatusAborted, Owner: execution.Owner, Branch: execution.Branch, Worktree: execution.Worktree, CreatedAt: now.Add(-time.Hour)}, status: StatusBlocked, reason: "abort receipt does not bind and close this execution", next: "inspect task runtime receipt evidence", actions: "run-status,run-abort"},
		{name: "partial cleanup", receipt: partialReceipt(execution, now), status: StatusBlocked, reason: partialCleanupReason, next: "run ce task run-recover sample-task", actions: "run-status,run-recover,run-abort"},
		{name: "lock", locked: true, status: StatusBlocked, reason: "repository task runtime mutation is in progress or stale", next: "inspect the mutation lock with run-doctor", actions: "run-status,run-abort"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			state := svc.verifiedExecutionState(context.Background(), execution, test.receipt, worktrunkInventory{}, test.locked)
			if state.Status != test.status || state.Reason != test.reason || state.NextAction != test.next || strings.Join(state.AllowedActions, ",") != test.actions {
				t.Fatalf("state %+v", state)
			}
		})
	}
}

func TestHasActiveRun(t *testing.T) {
	dir := t.TempDir()
	store := newStateStore(dir)
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	execution := Execution{Task: "task-345", Type: TypeFix, Source: "master", Owner: Owner{Actor: "tester", Host: "testhost", Kind: "agent"}, Branch: "dev/tester/testhost/fix/task-345", Worktree: "/work/task", StartedAt: now, UpdatedAt: now}
	if err := store.save([]Execution{execution}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{root: t.TempDir(), runner: commonDirRunner{dir: dir}, now: time.Now}
	active, err := svc.HasActiveRun(context.Background(), "TASK-345")
	if err != nil || !active {
		t.Fatalf("active=%v err=%v", active, err)
	}
	receipt := doneReceipt(execution)
	if err := store.appendReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	active, err = svc.HasActiveRun(context.Background(), "task-345")
	if err != nil || active {
		t.Fatalf("closed active=%v err=%v", active, err)
	}
	empty, err := svc.HasActiveRun(context.Background(), "---")
	if err != nil || empty {
		t.Fatalf("empty id active=%v err=%v", empty, err)
	}
}

type forbidRunner struct{ t *testing.T }

func (r forbidRunner) Run(context.Context, CommandRequest) (CommandResult, error) {
	r.t.Fatal("derivation probed a command before the receipt and lock checks")
	return CommandResult{}, nil
}

type commonDirRunner struct{ dir string }

func (r commonDirRunner) Run(_ context.Context, req CommandRequest) (CommandResult, error) {
	if req.Command == "git" && len(req.Args) > 0 && req.Args[0] == "rev-parse" {
		return CommandResult{Command: "git", Args: req.Args, Stdout: r.dir + "\n"}, nil
	}
	return CommandResult{Command: req.Command, Args: req.Args, ExitCode: 1, Stderr: "unexpected\n"}, nil
}

func doneReceipt(execution Execution) Receipt {
	return Receipt{
		Task: execution.Task, Operation: "finish", Status: StatusDone, Owner: execution.Owner,
		Branch: execution.Branch, Worktree: execution.Worktree, Reason: "integrated", CreatedAt: execution.StartedAt,
		SourceHeadBefore: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TaskHead: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SourceHead: "cccccccccccccccccccccccccccccccccccccccc", SourcePushed: true, WorktreeRemoved: true,
		LocalBranchRemoved: true, RemoteBranchRemoved: true,
	}
}

func partialReceipt(execution Execution, now time.Time) Receipt {
	return Receipt{
		Task: execution.Task, Operation: "finish", Status: StatusBlocked, Owner: execution.Owner,
		Branch: execution.Branch, Worktree: execution.Worktree, Reason: partialCleanupReason, CreatedAt: now,
		SourceHeadBefore: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TaskHead: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		SourcePushed: true,
		Diagnostics:  []CommandDiagnostic{{Command: providerGZGit, Args: []string{"integrate", "run", "--target", "origin/master"}, ExitCode: 3}},
	}
}
