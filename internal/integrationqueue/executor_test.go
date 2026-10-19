package integrationqueue

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

type testAdapter struct {
	hook      func(Request)
	execCalls int32
	recCalls  int32
	exec      Outcome
	rec       Outcome
}

type blockingAdapter struct {
	entered chan struct{}
	release chan struct{}
	calls   int32
}

func (a *blockingAdapter) Execute(context.Context, Request) Outcome {
	if atomic.AddInt32(&a.calls, 1) > 1 {
		return Outcome{State: Succeeded}
	}
	close(a.entered)
	<-a.release
	return Outcome{State: Succeeded}
}
func (a *blockingAdapter) Reconcile(context.Context, Request) Outcome {
	return Outcome{State: Succeeded}
}

func (a *testAdapter) Execute(_ context.Context, r Request) Outcome {
	atomic.AddInt32(&a.execCalls, 1)
	if a.hook != nil {
		a.hook(r)
	}
	return a.exec
}

func TestExecutorPublicationCASPreservesObservedOutcome(t *testing.T) {
	for _, code := range []int{0, 4} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Enqueue(spec("a", "ka", "one"))
			if err != nil {
				t.Fatal(err)
			}
			state := Succeeded
			if code == 4 {
				state = RevalidationRequired
			}
			a := &testAdapter{exec: Outcome{State: state, ExitCode: code, Reason: "actual provider result"}}
			a.hook = func(r Request) {
				if _, err := s.Mark(r.Spec.ID, r.Revision, r.Claim, Running, "newer CAS writer", 125); err != nil {
					t.Fatal(err)
				}
			}
			claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 42, Host: "h", ClaimedAt: r.CreatedAt}
			out, err := NewExecutor(s, a).Execute(context.Background(), r.Spec.ID, r.Revision, claim)
			if err == nil || out.State != state || out.ExitCode != code || out.Reason != "actual provider result" {
				t.Fatalf("lost result %+v err%v", out, err)
			}
			view, err := s.Get(r.Spec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if view.Request.State != Running || view.Request.LastError != "newer CAS writer" || a.execCalls != 1 {
				t.Fatal("overwrote CAS or repeated integration", view)
			}
		})
	}
}

func TestExecutorEarlyRevalidationPreservesExitFour(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(spec("a", "ka", "one"))
	if err != nil {
		t.Fatal(err)
	}
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 42, Host: "h", ClaimedAt: r.CreatedAt}
	a := &testAdapter{exec: Outcome{State: RevalidationRequired, ExitCode: 4}}
	e := NewExecutor(s, a)
	if _, err = e.Execute(context.Background(), r.Spec.ID, r.Revision, claim); err != nil {
		t.Fatal(err)
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Execute(context.Background(), r.Spec.ID, view.Request.Revision, claim)
	if err == nil || got.State != RevalidationRequired || got.ExitCode != 4 || a.execCalls != 1 {
		t.Fatalf("lost durable revalidation %+v %v calls%d", got, err, a.execCalls)
	}
}
func (a *testAdapter) Reconcile(context.Context, Request) Outcome {
	atomic.AddInt32(&a.recCalls, 1)
	return a.rec
}

func TestExecutorPublishesRunningBeforeCallbackAndTerminalIsIdempotent(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(spec("a", "ka", "one"))
	if err != nil {
		t.Fatal(err)
	}
	a := &testAdapter{exec: Outcome{State: Succeeded, Reason: "pushed", ExitCode: 0}}
	e := NewExecutor(s, a)
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 11, Host: "h", ClaimedAt: r.CreatedAt}
	out, err := e.Execute(context.Background(), r.Spec.ID, r.Revision, claim)
	if err != nil || out.State != Succeeded {
		t.Fatalf("execute=%+v err=%v", out, err)
	}
	if got := atomic.LoadInt32(&a.execCalls); got != 1 {
		t.Fatalf("execute callback calls=%d", got)
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != Succeeded {
		t.Fatalf("terminal request=%+v err=%v", view.Request, err)
	}
	if _, err = e.Execute(context.Background(), r.Spec.ID, view.Request.Revision, claim); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&a.execCalls); got != 1 {
		t.Fatalf("terminal request reran callback: %d", got)
	}
	bad := claim
	bad.Owner.Actor = "other"
	if _, err = e.Execute(context.Background(), r.Spec.ID, view.Request.Revision, bad); err == nil {
		t.Fatal("terminal request accepted foreign claim identity")
	}
	if _, err = e.Execute(context.Background(), r.Spec.ID, view.Request.Revision-1, claim); err == nil {
		t.Fatal("terminal request accepted stale revision")
	}
}

