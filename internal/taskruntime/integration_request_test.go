package taskruntime

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
)

func requestForWorld(w *endWorld) integrationqueue.Request {
	e := w.execution
	o := integrationqueue.Owner{Actor: e.Owner.Actor, Host: e.Owner.Host, Kind: e.Owner.Kind}
	return integrationqueue.Request{Spec: integrationqueue.Spec{ID: "request-one", IdempotencyKey: "one", RepositoryID: "sample-repo", RepositoryPath: w.root, CommonDir: w.common, ExpectedTaskSHA: oidTask, ExpectedSourceSHA: oidBase, Source: e.Source, Target: "origin/" + e.Source, GateProfile: "repository-default", Resource: "resource", Owner: o,
		Generation: integrationqueue.Generation{Task: e.Task, Type: e.Type, Purpose: "delivery", Source: e.Source, Owner: o, Branch: e.Branch, Worktree: e.Worktree, StartedAt: e.StartedAt}}}
}

type requestTestProvider struct{ *fakeProvider }

func (p requestTestProvider) DiagnoseRequest(context.Context, string) ([]CommandDiagnostic, error) {
	return nil, nil
}
func (p requestTestProvider) RunBound(ctx context.Context, dir, target string, noFetch bool, _, _ string, _ *requestBinding) (CommandResult, error) {
	return p.Run(ctx, dir, target, noFetch)
}

func TestRequestLateReceiptCASCannotBecomeKnownRefusal(t *testing.T) {
	w := newEndWorld(t)
	svc := w.service()
	svc.provider = requestTestProvider{w.provider}
	w.provider.onRun = func() {
		store := newStateStore(w.common)
		latest, err := store.latestReceipts()
		if err != nil {
			t.Fatal(err)
		}
		changed := latest[w.execution.Task]
		changed.RequestID = "foreign-request"
		if err := store.appendReceipt(changed); err != nil {
			t.Fatal(err)
		}
	}
	out := svc.Execute(context.Background(), requestForWorld(w))
	if out.State != integrationqueue.Running || out.ExitCode != 3 || w.provider.runs != 1 {
		t.Fatalf("lost provider result misclassified %+v calls%d", out, w.provider.runs)
	}
}

func TestRequestBoundProviderArgumentsAndOwner(t *testing.T) {
	for _, kind := range []string{"agent", "human"} {
		t.Run(kind, func(t *testing.T) {
			w := newEndWorld(t)
			r := requestForWorld(w)
			r.Spec.Owner.Kind = kind
			b := newRequestBinding(r.Spec)
			runner := &recordRunner{}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err := (gzGitProvider{runner: runner}).RunBound(ctx, w.worktree, r.Spec.Target, false, oidTask, oidBase, &b)
			if err != nil || len(runner.reqs) != 1 {
				t.Fatalf("run %v %+v", err, runner.reqs)
			}
			req := runner.reqs[0]
			args := strings.Join(req.Args, " ")
			for _, want := range []string{"--expect-source " + oidTask, "--expect-target " + oidBase, "--execution-id " + b.executionID, "--generation " + b.generation, "--owner-kind " + kind} {
				if !strings.Contains(args, want) {
					t.Fatalf("missing %s in %s", want, args)
				}
			}
			if req.ProcessGroup || req.Env["GIT_WORK_HOST"] != "testhost" {
				t.Fatalf("request %+v", req)
			}
			if kind == "agent" && (req.Env["GZ_GIT_TASK_ACTOR"] != "tester" || req.Env["GIT_WORK_ACTOR"] != "") {
				t.Fatal(req.Env)
			}
			if kind == "human" && (req.Env["GIT_WORK_ACTOR"] != "tester" || req.Env["GZ_GIT_TASK_ACTOR"] != "") {
				t.Fatal(req.Env)
			}
		})
	}
}

func TestRequestCapabilityFailsBeforeRunOnMissingFlag(t *testing.T) {
	var calls []string
	p := gzGitProvider{runner: scriptRunner{fn: func(_ context.Context, r CommandRequest) (CommandResult, error) {
		calls = append(calls, strings.Join(r.Args, " "))
		return CommandResult{Command: r.Command, Args: r.Args, Stdout: "Usage:\n  gz-git integrate run [flags]\n\nFlags:\n  --expect-source string\n"}, nil
	}}}
	diag, err := p.DiagnoseRequest(context.Background(), "/synthetic")
	if err == nil || !strings.Contains(err.Error(), "--expect-target") || len(diag) != 1 || len(calls) != 1 || calls[0] != "integrate run --help" {
		t.Fatalf("probe %v %v %+v", err, calls, diag)
	}
}

