package integrationqueue

import (
	"context"
	"errors"
	"fmt"
)

// Adapter is the boundary between queue coordination and the integration
// runtime. Implementations may invoke the provider, but the queue never does.
// Reconcile is cleanup/recovery only: it must not start a new integration.
type Adapter interface {
	Execute(context.Context, Request) Outcome
	Reconcile(context.Context, Request) Outcome
}

// Liveness observes a claim outside the queue lock. Unknown is deliberately
// different from dead: an uncertain owner must never be reclaimed.
type Liveness func(context.Context, Claim) ClaimLiveness

// Outcome is the durable result returned by an Adapter. ExitCode is retained
// even when State is supplied so provider exit 4 can remain observable.
type Outcome struct {
	State    State  `json:"state"`
	Reason   string `json:"reason"`
	ExitCode int    `json:"exitCode"`
}

// Executor coordinates durable request transitions. Store methods hold the
// queue lock only while publishing a state change; adapter calls happen after
// the lock has been released.
type Executor struct {
	store    *Store
	adapter  Adapter
	liveness Liveness
}

// NewExecutor constructs a queue executor. A missing liveness observer is
// fail-closed: recovery cannot reclaim a claim without positive observation.
func NewExecutor(store *Store, adapter Adapter, liveness ...Liveness) *Executor {
	var observe Liveness
	if len(liveness) != 0 {
		observe = liveness[0]
	}
	return &Executor{store: store, adapter: adapter, liveness: observe}
}

// Execute admits a queued request, publishes Running, and invokes Execute
// exactly once. The caller supplies the claim created by its runtime owner;
// this keeps PID/host ownership outside this repository-independent package.
func (e *Executor) Execute(ctx context.Context, id string, rev uint64, claim Claim) (Outcome, error) {
	if e == nil || e.store == nil || e.adapter == nil {
		return Outcome{}, errors.New("integration executor is incomplete")
	}
	if err := ctx.Err(); err != nil {
		return Outcome{}, fmt.Errorf("execute cancelled before admission: %w", err)
	}
	r, err := e.store.Get(id)
	if err != nil {
		return Outcome{}, err
	}
	if r.Request.Revision != rev || !claimIdentityMatches(claim, r.Request.Spec) {
		return Outcome{}, errors.New("request revision or claim identity does not match")
	}
	if terminal(r.Request.State) {
		return requestOutcome(r.Request), nil
	}
	if r.Request.State == RevalidationRequired {
		return Outcome{State: RevalidationRequired, Reason: r.Request.LastError, ExitCode: 4}, errors.New("request requires revalidation before execute")
	}
	if r.Request.State == Queued {
		r.Request, err = e.store.Admit(id, rev, claim)
		if err != nil {
			return Outcome{}, err
		}
	} else if r.Request.State != Admitted || r.Request.Claim == nil || !sameClaim(*r.Request.Claim, claim) || r.Request.Revision != rev {
		return Outcome{}, errors.New("request is not admitted by the supplied claim and revision")
	}
	if err := ctx.Err(); err != nil {
		// Cancellation at the admitted boundary is safe to publish as a
		// terminal cancellation. If another worker won the CAS, leave its
		// running state visible and return the original cancellation.
		if cancelled, cancelErr := e.store.Cancel(id, claim.Owner, r.Request.Revision); cancelErr == nil {
			return Outcome{State: cancelled.State, Reason: err.Error(), ExitCode: 125}, fmt.Errorf("execute cancelled before running: %w", err)
		}
		if current, getErr := e.store.Get(id); getErr == nil {
			return requestOutcome(current.Request), fmt.Errorf("execute cancelled before running: %w", err)
		}
		return Outcome{}, fmt.Errorf("execute cancelled before running: %w", err)
	}
	r.Request, err = e.store.Mark(id, r.Request.Revision, r.Request.Claim, Running, "")
	if err != nil {
		return Outcome{}, err
	}
	out := normalizeOutcome(e.adapter.Execute(ctx, r.Request))
	return e.publish(id, r.Request.Revision, r.Request.Claim, out)
}