func TestExecutorCancellationBeforeRunningDoesNotExecute(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(spec("a", "ka", "one"))
	if err != nil {
		t.Fatal(err)
	}
	a := &testAdapter{exec: Outcome{State: Succeeded}}
	e := NewExecutor(s, a)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 12, Host: "h", ClaimedAt: r.CreatedAt}
	if _, err = e.Execute(ctx, r.Spec.ID, r.Revision, claim); err == nil {
		t.Fatal("cancelled admission accepted")
	}
	if got := atomic.LoadInt32(&a.execCalls); got != 0 {
		t.Fatalf("cancelled request executed: %d", got)
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != Queued {
		t.Fatalf("cancel changed queued request=%+v err=%v", view.Request, err)
	}
}

func TestExecutorExitFourPreservesRevalidationAndDoesNotRerun(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	a := &testAdapter{exec: Outcome{State: Failed, Reason: "source moved", ExitCode: 4}}
	e := NewExecutor(s, a)
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 13, Host: "h", ClaimedAt: r.CreatedAt}
	out, err := e.Execute(context.Background(), r.Spec.ID, r.Revision, claim)
	if err != nil || out.State != RevalidationRequired {
		t.Fatalf("exit4=%+v err=%v", out, err)
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != RevalidationRequired || view.Request.LastError != "source moved" {
		t.Fatalf("revalidation request=%+v err=%v", view.Request, err)
	}
	if _, err = e.Execute(context.Background(), r.Spec.ID, view.Request.Revision, claim); err == nil {
		t.Fatal("revalidation request executed without resume")
	}
	if got := atomic.LoadInt32(&a.execCalls); got != 1 {
		t.Fatalf("exit4 reran provider: %d", got)
	}
}

func TestExecutorPartialOutcomeKeepsRunningClaimAndReconciles(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 22, Host: "h", ClaimedAt: r.CreatedAt}
	a := &testAdapter{exec: Outcome{State: Running, Reason: "cleanup pending", ExitCode: 3}, rec: Outcome{State: Succeeded, Reason: "cleanup complete"}}
	e := NewExecutor(s, a, func(context.Context, Claim) ClaimLiveness { return ClaimDead })
	out, err := e.Execute(context.Background(), r.Spec.ID, r.Revision, claim)
	if err != nil || out.State != Running || out.ExitCode != 3 {
		t.Fatalf("partial execute=%+v err=%v", out, err)
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != Running || view.Request.LastExitCode != 3 || view.Request.Claim == nil {
		t.Fatalf("partial state=%+v err=%v", view.Request, err)
	}
	replacement := claim
	replacement.PID = 23
	out, err = e.Reconcile(context.Background(), r.Spec.ID, view.Request.Revision, claim, replacement)
	if err != nil || out.State != Succeeded || atomic.LoadInt32(&a.recCalls) != 1 {
		t.Fatalf("partial reconcile=%+v err=%v calls=%d", out, err, a.recCalls)
	}
}

