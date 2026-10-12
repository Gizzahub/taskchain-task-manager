package taskruntime

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFinishClaimCASPreservesForeignChanges(t *testing.T) {
	for _, change := range []string{"claim-field", "generation", "purpose", "terminal"} {
		for _, outcome := range []struct {
			name string
			exit int
			err  error
			want int
		}{
			{"success", 0, nil, 3}, {"partial", 3, nil, 3}, {"failure", 1, nil, 1}, {"error", 0, errors.New("provider transport lost"), 1},
		} {
			t.Run(change+"/"+outcome.name, func(t *testing.T) {
				w := newEndWorld(t)
				svc := w.service()
				svc.now = time.Now
				store := newStateStore(w.common)
				w.provider.runCode, w.provider.runErr = outcome.exit, outcome.err
				var changed string
				w.provider.onRun = func() {
					unlock, err := store.lock()
					if err != nil {
						t.Fatal(err)
					}
					defer unlock()
					if change == "generation" || change == "purpose" {
						e := w.execution
						if change == "purpose" {
							e.Purpose = PurposeReview
						} else {
							e.StartedAt = e.StartedAt.Add(time.Second)
						}
						if err := store.save([]Execution{e}); err != nil {
							t.Fatal(err)
						}
					} else {
						receipts, err := store.latestReceipts()
						if err != nil {
							t.Fatal(err)
						}
						r := receipts[w.execution.Task]
						if change == "terminal" {
							r.Status = StatusAborted
							r.Operation = "abort"
						} else {
							r.Reason = "changed by another writer"
						}
						if err := store.appendReceipt(r); err != nil {
							t.Fatal(err)
						}
					}
					changed = w.receipts()
				}
				r, code := svc.Finish(context.Background(), w.execution.Task)
				if code != outcome.want || r.Receipt != nil || r.Execution == nil || r.Execution.StartedAt != w.execution.StartedAt || len(r.Diagnostics) == 0 {
					t.Fatalf("code %d response %+v", code, r)
				}
				if w.receipts() != changed {
					t.Fatal("late finish overwrote changed claim or terminal receipt")
				}
				if w.provider.checks != 0 || w.provider.runs != 1 {
					t.Fatalf("provider checks/runs %d/%d", w.provider.checks, w.provider.runs)
				}
				if outcome.err != nil && r.Diagnostics[len(r.Diagnostics)-1].Error != outcome.err.Error() {
					t.Fatal("provider error diagnostic lost")
				}
			})
		}
	}
}

func TestFinishClaimRelockTimeoutAndCancellationLeaveClaim(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			svc.now = time.Now
			svc.finishRelockWait = 30 * time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			store := newStateStore(w.common)
			var unlock func()
			var before string
			t.Cleanup(func() {
				if unlock != nil {
					unlock()
				}
			})
			w.provider.onRun = func() {
				var err error
				unlock, err = store.lock()
				if err != nil {
					t.Fatal(err)
				}
				before = w.receipts()
				if mode == "cancel" {
					cancel()
				}
			}
			r, code := svc.Finish(ctx, w.execution.Task)
			if code != 3 || r.Receipt != nil || r.Execution == nil || len(r.Diagnostics) == 0 || !strings.Contains(r.Reason, "reacquired") {
				t.Fatalf("code %d response %+v", code, r)
			}
			want := "deadline exceeded"
			if mode == "cancel" {
				want = "context canceled"
			}
			if !strings.Contains(r.Reason, want) {
				t.Fatalf("wrong relock outcome: %+v", r)
			}
			if w.receipts() != before {
				t.Fatal("unlocked outcome appended on failed relock")
			}
		})
	}
}

func TestFinishAllowsAnotherTaskMutationWhileProviderWaits(t *testing.T) {
	w := newEndWorld(t)
	svc := w.service()
	svc.now = time.Now
	other := w.execution
	other.Task = "other-task"
	other.Branch += "-other"
	other.Worktree += "-other"
	if err := newStateStore(w.common).save([]Execution{w.execution, other}); err != nil {
		t.Fatal(err)
	}
	w.provider.runCode = 1
	w.provider.onRun = func() {
		r, code := svc.Abort(context.Background(), other.Task, "unrelated task preservation")
		if code != 0 || r.Status != StatusAborted {
			t.Fatalf("unrelated mutation blocked %+v/%d", r, code)
		}
	}
	_, code := svc.Finish(context.Background(), w.execution.Task)
	if code != 1 {
		t.Fatalf("provider failure changed %d", code)
	}
	receipts, err := newStateStore(w.common).latestReceipts()
	if err != nil {
		t.Fatal(err)
	}
	if receipts[other.Task].Status != StatusAborted {
		t.Fatal("unrelated terminal receipt lost")
	}
}

func TestDeadAndLiveClaimRecoveryBoundaries(t *testing.T) {
	for _, dead := range []bool{false, true} {
		t.Run(map[bool]string{false: "live", true: "dead"}[dead], func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			svc.now = time.Now
			claim := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusFinishing, Owner: w.execution.Owner, Branch: w.execution.Branch, Worktree: w.execution.Worktree, CreatedAt: time.Now().UTC(), FinishPID: os.Getpid(), FinishHost: lockHost(), TaskHead: oidTask}
			svc.claimProcessStart = func(context.Context, int) (time.Time, error) { return claim.CreatedAt.Add(-time.Second), nil }
			if dead {
				claim.FinishPID = 1 << 29
			}
			store := newStateStore(w.common)
			if err := store.appendReceipt(claim); err != nil {
				t.Fatal(err)
			}
			before := w.receipts()
			status := svc.Status(context.Background(), w.execution.Task)
			if dead && (status.Status != StatusBlocked || len(status.AllowedActions) != 2) {
				t.Fatalf("dead status %+v", status)
			}
			r, code := svc.Recover(context.Background(), w.execution.Task, !dead)
			if dead {
				if code != 0 || r.Receipt == nil || r.Receipt.Status == StatusDone {
					t.Fatalf("dead recovery %+v/%d", r, code)
				}
			} else if code != 1 || w.receipts() != before {
				t.Fatalf("live claim was released %+v/%d", r, code)
			}
			if w.provider.runs != 0 || w.provider.checks != 0 {
				t.Fatal("recovery reinvoked provider")
			}
		})
	}
}

func TestFinishClaimParsedIdentityIgnoresTimestampPresentation(t *testing.T) {
	now := time.Now().UTC()
	claim := Receipt{Task: "test", Operation: "finish", Status: StatusFinishing, CreatedAt: now, ExecutionStartedAt: &now}
	same := claim
	same.CreatedAt = now.In(time.FixedZone("offset", 3600))
	start := now.In(time.FixedZone("offset", 3600))
	same.ExecutionStartedAt = &start
	if !sameFinishClaim(same, claim) {
		t.Fatal("equivalent parsed timestamps changed claim identity")
	}
	same.Reason = "new meaning"
	if sameFinishClaim(same, claim) {
		t.Fatal("claim field change accepted")
	}
}