// Reconcile reclaims a dead Running owner and invokes the adapter's
// cleanup/recovery operation. Live and unknown owners are preserved and never
// invoke the adapter. replacement must retain the request generation.
func (e *Executor) Reconcile(ctx context.Context, id string, rev uint64, expected, replacement Claim) (Outcome, error) {
	if e == nil || e.store == nil || e.adapter == nil {
		return Outcome{}, errors.New("integration executor is incomplete")
	}
	r, err := e.store.Get(id)
	if err != nil {
		return Outcome{}, err
	}
	if r.Request.Revision != rev || !claimIdentityMatches(expected, r.Request.Spec) || !claimIdentityMatches(replacement, r.Request.Spec) {
		return Outcome{}, errors.New("request revision or claim identity does not match")
	}
	if terminal(r.Request.State) {
		return requestOutcome(r.Request), nil
	}
	if r.Request.State != Running && r.Request.State != Admitted {
		return Outcome{}, errors.New("request is not recoverable")
	}
	if r.Request.Claim == nil || !sameClaim(*r.Request.Claim, expected) || r.Request.Revision != rev || !claimIdentityMatches(expected, r.Request.Spec) || !claimIdentityMatches(replacement, r.Request.Spec) {
		return Outcome{}, errors.New("running request claim or revision does not match")
	}
	if e.liveness == nil {
		return Outcome{State: Running, Reason: "claim liveness is unknown", ExitCode: 2}, errors.New("claim liveness observer is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return Outcome{State: Running, Reason: "recovery cancelled before claim replacement", ExitCode: 125}, fmt.Errorf("reconcile cancelled before recovery: %w", err)
	}
	observation := e.liveness(ctx, expected)
	if observation != ClaimDead {
		reason := "claim owner is live"
		if observation == ClaimUnknown {
			reason = "claim owner liveness is unknown"
		}
		return Outcome{State: Running, Reason: reason, ExitCode: 2}, fmt.Errorf("refuse running claim recovery: %s", reason)
	}
	if r.Request.State == Admitted {
		r.Request, err = e.store.RecoverAdmitted(id, r.Request.Spec.Owner, rev, expected, replacement, ClaimDead)
	} else {
		r.Request, err = e.store.RecoverRunning(id, r.Request.Spec.Owner, rev, expected, replacement, ClaimDead)
	}
	if err != nil {
		return Outcome{}, err
	}
	var out Outcome
	if r.Request.State == Admitted {
		r.Request, err = e.store.Mark(id, r.Request.Revision, r.Request.Claim, Running, "")
		if err != nil {
			return Outcome{}, err
		}
		out = normalizeOutcome(e.adapter.Execute(ctx, r.Request))
	} else {
		out = normalizeOutcome(e.adapter.Reconcile(ctx, r.Request))
	}
	return e.publish(id, r.Request.Revision, r.Request.Claim, out)
}

func (e *Executor) publish(id string, rev uint64, claim *Claim, out Outcome) (Outcome, error) {
	if !validExecutionOutcome(out.State) {
		out = Outcome{State: Running, Reason: "adapter returned an invalid or incomplete outcome; result is unknown", ExitCode: 125}
		_, markErr := e.store.Mark(id, rev, claim, Running, out.Reason, out.ExitCode)
		if markErr != nil {
			return out, markErr
		}
		return out, errors.New("adapter returned invalid outcome")
	}
	_, err := e.store.Mark(id, rev, claim, out.State, out.Reason, out.ExitCode)
	if err != nil {
		return out, fmt.Errorf("observed integration outcome was not published: %w", err)
	}
	return out, nil
}

func normalizeOutcome(out Outcome) Outcome {
	if out.ExitCode == 4 {
		out.State = RevalidationRequired
	}
	valid := out.ExitCode >= 0
	switch out.State {
	case Succeeded:
		valid = valid && out.ExitCode == 0
	case Running, Failed:
		valid = valid && out.ExitCode != 0
	case RevalidationRequired:
		valid = valid && out.ExitCode == 4
	default:
		valid = false
	}
	if !valid {
		return Outcome{State: State("invalid"), Reason: "adapter returned an inconsistent outcome; result is unknown", ExitCode: 125}
	}
	return out
}

func validExecutionOutcome(s State) bool {
	return s == Running || s == Succeeded || s == Failed || s == RevalidationRequired
}

func terminal(s State) bool { return s == Succeeded || s == Failed || s == Cancelled }

func requestOutcome(r Request) Outcome {
	exitCode := r.LastExitCode
	if r.State == Failed && exitCode == 0 {
		exitCode = 1
	}
	if r.State == RevalidationRequired {
		exitCode = 4
	}
	if exitCode == 0 && (r.State == Running || r.State == Admitted || r.State == Queued) {
		exitCode = 2
	}
	return Outcome{State: r.State, Reason: r.LastError, ExitCode: exitCode}
}

func claimIdentityMatches(c Claim, s Spec) bool {
	return c.Owner == s.Owner && sameGeneration(c.Generation, s.Generation)
}
