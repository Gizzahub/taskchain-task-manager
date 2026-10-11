package taskruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAbortRefusalsAndNoOp(t *testing.T) {
	w := newEndWorld(t)
	result, code := w.service().Abort(context.Background(), "---", "")
	if code != 1 || result.Reason != "task is required" {
		t.Fatalf("empty code=%d reason=%q", code, result.Reason)
	}
	missing, missingCode := w.service().Abort(context.Background(), "missing-task", "")
	if missingCode != 1 || missing.Status != StatusUnknown || len(missing.AllowedActions) != 1 {
		t.Fatalf("missing %+v", missing)
	}
	t.Setenv("CE_TASK_ACTOR", "other")
	refused, refusedCode := w.service().Abort(context.Background(), "sample-task", "stop")
	if refusedCode != 1 || !strings.Contains(refused.Reason, "only tester/testhost may abort") || w.receipts() != "" {
		t.Fatalf("refused code=%d reason=%q", refusedCode, refused.Reason)
	}
	t.Setenv("CE_TASK_ACTOR", "tester")
	before := w.executions()
	closed, closedCode := w.service().Abort(context.Background(), "Sample-Task", "stop")
	if closedCode != 0 || closed.Status != StatusAborted || len(closed.Warnings) != 1 || !strings.Contains(closed.Reason, "stop") {
		t.Fatalf("abort code=%d reason=%q warnings=%v", closedCode, closed.Reason, closed.Warnings)
	}
	if w.executions() != before {
		t.Fatal("abort rewrote executions.json")
	}
	receipts := w.receipts()
	again, againCode := w.service().Abort(context.Background(), "sample-task", "again")
	if againCode != 0 || !strings.Contains(again.Reason, "already ABORTED") || w.receipts() != receipts || w.executions() != before {
		t.Fatalf("second code=%d reason=%q", againCode, again.Reason)
	}
}

func TestAbortPreservesMalformedLedgerAndLiveLock(t *testing.T) {
	w := newEndWorld(t)
	path := newStateStore(w.common).executionsPath()
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, code := w.service().Abort(context.Background(), "sample-task", "")
	if code != 1 || !strings.Contains(result.Reason, "decode task executions") {
		t.Fatalf("code=%d reason=%q", code, result.Reason)
	}
	if got := mustText(t, path); got != "{" {
		t.Fatalf("malformed bytes became %q", got)
	}
	if err := newStateStore(w.common).save([]Execution{w.execution}); err != nil {
		t.Fatal(err)
	}
	lockPath := newStateStore(w.common).lockPath()
	body := "pid=1 host=other-host created=2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(lockPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	locked, lockedCode := w.service().Abort(context.Background(), "sample-task", "")
	if lockedCode != 1 || !strings.Contains(locked.Reason, "mutation lock") || !strings.Contains(locked.NextAction, "run-doctor") {
		t.Fatalf("lock code=%d reason=%q next=%q", lockedCode, locked.Reason, locked.NextAction)
	}
	if got := mustText(t, lockPath); got != body {
		t.Fatalf("lock became %q", got)
	}
	admin := "schema-version: 3\nrepository-id: sample-repo\nworktree-roots:\n  testhost: " + filepath.Dir(w.worktree) + "\nintegration-provider: gz-git\nintegration-network-policy: allow\nexecution-administrators:\n  - {actor: tester, host: testhost, kind: agent}\n"
	if err := os.WriteFile(filepath.Join(w.root, ConfigFile), []byte(admin), 0o644); err != nil {
		t.Fatal(err)
	}
	closed, closedCode := w.service().Close(context.Background(), "sample-task", "gone")
	if closedCode != 1 || closed.NextAction != "inspect mutation lock" || mustText(t, lockPath) != body {
		t.Fatalf("close lock code=%d reason=%q next=%q", closedCode, closed.NextAction, closed.Reason)
	}
}