func TestRequestBoundRevalidationCannotBypassWithBareFinish(t *testing.T) {
	w := newEndWorld(t)
	svc := w.service()
	r := requestForWorld(w)
	b := newRequestBinding(r.Spec)
	prior := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusBlocked, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: time.Now(), TaskHead: oidTask, SourceHeadBefore: oidBase, RevalidationRequired: true}
	bindRequestReceipt(&prior, &b)
	if err := newStateStore(w.common).appendReceipt(prior); err != nil {
		t.Fatal(err)
	}
	res, code := svc.Finish(context.Background(), w.execution.Task)
	if code == 0 || w.provider.runs != 0 || !strings.Contains(res.Reason, "request-bound") {
		t.Fatalf("bypass %+v %d calls%d", res, code, w.provider.runs)
	}
}

func TestRequestLocalDriftPersistsBindingBeforeBareFinish(t *testing.T) {
	w := newEndWorld(t)
	svc := w.service()
	r := requestForWorld(w)
	r.Spec.ExpectedTaskSHA = oidMoved
	out := svc.Execute(context.Background(), r)
	if out.State != integrationqueue.RevalidationRequired || out.ExitCode != 4 {
		t.Fatal(out)
	}
	latest, err := newStateStore(w.common).latestReceipts()
	if err != nil {
		t.Fatal(err)
	}
	if !requestReceiptMatches(latest[w.execution.Task], newRequestBinding(r.Spec)) || !latest[w.execution.Task].RevalidationRequired {
		t.Fatal("drift lost exact binding")
	}
	resumed := svc.Reconcile(context.Background(), r)
	if resumed.State != integrationqueue.RevalidationRequired || resumed.ExitCode != 4 || w.provider.runs != 0 {
		t.Fatalf("lost durable pre-mutation result %+v", resumed)
	}
	res, code := svc.Finish(context.Background(), w.execution.Task)
	if code == 0 || w.provider.runs != 0 || !strings.Contains(res.Reason, "request-bound") {
		t.Fatalf("bare bypass %+v %d runs%d", res, code, w.provider.runs)
	}
}

func TestRequestStatusAndNewApprovalAfterRevalidation(t *testing.T) {
	for _, revalidation := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "revalidation"}[revalidation], func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			r := requestForWorld(w)
			b := newRequestBinding(r.Spec)
			prior := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusBlocked, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: time.Now(), TaskHead: oidTask, SourceHeadBefore: oidBase, RevalidationRequired: revalidation}
			bindRequestReceipt(&prior, &b)
			store := newStateStore(w.common)
			if err := store.appendReceipt(prior); err != nil {
				t.Fatal(err)
			}
			status := svc.Status(context.Background(), w.execution.Task)
			if status.Status != StatusBlocked || status.FinishReady || strings.Join(status.AllowedActions, ",") != "run-status" {
				t.Fatalf("false ready %+v", status)
			}
			if !strings.Contains(status.NextAction, map[bool]string{false: "resume original", true: "newly approved"}[revalidation]) {
				t.Fatal("wrong next action", status.NextAction)
			}
			svc.provider = requestTestProvider{w.provider}
			w.provider.runCode = 4
			r.Spec.ID = "new-approved-request"
			out := svc.Execute(context.Background(), r)
			if revalidation {
				if w.provider.runs != 1 || out.ExitCode != 4 {
					t.Fatalf("new approval wrongly requires old cancel %+v calls%d", out, w.provider.runs)
				}
			} else if w.provider.runs != 0 {
				t.Fatal("unknown outcome superseded")
			}
		})
	}
}

