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

func TestFixtureExecutionRejectsRuntimeStateSymlinks(t *testing.T) {
	for _, part := range []string{RuntimeStateDir, filepath.Join(RuntimeStateDir, "task-runtime"), filepath.Join(RuntimeStateDir, "task-runtime", "v1")} {
		t.Run(part, func(t *testing.T) {
			w := newEndWorld(t)
			sandbox := filepath.Dir(w.root)
			if err := os.Chmod(sandbox, 0o700); err != nil {
				t.Fatal(err)
			}
			outside, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(w.common, part)
			executions := w.executions()
			if err := os.Rename(path, path+"-retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			wantEntries := 0
			if part == filepath.Join(RuntimeStateDir, "task-runtime", "v1") {
				if err := os.WriteFile(filepath.Join(outside, "executions.json"), []byte(executions), 0o644); err != nil {
					t.Fatal(err)
				}
				wantEntries = 1
			}
			err = w.service().WithFixtureExecution(context.Background(), sandbox, "sample-repo", w.common, w.execution, func() error { t.Fatal("symlink binding"); return nil })
			if err == nil {
				t.Fatal("runtime-state symlink accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != wantEntries {
				t.Fatal("runtime-state/lock created outside sandbox")
			}
		})
	}
}

func TestFixtureRuntimeLockRemainsPinnedAfterReplacement(t *testing.T) {
	w := newEndWorld(t)
	sandbox := filepath.Dir(w.root)
	if err := os.Chmod(sandbox, 0o700); err != nil {
		t.Fatal(err)
	}
	r, err := OpenFixtureSandbox(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	s, check, err := newStateStore(w.common).pinFixture(r, sandbox)
	if err != nil {
		t.Fatal(err)
	}
	defer s.fixtureRoot.Close()
	if err := os.Rename(s.dir, s.dir+"-retained"); err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, s.dir); err != nil {
		t.Fatal(err)
	}
	unlock, err := s.lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := os.Stat(filepath.Join(s.dir+"-retained", "mutation.lock")); err != nil {
		t.Fatal("lock not pinned", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("lock escaped pinned directory", err)
	}
	if err := check(); err == nil {
		t.Fatal("moved state path was accepted")
	}
	if err := s.save([]Execution{w.execution}); err == nil {
		t.Fatal("fixture runtime execution save enabled")
	}
	if err := s.appendReceipt(Receipt{Task: w.execution.Task}); err == nil {
		t.Fatal("fixture runtime receipt append enabled")
	}
}
