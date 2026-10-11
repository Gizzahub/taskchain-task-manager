package taskruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFinishExitCodes(t *testing.T) {
	cases := []struct {
		name     string
		check    int
		run      int
		checkErr error
		stdout   string
		want     int
		reason   string
		stored   string
		runs     int
	}{
		{name: "check exit 1", check: 1, stdout: "FAIL base drifted\n", want: 1, reason: "integrate-check failed: base drifted", stored: "integrate-check failed: base drifted"},
		{name: "check exit 2", check: 2, stdout: "FAIL policy\n", want: 2, reason: "integrate-check failed: policy", stored: "integrate-check failed: policy"},
		{name: "check exit 3 folds", check: 3, want: 2, reason: "integration did not complete"},
		{name: "check exit 7 folds", check: 7, want: 2, reason: "integration did not complete"},
		{name: "run exit 1", run: 1, want: 1, reason: "integration did not complete", runs: 1},
		{name: "run exit 2", run: 2, want: 2, reason: "integration did not complete", runs: 1},
		{name: "run exit 4 folds", run: 4, want: 2, reason: "integration did not complete", runs: 1},
		{name: "provider error", checkErr: errors.New("tool missing"), want: 1, reason: "integration provider invocation failed: tool missing"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := newEndWorld(t)
			w.provider.checkCode, w.provider.runCode = tc.check, tc.run
			w.provider.checkErr, w.provider.checkStdout = tc.checkErr, tc.stdout
			before := w.executions()
			result, code := w.service().Finish(context.Background(), "sample-task")
			if code != tc.want || !strings.Contains(result.Reason, tc.reason) {
				t.Fatalf("code=%d reason=%q diagnostics=%v", code, result.Reason, result.Diagnostics)
			}
			if result.Execution != nil {
				t.Fatal("failure response included the execution")
			}
			if w.executions() != before {
				t.Fatal("finish failure rewrote executions.json")
			}
			if tc.stored != "" && (result.Receipt == nil || result.Receipt.Reason != tc.stored) {
				t.Fatalf("stored reason=%v", result.Receipt)
			}
			if w.provider.runs != tc.runs {
				t.Fatalf("runs=%d", w.provider.runs)
			}
		})
	}
}

func TestFinishRunExit3KeepsPartialCleanupReason(t *testing.T) {
	w := newEndWorld(t)
	w.provider.runCode = 3
	before := w.executions()
	result, code := w.service().Finish(context.Background(), "sample-task")
	if code != 3 || !strings.Contains(result.Reason, partialCleanupReason) || !strings.Contains(result.Reason, "post-operation evidence failed: task recovery is incomplete") {
		t.Fatalf("code=%d reason=%q", code, result.Reason)
	}
	if result.Receipt == nil || result.Receipt.Reason != partialCleanupReason || !result.Receipt.SourcePushed {
		t.Fatalf("receipt=%+v", result.Receipt)
	}
	if !isPartialCleanupReceipt(w.execution, *result.Receipt) {
		t.Fatal("stored receipt is not a partial cleanup")
	}
	if w.executions() != before {
		t.Fatal("exit 3 rewrote executions.json")
	}
	recovered, recoverCode := w.service().Recover(context.Background(), "sample-task")
	if recoverCode != 0 || recovered.Status != StatusDone || recovered.Receipt == nil || recovered.Receipt.Operation != "recover" {
		t.Fatalf("recover code=%d status=%s reason=%q", recoverCode, recovered.Status, recovered.Reason)
	}
	if _, err := os.Stat(w.worktree); err != nil {
		t.Fatal("recover removed the residual worktree")
	}
	receipts := w.receipts()
	again, againCode := w.service().Recover(context.Background(), "sample-task")
	if againCode != 0 || !strings.Contains(again.Reason, "already DONE") || w.receipts() != receipts || w.executions() != before {
		t.Fatalf("second recover code=%d reason=%q", againCode, again.Reason)
	}
}

func TestFinishSuccessAndAlreadyDone(t *testing.T) {
	w := newEndWorld(t)
	w.provider.onRun = func() { _ = os.RemoveAll(w.worktree) }
	before := w.executions()
	result, code := w.service().Finish(context.Background(), "sample-task")
	if code != 0 || result.Status != StatusDone || result.Reason != "integration and recovery completed" || result.AllowedActions != nil {
		t.Fatalf("code=%d result=%+v", code, result)
	}
	if result.Receipt == nil || !result.Receipt.WorktreeRemoved || !result.Receipt.LocalBranchRemoved || !result.Receipt.RemoteBranchRemoved || !result.Receipt.SourcePushed {
		t.Fatalf("receipt=%+v", result.Receipt)
	}
	if w.executions() != before {
		t.Fatal("success rewrote executions.json")
	}
	if _, err := os.Stat(w.worktree); !os.IsNotExist(err) {
		t.Fatal("worktree still exists")
	}
	receipts := w.receipts()
	again, againCode := w.service().Finish(context.Background(), "sample-task")
	if againCode != 0 || again.Status != StatusDone || w.receipts() != receipts || w.executions() != before {
		t.Fatalf("second finish code=%d status=%s reason=%q", againCode, again.Status, again.Reason)
	}
}

