package taskruntime

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestFinishClaimReleasesMutationLockDuringProviderRun(t *testing.T) {
	w := newEndWorld(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	w.provider.runCode = 1
	svc := w.service()
	svc.now = time.Now
	w.provider.onRun = func() { close(entered); <-release }
	finished := make(chan struct {
		response Response
		code     int
	}, 1)
	go func() {
		r, c := svc.Finish(context.Background(), "sample-task")
		finished <- struct {
			response Response
			code     int
		}{r, c}
	}()
	<-entered

	status := w.service().Status(context.Background(), "sample-task")
	if status.Status != StatusActive || status.FinishReady || len(status.AllowedActions) != 1 || status.AllowedActions[0] != "run-status" {
		t.Fatalf("status during provider wait=%+v", status)
	}
	store := newStateStore(w.common)
	unlock, err := store.lock()
	if err != nil {
		t.Fatalf("provider wait retained mutation lock: %v", err)
	}
	unlock()
	blocked, code := w.service().Abort(context.Background(), "sample-task", "stop")
	if code != 1 || !strings.Contains(blocked.Reason, "FINISHING claim") {
		t.Fatalf("abort=%+v code=%d", blocked, code)
	}
	close(release)
	result := <-finished
	if result.code != 1 || result.response.Receipt == nil || result.response.Receipt.Status != StatusBlocked || len(result.response.Diagnostics) == 0 || w.provider.checks != 0 || w.provider.runs != 1 {
		t.Fatalf("finish=%+v code=%d", result.response, result.code)
	}
}

func TestClaimLivenessConservativeBoundaries(t *testing.T) {
	w := newEndWorld(t)
	svc := w.service()
	claim := Receipt{Status: StatusFinishing, FinishPID: os.Getpid(), FinishHost: lockHost(), CreatedAt: time.Now().UTC()}
	svc.claimProcessStart = func(context.Context, int) (time.Time, error) { return claim.CreatedAt, nil }
	if got := svc.claimLiveness(context.Background(), claim); got != claimAlive {
		t.Fatalf("live=%v", got)
	}
	svc.claimProcessStart = func(context.Context, int) (time.Time, error) { return claim.CreatedAt.Add(6 * time.Second), nil }
	if got := svc.claimLiveness(context.Background(), claim); got != claimDead {
		t.Fatalf("pid reuse=%v", got)
	}
	svc.claimProcessStart = func(context.Context, int) (time.Time, error) { return time.Time{}, errors.New("ps unavailable") }
	if got := svc.claimLiveness(context.Background(), claim); got != claimUnknown {
		t.Fatalf("probe failure=%v", got)
	}
	svc.claimProcessStart = func(context.Context, int) (time.Time, error) { return claim.CreatedAt, nil }
	claim.FinishHost = "foreign-host"
	if got := svc.claimLiveness(context.Background(), claim); got != claimUnknown {
		t.Fatalf("foreign=%v", got)
	}
}

func TestRecoverForeignClaimDeadDoesNotRerunProvider(t *testing.T) {
	w := newEndWorld(t)
	claim := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusFinishing, Owner: w.execution.Owner,
		Branch: w.execution.Branch, Worktree: w.execution.Worktree, CreatedAt: w.execution.StartedAt,
		FinishPID: 99, FinishHost: "foreign-host", SourceHeadBefore: w.sourceTip, BaseHead: w.sourceTip, TaskHead: oidTask}
	if err := newStateStore(w.common).appendReceipt(claim); err != nil {
		t.Fatal(err)
	}
	result, code := w.service().Recover(context.Background(), "sample-task", true)
	if code != 0 || result.Receipt == nil || result.Receipt.Operation != "recover" || result.Receipt.Status == StatusDone || result.Receipt.PerformedBy == nil {
		t.Fatalf("recover=%+v code=%d", result, code)
	}
	if w.provider.runs != 0 || w.provider.checks != 0 {
		t.Fatalf("provider was rerun: runs=%d checks=%d", w.provider.runs, w.provider.checks)
	}
}