func TestRecoverRefusesTheWrongCallerAndOutcome(t *testing.T) {
	w := newEndWorld(t)
	result, code := w.service().Recover(context.Background(), "sample-task")
	if code != 1 || result.Reason != "run-recover requires the recorded exit-3 provider cleanup failure" {
		t.Fatalf("code=%d reason=%q", code, result.Reason)
	}
	receipt := partialReceipt(w.execution, w.execution.StartedAt)
	if err := newStateStore(w.common).appendReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_WORK_HOST", "otherhost")
	refused, refusedCode := w.service().Recover(context.Background(), "sample-task")
	if refusedCode != 1 || !strings.Contains(refused.Reason, "only tester/testhost may recover") {
		t.Fatalf("code=%d reason=%q", refusedCode, refused.Reason)
	}
	if strings.Count(w.receipts(), "\n") != 1 {
		t.Fatal("refused recover appended a receipt")
	}
}

func TestCloseAdministratorRules(t *testing.T) {
	w := newEndWorld(t)
	_ = os.RemoveAll(w.worktree)
	missing, code := w.service().Close(context.Background(), "sample-task", "abandoned")
	if code != 1 || missing.Reason != "caller is not a repository-designated execution administrator" {
		t.Fatalf("missing code=%d reason=%q", code, missing.Reason)
	}
	admin := "schema-version: 3\nrepository-id: sample-repo\nworktree-roots:\n  testhost: " + filepath.Dir(w.worktree) + "\nintegration-provider: gz-git\nintegration-network-policy: allow\nexecution-administrators:\n  - {actor: tester, host: testhost, kind: agent}\n"
	if err := os.WriteFile(filepath.Join(w.root, ConfigFile), []byte(admin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(w.worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	present, presentCode := w.service().Close(context.Background(), "sample-task", "abandoned")
	if presentCode != 1 || present.Reason != "recorded worktree is present or cannot be inspected" {
		t.Fatalf("present code=%d reason=%q", presentCode, present.Reason)
	}
	_ = os.RemoveAll(w.worktree)
	before := w.executions()
	closed, closedCode := w.service().Close(context.Background(), "sample-task", "abandoned")
	if closedCode != 0 || closed.Status != StatusAborted || closed.Receipt == nil || closed.Receipt.Operation != "close" || closed.Receipt.Owner.Actor != "tester" {
		t.Fatalf("close code=%d result=%+v", closedCode, closed)
	}
	if closed.Receipt.PerformedBy == nil || closed.Receipt.ToolRevision == "" || closed.Receipt.TaskHead != "" || closed.Receipt.WorktreeRemoved {
		t.Fatalf("receipt=%+v", closed.Receipt)
	}
	if w.executions() != before {
		t.Fatal("close rewrote executions.json")
	}
	receipts := w.receipts()
	again, againCode := w.service().Close(context.Background(), "sample-task", "again")
	if againCode != 0 || again.Reason != "execution is already terminal" || w.receipts() != receipts {
		t.Fatalf("second code=%d reason=%q", againCode, again.Reason)
	}
}

func TestSettleContainment(t *testing.T) {
	w := newEndWorld(t)
	present, presentCode := w.service().Settle(context.Background(), "sample-task")
	if presentCode != 1 || present.Reason != "recorded worktree still exists" {
		t.Fatalf("present code=%d reason=%q", presentCode, present.Reason)
	}
	if err := os.RemoveAll(w.worktree); err != nil {
		t.Fatal(err)
	}
	w.containedExit = 1
	outside, outsideCode := w.service().Settle(context.Background(), "sample-task")
	if outsideCode != 1 || outside.Reason != "recorded task tip is not contained in named source ref" {
		t.Fatalf("outside code=%d reason=%q", outsideCode, outside.Reason)
	}
	w.containedExit = 0
	t.Setenv("CE_TASK_ACTOR", "other")
	before := w.executions()
	settled, settledCode := w.service().Settle(context.Background(), "sample-task")
	if settledCode != 0 || settled.Receipt == nil || settled.Receipt.Operation != "settle" || settled.Receipt.TaskHead != oidTask || settled.Receipt.SourceHead != oidSource {
		t.Fatalf("settle code=%d receipt=%+v reason=%q", settledCode, settled.Receipt, settled.Reason)
	}
	if settled.Receipt.Reason != "CE recorded settlement: the surviving task branch tip is contained in the named source branch" {
		t.Fatalf("reason=%q", settled.Receipt.Reason)
	}
	if settled.Receipt.PerformedBy == nil || settled.Receipt.PerformedBy.Actor != "other" || settled.Receipt.Owner.Actor != "tester" {
		t.Fatalf("identity=%+v", settled.Receipt)
	}
	if w.executions() != before {
		t.Fatal("settle rewrote executions.json")
	}
}

func TestDiscardRefusalsAndTakeover(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		w := newEndWorld(t)
		w.derivePorcelain = "?? scratch\n"
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "")
		if code != 1 || result.Reason != "task worktree is dirty" {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("pushed", func(t *testing.T) {
		w := newEndWorld(t)
		w.lsRemote = 0
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "")
		if code != 1 || result.Reason != "task branch still exists on origin" {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("wrong host", func(t *testing.T) {
		w := newEndWorld(t)
		t.Setenv("GIT_WORK_HOST", "otherhost")
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "tester/testhost")
		if code != 1 || !strings.Contains(result.Reason, "on host testhost") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("not owner", func(t *testing.T) {
		w := newEndWorld(t)
		t.Setenv("CE_TASK_ACTOR", "other")
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "")
		if code != 1 || !strings.Contains(result.Reason, "only tester/testhost may discard") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("no-fetch", func(t *testing.T) {
		w := newEndWorld(t)
		body := mustText(t, filepath.Join(w.root, ConfigFile))
		if err := os.WriteFile(filepath.Join(w.root, ConfigFile), []byte(strings.Replace(body, "allow", "no-fetch", 1)), 0o644); err != nil {
			t.Fatal(err)
		}
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "")
		if code != 1 || !strings.Contains(result.Reason, "no-fetch cannot prove remote branch absence") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
	})
	t.Run("takeover", func(t *testing.T) {
		w := newEndWorld(t)
		t.Setenv("CE_TASK_ACTOR", "other")
		before := w.executions()
		result, code := w.service().Discard(context.Background(), "sample-task", "scratch", "tester/testhost")
		if code != 0 || result.Status != StatusAborted || result.Receipt == nil || !strings.HasPrefix(result.Receipt.Reason, "discard completed: ") {
			t.Fatalf("code=%d reason=%q", code, result.Reason)
		}
		if result.Receipt.PerformedBy == nil || result.Receipt.PerformedBy.Actor != "other" || result.Receipt.Owner.Actor != "tester" {
			t.Fatalf("identity=%+v", result.Receipt)
		}
		if !result.Receipt.WorktreeRemoved || !result.Receipt.LocalBranchRemoved || !result.Receipt.RemoteBranchRemoved {
			t.Fatalf("flags=%+v", result.Receipt)
		}
		if _, err := os.Stat(w.worktree); !os.IsNotExist(err) {
			t.Fatal("worktree remains")
		}
		if w.executions() != before {
			t.Fatal("discard rewrote executions.json")
		}
		if strings.Count(w.receipts(), `"operation":"discard"`) < 2 && strings.Count(w.receipts(), `"operation": "discard"`) < 2 {
			t.Fatalf("intent receipt missing:\n%s", w.receipts())
		}
		receipts := w.receipts()
		again, againCode := w.service().Discard(context.Background(), "sample-task", "again", "tester/testhost")
		if againCode != 0 || !strings.Contains(again.Reason, "already discarded") || w.receipts() != receipts {
			t.Fatalf("second code=%d reason=%q", againCode, again.Reason)
		}
	})
}

func TestDiscardRefusesDoneAndPartialCleanup(t *testing.T) {
	w := newEndWorld(t)
	if err := newStateStore(w.common).appendReceipt(doneReceipt(w.execution)); err != nil {
		t.Fatal(err)
	}
	done, doneCode := w.service().Discard(context.Background(), "sample-task", "scratch", "")
	if doneCode != 1 || done.Reason != "run-discard refuses an execution with a DONE receipt" {
		t.Fatalf("done code=%d reason=%q", doneCode, done.Reason)
	}
	w = newEndWorld(t)
	if err := newStateStore(w.common).appendReceipt(partialReceipt(w.execution, time.Date(2026, 10, 11, 2, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	partial, partialCode := w.service().Discard(context.Background(), "sample-task", "scratch", "")
	if partialCode != 1 || !strings.Contains(partial.Reason, "recorded integration cleanup failure") {
		t.Fatalf("partial code=%d reason=%q", partialCode, partial.Reason)
	}
}

func mustText(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