func TestExecutorReconcileRefusesLiveAndUnknownButRecoversDead(t *testing.T) {
	for _, observed := range []ClaimLiveness{ClaimLive, ClaimUnknown} {
		t.Run(string(observed), func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r, _ := s.Enqueue(spec("a", "ka", "one"))
			claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 14, Host: "h", ClaimedAt: r.CreatedAt}
			r, err = s.Admit(r.Spec.ID, r.Revision, claim)
			if err != nil {
				t.Fatal(err)
			}
			r, err = s.Mark(r.Spec.ID, r.Revision, r.Claim, Running, "")
			if err != nil {
				t.Fatal(err)
			}
			a := &testAdapter{rec: Outcome{State: Succeeded, Reason: "cleaned"}}
			e := NewExecutor(s, a, func(context.Context, Claim) ClaimLiveness { return observed })
			replacement := claim
			replacement.PID = 15
			refused, refuseErr := e.Reconcile(context.Background(), r.Spec.ID, r.Revision, claim, replacement)
			if refuseErr == nil || refused.ExitCode != 2 || refused.State != Running {
				t.Fatal("reclaimed non-dead claim")
			}
			if got := atomic.LoadInt32(&a.recCalls); got != 0 {
				t.Fatalf("reconcile callback calls=%d", got)
			}
			view, _ := s.Get(r.Spec.ID)
			if view.Request.State != Running || view.Request.Claim.PID != claim.PID {
				t.Fatalf("claim changed on refusal=%+v", view.Request)
			}
		})
	}

	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 16, Host: "h", ClaimedAt: r.CreatedAt}
	r, _ = s.Admit(r.Spec.ID, r.Revision, claim)
	r, _ = s.Mark(r.Spec.ID, r.Revision, r.Claim, Running, "")
	a := &testAdapter{rec: Outcome{State: Succeeded, Reason: "cleaned"}}
	e := NewExecutor(s, a, func(context.Context, Claim) ClaimLiveness { return ClaimDead })
	replacement := claim
	replacement.PID = 17
	out, err := e.Reconcile(context.Background(), r.Spec.ID, r.Revision, claim, replacement)
	if err != nil || out.State != Succeeded {
		t.Fatalf("dead recovery=%+v err=%v", out, err)
	}
	if atomic.LoadInt32(&a.recCalls) != 1 {
		t.Fatal("dead recovery did not call reconcile exactly once")
	}
}

func TestExecutorAdmittedCrashRecoversByExecutingOnce(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 20, Host: "h", ClaimedAt: r.CreatedAt}
	r, err = s.Admit(r.Spec.ID, r.Revision, claim)
	if err != nil {
		t.Fatal(err)
	}
	a := &testAdapter{exec: Outcome{State: Succeeded, Reason: "completed"}}
	e := NewExecutor(s, a, func(context.Context, Claim) ClaimLiveness { return ClaimDead })
	replacement := claim
	replacement.PID = 21
	out, err := e.Reconcile(context.Background(), r.Spec.ID, r.Revision, claim, replacement)
	if err != nil || out.State != Succeeded {
		t.Fatalf("admitted recovery=%+v err=%v", out, err)
	}
	if atomic.LoadInt32(&a.execCalls) != 1 || atomic.LoadInt32(&a.recCalls) != 0 {
		t.Fatalf("calls execute=%d reconcile=%d", a.execCalls, a.recCalls)
	}
}

func TestExecutorDoesNotCallAdapterForInvalidOutcome(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	a := &testAdapter{exec: Outcome{State: State("bogus"), Reason: "invalid"}}
	e := NewExecutor(s, a)
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 19, Host: "h", ClaimedAt: r.CreatedAt}
	if _, err = e.Execute(context.Background(), r.Spec.ID, r.Revision, claim); err == nil {
		t.Fatal("invalid adapter result accepted")
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != Running || view.Request.LastExitCode != 125 {
		t.Fatalf("invalid outcome was not retained as unknown running state: %+v %v", view.Request, err)
	}
	if _, err = e.Execute(context.Background(), r.Spec.ID, view.Request.Revision-1, claim); err == nil {
		t.Fatal("stale running request accepted")
	}
	if _, err = s.Get(r.Spec.ID); err != nil {
		t.Fatal(err)
	}
}