func TestFinishEvidenceAndPersistFailuresExit3(t *testing.T) {
	t.Run("evidence", func(t *testing.T) {
		w := newEndWorld(t)
		before := w.executions()
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 3 || !strings.Contains(result.Reason, "integration completed but evidence verification failed") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
		if w.receipts() != "" || w.executions() != before {
			t.Fatal("evidence failure persisted a receipt or rewrote executions")
		}
	})
	t.Run("persist", func(t *testing.T) {
		w := newEndWorld(t)
		w.provider.onRun = func() {
			_ = os.RemoveAll(w.worktree)
			// Status already read the ledger. Replacing the receipt file after
			// the provider run makes only the final append fail.
			_ = os.MkdirAll(newStateStore(w.common).receiptsPath(), 0o755)
		}
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 3 || !strings.Contains(result.Reason, "integration completed but receipt persistence failed") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
}

func TestFinishRefusals(t *testing.T) {
	t.Run("not owner", func(t *testing.T) {
		w := newEndWorld(t)
		t.Setenv("CE_TASK_ACTOR", "other")
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 1 || !strings.Contains(result.Reason, "only tester/testhost may finish") || w.provider.checks != 0 {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("dirty tree", func(t *testing.T) {
		w := newEndWorld(t)
		w.finishPorcelain = " M tracked\n"
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 1 || result.Reason != "task worktree is dirty" || w.provider.checks != 0 {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("stale base", func(t *testing.T) {
		w := newEndWorld(t)
		w.ancestorExit = 1
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 1 || !strings.Contains(result.Reason, "no longer the source tip") || w.provider.checks != 0 {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("stale after failed check keeps provider code", func(t *testing.T) {
		w := newEndWorld(t)
		w.provider.checkCode = 2
		w.provider.beforeCheck = func() {
			w.mu.Lock()
			w.ancestorExit = 1
			w.mu.Unlock()
		}
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 2 || !strings.Contains(result.Reason, "no longer the source tip") || w.provider.runs != 0 {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("source tip moves during check", func(t *testing.T) {
		w := newEndWorld(t)
		w.provider.wait = true
		w.provider.beforeCheck = func() {
			w.mu.Lock()
			w.sourceTip = oidMoved
			w.mu.Unlock()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		result, code := w.service().Finish(ctx, "sample-task")
		if code != 1 || !strings.Contains(result.Reason, "stopped early") || !strings.Contains(result.Reason, oidMoved) || w.provider.runs != 0 {
			t.Fatalf("code=%d reason=%q runs=%d", code, result.Reason, w.provider.runs)
		}
	})
	t.Run("no-fetch capability", func(t *testing.T) {
		w := newEndWorld(t)
		body, err := os.ReadFile(w.root + "/" + ConfigFile)
		if err != nil {
			t.Fatal(err)
		}
		replaced := strings.Replace(string(body), "allow", "no-fetch", 1)
		if err := os.WriteFile(w.root+"/"+ConfigFile, []byte(replaced), 0o644); err != nil {
			t.Fatal(err)
		}
		w.provider.diagnoseErr = errors.New("flag absent")
		result, code := w.service().Finish(context.Background(), "sample-task")
		if code != 1 || !strings.Contains(result.Reason, "no declared no-fetch finish capability") || w.provider.checks != 0 {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
		if result.Receipt == nil || !hasNotStarted(result.Receipt.Diagnostics) {
			t.Fatal("missing not-started diagnostic")
		}
	})
	t.Run("bare none", func(t *testing.T) {
		w := newEndWorld(t)
		if err := newStateStore(w.common).save(nil); err != nil {
			t.Fatal(err)
		}
		result, code := w.service().Finish(context.Background(), "")
		if code != 1 || !strings.Contains(result.Reason, "no active execution owned by tester/testhost") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("bare ambiguous", func(t *testing.T) {
		w := newEndWorld(t)
		w.noUpstream = true
		second := w.execution
		second.Task = "other-task"
		second.Branch = "dev/tester/testhost/feat/other-task"
		second.Worktree = filepath.Join(filepath.Dir(w.worktree), "tester__testhost__feat__other-task")
		w.extraBranch, w.extraWorktree = second.Branch, second.Worktree
		if err := os.MkdirAll(second.Worktree, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := newStateStore(w.common).save([]Execution{w.execution, second}); err != nil {
			t.Fatal(err)
		}
		result, code := w.service().Finish(context.Background(), "")
		if code != 1 || !strings.Contains(result.Reason, "ambiguous") || !strings.Contains(result.NextAction, "ce task run-finish other-task") {
			t.Fatalf("code=%d reason=%q next=%q", code, result.Reason, result.NextAction)
		}
	})
	t.Run("bare one", func(t *testing.T) {
		w := newEndWorld(t)
		w.provider.onRun = func() { _ = os.RemoveAll(w.worktree) }
		result, code := w.service().Finish(context.Background(), " ")
		if code != 0 || result.Task != "sample-task" || result.Status != StatusDone {
			t.Fatalf("code=%d task=%s status=%s reason=%q", code, result.Task, result.Status, result.Reason)
		}
	})
}

func hasNotStarted(items []CommandDiagnostic) bool {
	for _, item := range items {
		if item.NotStarted && item.Command == providerGZGit {
			return true
		}
	}
	return false
}