func TestRequestDispositionAndCancelledRefusalRelease(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "revalidation-cancel"}[cancelled], func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			r := requestForWorld(w)
			b := newRequestBinding(r.Spec)
			prior := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusBlocked, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: time.Now(), TaskHead: oidTask, SourceHeadBefore: oidBase, RevalidationRequired: cancelled}
			bindRequestReceipt(&prior, &b)
			store := newStateStore(w.common)
			if err := store.appendReceipt(prior); err != nil {
				t.Fatal(err)
			}
			before := w.receipts()
			if res, code := svc.Abort(context.Background(), w.execution.Task, "synthetic"); code == 0 || !strings.Contains(res.Reason, "request-bound") {
				t.Fatalf("abort %+v code%d", res, code)
			}
			if res, code := svc.Discard(context.Background(), w.execution.Task, "synthetic", ""); code == 0 || !strings.Contains(res.Reason, "request-bound") {
				t.Fatalf("discard %+v code%d", res, code)
			}
			if w.receipts() != before {
				t.Fatal("request guard overwrote receipt")
			}
			r.State = integrationqueue.Cancelled
			err := svc.CancelRequest(context.Background(), r)
			if !cancelled {
				if err == nil || w.receipts() != before {
					t.Fatal("cancel released uncertain outcome")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after := w.receipts()
			if err = svc.CancelRequest(context.Background(), r); err != nil || w.receipts() != after {
				t.Fatal("cancel disposition retry changed evidence", err)
			}
			res, code := svc.Abort(context.Background(), w.execution.Task, "cancelled request")
			if code != 0 || res.Receipt == nil || res.Receipt.ExecutionStartedAt == nil || !res.Receipt.ExecutionStartedAt.Equal(w.execution.StartedAt) {
				t.Fatalf("safe abort %+v code%d", res, code)
			}
		})
	}
}

func TestRequestTerminalGenerationCannotBindReusedTask(t *testing.T) {
	w := newEndWorld(t)
	r := discardReceipt(w.execution, StatusAborted, "synthetic", nil, nil, "discard", time.Now())
	if !receiptBindsExecution(w.execution, r) {
		t.Fatal("current generation not bound")
	}
	reused := w.execution
	reused.StartedAt = reused.StartedAt.Add(time.Second)
	if receiptBindsExecution(reused, r) {
		t.Fatal("terminal receipt bound a different execution generation")
	}
	done := r
	done.Operation = "finish"
	done.Status = StatusDone
	done.SourceHeadBefore = oidBase
	done.TaskHead = oidTask
	done.SourceHead = oidTask
	done.SourcePushed = true
	done.WorktreeRemoved = true
	done.LocalBranchRemoved = true
	done.RemoteBranchRemoved = true
	if err := validateDoneReceipt(w.execution.Task, &w.execution, &done); err != nil {
		t.Fatal("valid completion", err)
	}
	if err := validateDoneReceipt(reused.Task, &reused, &done); err == nil {
		t.Fatal("DONE validator accepted reused generation")
	}
	r.ExecutionStartedAt = nil
	if receiptBindsExecution(w.execution, r) {
		t.Fatal("partial generation accepted as legacy")
	}
}

func TestRequestBindingRefusesDriftBeforeProvider(t *testing.T) {
	for _, change := range []string{"task-sha", "source-sha", "generation", "purpose", "target", "profile", "owner", "common"} {
		t.Run(change, func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			r := requestForWorld(w)
			switch change {
			case "task-sha":
				r.Spec.ExpectedTaskSHA = oidMoved
			case "source-sha":
				r.Spec.ExpectedSourceSHA = oidMoved
			case "generation":
				r.Spec.Generation.StartedAt = r.Spec.Generation.StartedAt.Add(time.Second)
			case "purpose":
				r.Spec.Generation.Purpose = "review"
			case "target":
				r.Spec.Target = "origin/other"
			case "profile":
				r.Spec.GateProfile = "unknown"
			case "owner":
				r.Spec.Generation.Owner.Actor = "other"
			case "common":
				r.Spec.CommonDir = w.root
			}
			out := svc.Execute(context.Background(), r)
			wantState, wantCode := integrationqueue.Failed, 2
			if change == "task-sha" || change == "source-sha" {
				wantState, wantCode = integrationqueue.RevalidationRequired, 4
			}
			if out.State != wantState || out.ExitCode != wantCode || w.provider.runs != 0 || w.provider.checks != 0 {
				t.Fatalf("out %+v calls %d/%d", out, w.provider.runs, w.provider.checks)
			}
		})
	}
}

