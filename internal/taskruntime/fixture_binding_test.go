//go:build unix

package taskruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFixtureExecutionBindingGuards(t *testing.T) {
	for _, scenario := range []string{"valid", "owner", "repository", "common", "generation", "purpose", "terminal", "held-lock", "callback-error", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			sandbox := filepath.Dir(w.root)
			if err := os.Chmod(sandbox, 0o700); err != nil {
				t.Fatal(err)
			}
			captured := w.execution
			repo, common := "sample-repo", w.common
			called := false
			failure := errors.New("synthetic persistence failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "owner":
				t.Setenv("CE_TASK_ACTOR", "other")
			case "repository":
				repo = "other"
			case "common":
				common = filepath.Join(sandbox, "other")
			case "generation":
				captured.StartedAt = captured.StartedAt.Add(time.Second)
			case "purpose":
				captured.Purpose = PurposeDelivery
			case "terminal":
				if err := newStateStore(w.common).appendReceipt(Receipt{Task: captured.Task, Status: StatusDone, CreatedAt: captured.StartedAt.Add(time.Second)}); err != nil {
					t.Fatal(err)
				}
			case "held-lock":
				unlock, e := newStateStore(w.common).lock()
				if e != nil {
					t.Fatal(e)
				}
				defer unlock()
			case "cancelled":
				cancel()
			}
			before, receipts := w.executions(), w.receipts()
			err := svc.WithFixtureExecution(ctx, sandbox, repo, common, captured, func() error {
				called = true
				if _, e := os.Stat(newStateStore(common).lockPath()); e != nil {
					t.Fatal("callback outside mutation lock")
				}
				if scenario == "callback-error" {
					return failure
				}
				return nil
			})
			wantCall := scenario == "valid" || scenario == "callback-error"
			if called != wantCall || (err == nil) != (scenario == "valid") {
				t.Fatalf("called=%v error=%v", called, err)
			}
			if scenario == "callback-error" && !errors.Is(err, failure) {
				t.Fatal("callback failure hidden")
			}
			if w.executions() != before || w.receipts() != receipts {
				t.Fatal("execution or receipt mutated")
			}
			if w.provider.checks != 0 || w.provider.runs != 0 {
				t.Fatal("provider invoked")
			}
		})
	}
}

func TestFixtureExecutionRejectsSourceMovement(t *testing.T) {
	w := newEndWorld(t)
	if err := os.Chmod(filepath.Dir(w.root), 0o700); err != nil {
		t.Fatal(err)
	}
	svc := w.service()
	calls := 0
	svc.runner = scriptRunner{fn: func(ctx context.Context, req CommandRequest) (CommandResult, error) {
		calls++
		if calls == 2 {
			return CommandResult{Stdout: filepath.Join(filepath.Dir(w.common), "moved")}, nil
		}
		return w.run(ctx, req)
	}}
	err := svc.WithFixtureExecution(context.Background(), filepath.Dir(w.root), "sample-repo", w.common, w.execution, func() error { t.Fatal("moved source was bound"); return nil })
	if err == nil {
		t.Fatal("source movement accepted")
	}
}

func TestFixtureExecutionRejectsUnsafeAncestorsBeforeLock(t *testing.T) {
	for _, scenario := range []string{"symlink", "writable", "outside"} {
		t.Run(scenario, func(t *testing.T) {
			w := newEndWorld(t)
			sandbox := filepath.Dir(w.root)
			if err := os.Chmod(sandbox, 0o700); err != nil {
				t.Fatal(err)
			}
			common := w.common
			if scenario == "symlink" {
				common = filepath.Join(sandbox, "alias")
				if err := os.Symlink(w.common, common); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "writable" {
				if err := os.Chmod(w.common, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "outside" {
				common = t.TempDir()
			}
			err := w.service().WithFixtureExecution(context.Background(), sandbox, "sample-repo", common, w.execution, func() error { t.Fatal("unsafe callback"); return nil })
			if err == nil {
				t.Fatal("unsafe ancestor accepted")
			}
			if _, err := os.Stat(newStateStore(w.common).lockPath()); !os.IsNotExist(err) {
				t.Fatal("lock touched before boundary validation")
			}
		})
	}
}