func TestExecutorFailClosesContradictoryOutcomeStates(t *testing.T) {
	cases := []Outcome{
		{State: Succeeded, ExitCode: 3},
		{State: Succeeded, ExitCode: 2},
		{State: Running, ExitCode: 0},
		{State: Failed, ExitCode: 0},
		{State: RevalidationRequired, ExitCode: 2},
	}
	for i, adapterOutcome := range cases {
		t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r, _ := s.Enqueue(spec("a", "ka", "one"))
			claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 30 + i, Host: "h", ClaimedAt: r.CreatedAt}
			e := NewExecutor(s, &testAdapter{exec: adapterOutcome})
			out, err := e.Execute(context.Background(), r.Spec.ID, r.Revision, claim)
			if err == nil || out.State != Running || out.ExitCode != 125 {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			view, getErr := s.Get(r.Spec.ID)
			if getErr != nil || view.Request.State != Running || view.Request.LastExitCode != 125 {
				t.Fatalf("stored=%+v err=%v", view.Request, getErr)
			}
		})
	}
}

func TestExecutorReconcileCancellationDoesNotReplaceClaim(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 40, Host: "h", ClaimedAt: r.CreatedAt}
	r, _ = s.Admit(r.Spec.ID, r.Revision, claim)
	r, _ = s.Mark(r.Spec.ID, r.Revision, r.Claim, Running, "")
	a := &testAdapter{rec: Outcome{State: Succeeded}}
	e := NewExecutor(s, a, func(context.Context, Claim) ClaimLiveness { return ClaimDead })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	replacement := claim
	replacement.PID = 41
	if _, err = e.Reconcile(ctx, r.Spec.ID, r.Revision, claim, replacement); err == nil {
		t.Fatal("cancelled recovery accepted")
	}
	view, err := s.Get(r.Spec.ID)
	if err != nil || view.Request.State != Running || view.Request.Claim.PID != claim.PID || a.recCalls != 0 {
		t.Fatalf("cancelled recovery mutated=%+v err=%v calls=%d", view.Request, err, a.recCalls)
	}
}

func TestExecutorLegacyFailedTerminalOutcomeUsesNonzeroExit(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, _ := s.Enqueue(spec("a", "ka", "one"))
	claim := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 50, Host: "h", ClaimedAt: r.CreatedAt}
	r, _ = s.Admit(r.Spec.ID, r.Revision, claim)
	r, _ = s.Mark(r.Spec.ID, r.Revision, r.Claim, Running, "")
	r, _ = s.Mark(r.Spec.ID, r.Revision, r.Claim, Failed, "legacy failure")
	e := NewExecutor(s, &testAdapter{})
	out, err := e.Execute(context.Background(), r.Spec.ID, r.Revision, claim)
	if err != nil || out.State != Failed || out.ExitCode != 1 {
		t.Fatalf("legacy outcome=%+v err=%v", out, err)
	}
}

func TestExecutorDoesNotHoldQueueLockDuringAdapter(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	aSpec, _ := s.Enqueue(spec("a", "ka", "one"))
	bSpec := spec("b", "kb", "two")
	bSpec.Owner.Actor = "b"
	bSpec.Generation.Owner.Actor = "b"
	bSpec.Generation.Branch = "dev/b/h/feat/t"
	bSpec.Resource = "two"
	bRequest, err := s.Enqueue(bSpec)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &blockingAdapter{entered: make(chan struct{}), release: make(chan struct{})}
	e := NewExecutor(s, adapter)
	claimA := Claim{Owner: aSpec.Spec.Owner, Generation: aSpec.Spec.Generation, PID: 24, Host: "h", ClaimedAt: aSpec.CreatedAt}
	claimB := Claim{Owner: bRequest.Spec.Owner, Generation: bRequest.Spec.Generation, PID: 25, Host: "h", ClaimedAt: bRequest.CreatedAt}
	firstDone := make(chan error, 1)
	go func() {
		_, runErr := e.Execute(context.Background(), aSpec.Spec.ID, aSpec.Revision, claimA)
		firstDone <- runErr
	}()
	select {
	case <-adapter.entered:
	case <-time.After(time.Second):
		t.Fatal("first adapter did not start")
	}
	secondDone := make(chan error, 1)
	go func() {
		_, runErr := e.Execute(context.Background(), bRequest.Spec.ID, bRequest.Revision, claimB)
		secondDone <- runErr
	}()
	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("independent resource blocked: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queue lock held during adapter callback")
	}
	close(adapter.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}