func TestRequestReconcileNativeIdentityAndCleanupOnly(t *testing.T) {
	for _, change := range []string{"", "generation", "owner", "target", "task", "source", "unpushed", "missing", "partial", "runtime-generation", "live", "unknown"} {
		t.Run(change, func(t *testing.T) {
			w := newEndWorld(t)
			svc := w.service()
			svc.now = time.Now
			r := requestForWorld(w)
			b := newRequestBinding(r.Spec)
			prior := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusBlocked, Owner: w.execution.Owner, Branch: w.branch, Worktree: w.worktree, CreatedAt: time.Now(), TaskHead: oidTask, SourceHeadBefore: oidBase, BaseHead: oidBase}
			bindRequestReceipt(&prior, &b)
			if change == "runtime-generation" {
				started := w.execution.StartedAt.Add(time.Second)
				prior.ExecutionStartedAt = &started
			}
			if change == "live" || change == "unknown" {
				prior.Status = StatusFinishing
				prior.FinishPID = os.Getpid()
				prior.FinishHost = lockHost()
				if change == "unknown" {
					prior.FinishHost = "foreign"
				}
			}
			store := newStateStore(w.common)
			if err := store.appendReceipt(prior); err != nil {
				t.Fatal(err)
			}
			native := nativeRequestReceipt{Schema: 2, ExecutionID: b.executionID, Generation: b.generation, Owner: w.execution.Owner, Common: w.common, Branch: w.branch, Worktree: w.worktree, TaskSHA: oidTask, Target: "refs/heads/master", TargetBefore: oidBase, SourcePushed: true, Phase: "pushed"}
			switch change {
			case "generation":
				native.Generation = "other"
			case "owner":
				native.Owner.Actor = "other"
			case "target":
				native.Target = "origin/master"
			case "task":
				native.TaskSHA = oidMoved
			case "source":
				native.TargetBefore = oidMoved
			case "unpushed":
				native.SourcePushed = false
				native.Phase = "intent"
			}
			queries, cleanups, runs := 0, 0, 0
			svc.runner = scriptRunner{fn: func(ctx context.Context, req CommandRequest) (CommandResult, error) {
				result := CommandResult{Command: req.Command, Args: req.Args, WorkDir: req.WorkDir}
				if req.Command == providerGZGit {
					switch req.Args[1] {
					case "receipt":
						queries++
						data, _ := json.Marshal(native)
						result.Stdout = string(data)
						if change == "missing" {
							result.Stdout = "null"
							result.ExitCode = 2
						}
					case "cleanup":
						cleanups++
						if req.Env["GZ_GIT_TASK_ACTOR"] != "tester" || req.Env["GIT_WORK_ACTOR"] != "" {
							t.Fatal("cleanup owner not bound", req.Env)
						}
						if change == "partial" {
							result.ExitCode = 3
							result.Stderr = "remote denied"
						} else {
							native.SourcePushed = true
							native.Phase = "complete"
							native.WorktreeRemoved = true
							native.LocalRemoved = true
							native.RemoteRemoved = true
							if err := os.Remove(w.worktree); err != nil {
								t.Fatal(err)
							}
						}
					case "run":
						runs++
						t.Fatal("reconcile repeated integration")
					}
					return result, nil
				}
				if req.Command == "git" && len(req.Args) > 0 && req.Args[0] == "fetch" {
					return result, nil
				}
				if req.Command == "git" && sameArgs(req.Args, "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/master") {
					result.Stdout = oidTask + "\trefs/heads/master\n"
					return result, nil
				}
				if req.Command == "git" && sameArgs(req.Args, "worktree", "list", "--porcelain", "-z") {
					result.Stdout = "worktree " + w.root + "\x00\x00"
					return result, nil
				}
				if req.Command == "git" && sameArgs(req.Args, "rev-parse", "--verify", "FETCH_HEAD^{commit}") {
					result.Stdout = oidTask
					return result, nil
				}
				return w.run(ctx, req)
			}}
			out := svc.Reconcile(context.Background(), r)
			if runs != 0 {
				t.Fatal("provider rerun")
			}
			if change == "" || change == "unpushed" {
				if out.State != integrationqueue.Succeeded || out.ExitCode != 0 || queries != 2 || cleanups != 1 {
					t.Fatalf("out %+v query%d cleanup%d", out, queries, cleanups)
				}
				latest, err := store.latestReceipts()
				if err != nil {
					t.Fatal(err)
				}
				if latest[w.execution.Task].SourceHead != oidTask {
					t.Fatal("fresh fetched source identity lost")
				}
			} else {
				if out.State != integrationqueue.Running || out.ExitCode == 0 {
					t.Fatalf("out %+v", out)
				}
				if change != "partial" && cleanups != 0 {
					t.Fatal("mismatched receipt permitted cleanup")
				}
			}
			if change == "partial" {
				latest, err := store.latestReceipts()
				if err != nil {
					t.Fatal(err)
				}
				got := latest[w.execution.Task]
				if !requestReceiptMatches(got, b) || len(got.Diagnostics) < 2 || got.Diagnostics[len(got.Diagnostics)-1].Stderr != "remote denied" {
					t.Fatalf("lost actual diagnostics %+v", got)
				}
			}
		})
	}
}
