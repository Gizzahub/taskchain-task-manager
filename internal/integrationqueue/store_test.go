package integrationqueue

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func spec(id, key, res string) Spec {
	o := Owner{"a", "h", "agent"}
	return Spec{ID: id, IdempotencyKey: key, Resource: res, RepositoryID: "r", RepositoryPath: "/repo", CommonDir: "/common", ExpectedTaskSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ExpectedSourceSHA: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Source: "main", Target: "main", GateProfile: "gate", Owner: o, Generation: Generation{Task: "T", Type: "feat", Purpose: "delivery", Source: "main", Branch: "dev/a/h/feat/t", Worktree: "/work", Owner: o, StartedAt: time.Unix(1, 0)}}
}

func TestIntegrationQueueSubprocessHelper(t *testing.T) {
	if os.Getenv("INTEGRATION_QUEUE_HELPER") != "1" {
		return
	}
	root := os.Getenv("INTEGRATION_QUEUE_ROOT")
	if err := os.WriteFile(filepath.Join(root, ".ready-"+os.Getenv("INTEGRATION_QUEUE_ID")), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, ".start")); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("parent did not release concurrent workers")
		}
		time.Sleep(time.Millisecond)
	}
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Enqueue(spec(os.Getenv("INTEGRATION_QUEUE_ID"), os.Getenv("INTEGRATION_QUEUE_KEY"), "sub"))
	if err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSubprocessNewAndEnqueue(t *testing.T) {
	root := t.TempDir()
	cmds := make([]*exec.Cmd, 8)
	t.Cleanup(func() {
		for _, c := range cmds {
			if c != nil && c.Process != nil && c.ProcessState == nil {
				if err := c.Process.Kill(); err != nil && err != os.ErrProcessDone {
					t.Errorf("stop owned helper: %v", err)
				}
				_, _ = c.Process.Wait()
			}
		}
	})
	for i := range cmds {
		c := exec.Command(os.Args[0], "-test.run=TestIntegrationQueueSubprocessHelper", "-test.v")
		c.Env = append(os.Environ(), "INTEGRATION_QUEUE_HELPER=1", "INTEGRATION_QUEUE_ROOT="+root, fmt.Sprintf("INTEGRATION_QUEUE_ID=%d", i), fmt.Sprintf("INTEGRATION_QUEUE_KEY=k%d", i))
		cmds[i] = c
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ready := 0
		for i := range cmds {
			if _, err := os.Stat(filepath.Join(root, fmt.Sprintf(".ready-%d", i))); err == nil {
				ready++
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
		}
		if ready == len(cmds) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d workers reached start barrier", ready, len(cmds))
		}
		time.Sleep(time.Millisecond)
	}
	if err := os.WriteFile(filepath.Join(root, ".start"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range cmds {
		if err := c.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.List()
	if err != nil || len(v) != len(cmds) {
		t.Fatalf("views=%d err=%v", len(v), err)
	}
}
func TestEnqueueFIFOAndDedup(t *testing.T) {
	s, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Enqueue(spec("a", "ka", "one"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Enqueue(spec("b", "kb", "one"))
	if e != nil {
		t.Fatal(e)
	}
	if a.Sequence != 1 || b.Sequence != 2 {
		t.Fatal(a, b)
	}
	again, e := s.Enqueue(spec("other", "ka", "one"))
	if e != nil || again.Spec.ID != "a" {
		t.Fatalf("%+v %v", again, e)
	}
	v, e := s.List()
	if e != nil || *v[0].CoordinatorPosition != 1 || *v[1].CoordinatorPosition != 2 {
		t.Fatalf("%+v %v", v, e)
	}
	_, e = s.Enqueue(spec("a", "different", "one"))
	if e == nil {
		t.Fatal("accepted changed id")
	}
}
func TestResourceIndependenceCancelAndStateErrors(t *testing.T) {
	root := t.TempDir()
	s, e := New(root)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Enqueue(spec("a", "ka", "one"))
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Enqueue(spec("b", "kb", "two"))
	if e != nil {
		t.Fatal(e)
	}
	v, e := s.Get(b.Spec.ID)
	if e != nil || *v.CoordinatorPosition != 1 {
		t.Fatal(v, e)
	}
	c, e := s.Cancel(a.Spec.ID, a.Spec.Owner, a.Revision)
	if e != nil || c.State != Cancelled {
		t.Fatal(c, e)
	}
	if _, e = s.Get("none"); e == nil {
		t.Fatal("missing accepted")
	}
	if e = os.WriteFile(filepath.Join(root, "integration-requests.json"), []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = s.List(); e == nil {
		t.Fatal("corruption accepted")
	}
}

func TestAdmissionFIFOTransitionsAndRecovery(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Enqueue(spec("a", "a", "same"))
	b, _ := s.Enqueue(spec("b", "b", "same"))
	claim := Claim{Owner: a.Spec.Owner, Generation: a.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Now().UTC()}
	if _, err := s.Admit(b.Spec.ID, b.Revision, Claim{Owner: b.Spec.Owner, Generation: b.Spec.Generation, PID: 2, Host: "h", ClaimedAt: time.Unix(100, 0)}); err == nil {
		t.Fatal("later request admitted before FIFO head")
	}
	a, err = s.Admit(a.Spec.ID, a.Revision, claim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Admit(b.Spec.ID, b.Revision, Claim{Owner: b.Spec.Owner, Generation: b.Spec.Generation, PID: 2, Host: "h", ClaimedAt: time.Unix(100, 0)}); err == nil {
		t.Fatal("active resource admitted twice")
	}
	if _, err := s.RecoverAdmitted(a.Spec.ID, a.Spec.Owner, a.Revision, *a.Claim, claim, ClaimLive); err == nil {
		t.Fatal("live claim recovered")
	}
	if _, err := s.RecoverAdmitted(a.Spec.ID, a.Spec.Owner, a.Revision, *a.Claim, claim, ClaimUnknown); err == nil {
		t.Fatal("unknown claim recovered")
	}
	if _, err := s.Mark(a.Spec.ID, a.Revision, nil, Succeeded, ""); err == nil {
		t.Fatal("missing claim CAS accepted")
	}
	if _, err := s.Mark(a.Spec.ID, a.Revision, a.Claim, Queued, ""); err == nil {
		t.Fatal("invalid transition accepted")
	}
	dead := claim
	dead.PID = 9
	a, err = s.RecoverAdmitted(a.Spec.ID, a.Spec.Owner, a.Revision, *a.Claim, dead, ClaimDead)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.Mark(a.Spec.ID, a.Revision, a.Claim, Failed, "failed")
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Admit(b.Spec.ID, b.Revision, Claim{Owner: b.Spec.Owner, Generation: b.Spec.Generation, PID: 2, Host: "h", ClaimedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMissingInitializedStateAndPublicQuerySchema(t *testing.T) {
	root := t.TempDir()
	s, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, "integration-requests.json")); err != nil {
		t.Fatal(err)
	}
	if _, err = New(root); err == nil {
		t.Fatal("recreated erased initialized state")
	}
	root = t.TempDir()
	s, err = New(root)
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(spec("a", "a", "r"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(r.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["providerPosition"] != "UNKNOWN" || got["providerWaitReason"] != "UNKNOWN" || got["coordinatorPosition"] == nil {
		t.Fatalf("query schema=%s", b)
	}
}

func TestOpenDoesNotCreateMissingQueue(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	if _, err := Open(root); err == nil {
		t.Fatal("opened missing queue")
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("open created root")
	}
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Open(s.root); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRevalidationExcludesActiveResourceAndPreservesSequence(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.Enqueue(spec("a", "a", "r"))
	b, _ := s.Enqueue(spec("b", "b", "r"))
	ca := Claim{Owner: a.Spec.Owner, Generation: a.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Now().UTC()}
	a, err = s.Admit(a.Spec.ID, a.Revision, ca)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.Mark(a.Spec.ID, a.Revision, a.Claim, RevalidationRequired, "stale")
	if err != nil {
		t.Fatal(err)
	}
	cb := Claim{Owner: b.Spec.Owner, Generation: b.Spec.Generation, PID: 2, Host: "h", ClaimedAt: time.Now().UTC()}
	b, err = s.Admit(b.Spec.ID, b.Revision, cb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResumeRevalidation(a.Spec.ID, a.Spec.Owner, a.Revision, *a.Claim); err == nil {
		t.Fatal("resumed beside active request")
	}
	b, err = s.Mark(b.Spec.ID, b.Revision, b.Claim, Failed, "failed")
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.ResumeRevalidation(a.Spec.ID, a.Spec.Owner, a.Revision, *a.Claim)
	if err != nil || a.Sequence != 1 || a.State != Admitted {
		t.Fatal(a, err)
	}
}

func TestNewRepairsMissingMarkerForPublishedState(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(s.markerPath); err != nil {
		t.Fatal(err)
	}
	if _, err = New(s.root); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(s.root); err != nil {
		t.Fatal(err)
	}
}

func TestCrossedIDKeyCollisionRefusesBothInsertionOrders(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			xs := []Spec{spec("a", "ka", "r"), spec("b", "kb", "r")}
			if reverse {
				xs[0], xs[1] = xs[1], xs[0]
			}
			for _, x := range xs {
				if _, err := s.Enqueue(x); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Enqueue(spec("b", "ka", "r")); err == nil {
				t.Fatal("crossed ID/key collision accepted")
			}
			views, err := s.List()
			if err != nil || len(views) != 2 {
				t.Fatalf("collision changed requests: %+v %v", views, err)
			}
		})
	}
}

func TestFailedOutcomeAndActiveWaitReason(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.Enqueue(spec("a", "ka", "r"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Enqueue(spec("b", "kb", "r"))
	if err != nil {
		t.Fatal(err)
	}
	c := Claim{Owner: a.Spec.Owner, Generation: a.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Unix(100, 0)}
	a, err = s.Admit(a.Spec.ID, a.Revision, c)
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(b.Spec.ID)
	if err != nil || v.WaitReason != "waiting for active local request" || v.CoordinatorPosition == nil || *v.CoordinatorPosition != 1 {
		t.Fatalf("unreported active blocker: %+v %v", v, err)
	}
	a, err = s.Mark(a.Spec.ID, a.Revision, a.Claim, Failed, "provider failed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(a.Spec.ID, a.Spec.Owner, a.Revision); err == nil {
		t.Fatal("terminal failed outcome overwritten")
	}
	v, err = s.Get(a.Spec.ID)
	if err != nil || v.Request.State != Failed || v.Request.LastError != "provider failed" {
		t.Fatalf("terminal changed: %+v %v", v, err)
	}
}

func TestTerminalViewsNeverWaitForActivePeer(t *testing.T) {
	for _, state := range []State{Succeeded, Failed, Cancelled} {
		t.Run(string(state), func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.Enqueue(spec("a", "ka", "r"))
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Enqueue(spec("b", "kb", "r"))
			if err != nil {
				t.Fatal(err)
			}
			ca := Claim{Owner: a.Spec.Owner, Generation: a.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Unix(100, 0)}
			a, err = s.Admit(a.Spec.ID, a.Revision, ca)
			if err != nil {
				t.Fatal(err)
			}
			if state == Succeeded {
				a, err = s.Mark(a.Spec.ID, a.Revision, a.Claim, Running, "")
				if err != nil {
					t.Fatal(err)
				}
			}
			a, err = s.Mark(a.Spec.ID, a.Revision, a.Claim, state, "")
			if err != nil {
				t.Fatal(err)
			}
			cb := Claim{Owner: b.Spec.Owner, Generation: b.Spec.Generation, PID: 2, Host: "h", ClaimedAt: time.Unix(100, 0)}
			if _, err = s.Admit(b.Spec.ID, b.Revision, cb); err != nil {
				t.Fatal(err)
			}
			v, err := s.Get(a.Spec.ID)
			if err != nil || v.WaitReason != "" || v.CoordinatorPosition != nil {
				t.Fatalf("terminal appears waiting: %+v %v", v, err)
			}
		})
	}
}

func TestMalformedSpecRefusesWithoutPublication(t *testing.T) {
	cases := map[string]func(*Spec){
		"task oid":      func(x *Spec) { x.ExpectedTaskSHA = "task" },
		"source oid":    func(x *Spec) { x.ExpectedSourceSHA = "source" },
		"uppercase oid": func(x *Spec) { x.ExpectedTaskSHA = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" },
		"purpose":       func(x *Spec) { x.Generation.Purpose = "review" },
		"type":          func(x *Spec) { x.Generation.Type = "unknown" },
		"prefix":        func(x *Spec) { x.Generation.Branch = "review/a/h/feat/t" },
		"worktree":      func(x *Spec) { x.Generation.Worktree = "relative" },
		"owner":         func(x *Spec) { x.Generation.Owner.Actor = "other" },
		"source":        func(x *Spec) { x.Generation.Source = "other" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			x := spec("a", "ka", "r")
			change(&x)
			if _, err := s.Enqueue(x); err == nil {
				t.Fatal("malformed immutable spec accepted")
			}
			v, err := s.List()
			if err != nil || len(v) != 0 {
				t.Fatalf("refusal published: %+v %v", v, err)
			}
		})
	}
}

func TestOpenRejectsImpossibleClaimState(t *testing.T) {
	cases := map[string]func(*Request){
		"running no claim":      func(r *Request) { r.State = Running; r.Claim = nil },
		"admitted no claim":     func(r *Request) { r.State = Admitted; r.Claim = nil },
		"revalidation no claim": func(r *Request) { r.State = RevalidationRequired; r.Claim = nil },
		"queued with claim":     func(r *Request) { r.State = Queued },
		"terminal with claim":   func(r *Request) { r.State = Succeeded },
		"foreign claim owner":   func(r *Request) { r.Claim.Owner.Actor = "other" },
		"foreign generation":    func(r *Request) { r.Claim.Generation.Task = "other" },
		"missing claim time":    func(r *Request) { r.Claim.ClaimedAt = time.Time{} },
		"missing pid":           func(r *Request) { r.Claim.PID = 0 },
		"missing host":          func(r *Request) { r.Claim.Host = "" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			s, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r, err := s.Enqueue(spec("a", "ka", "r"))
			if err != nil {
				t.Fatal(err)
			}
			r.State = Admitted
			r.Claim = &Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Unix(100, 0)}
			change(&r)
			b, err := json.Marshal(persisted{NextSequence: 1, Requests: []Request{r}})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(s.statePath, b, 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(s.root); err == nil {
				t.Fatal("impossible persisted claim/state accepted")
			}
		})
	}
}

func TestClaimCASAndRecoveryRefusalsPreserveRequest(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Enqueue(spec("a", "ka", "r"))
	if err != nil {
		t.Fatal(err)
	}
	c := Claim{Owner: r.Spec.Owner, Generation: r.Spec.Generation, PID: 1, Host: "h", ClaimedAt: time.Unix(100, 0)}
	bad := c
	bad.ClaimedAt = time.Time{}
	if _, err = s.Admit(r.Spec.ID, r.Revision, bad); err == nil {
		t.Fatal("zero-time claim admitted")
	}
	r, err = s.Admit(r.Spec.ID, r.Revision, c)
	if err != nil {
		t.Fatal(err)
	}
	bad = c
	bad.PID++
	if _, err = s.Mark(r.Spec.ID, r.Revision, &bad, Running, ""); err == nil {
		t.Fatal("foreign claim passed CAS")
	}
	if _, err = s.Mark(r.Spec.ID, r.Revision+1, &c, Running, ""); err == nil {
		t.Fatal("stale revision passed CAS")
	}
	bad = c
	bad.Owner.Actor = "other"
	if _, err = s.RecoverAdmitted(r.Spec.ID, r.Spec.Owner, r.Revision, c, bad, ClaimDead); err == nil {
		t.Fatal("foreign owner recovered")
	}
	bad = c
	bad.Generation.Task = "other"
	if _, err = s.RecoverAdmitted(r.Spec.ID, r.Spec.Owner, r.Revision, c, bad, ClaimDead); err == nil {
		t.Fatal("foreign generation recovered")
	}
	bad = c
	bad.ClaimedAt = time.Time{}
	if _, err = s.RecoverAdmitted(r.Spec.ID, r.Spec.Owner, r.Revision, c, bad, ClaimDead); err == nil {
		t.Fatal("zero-time replacement recovered")
	}
	v, err := s.Get(r.Spec.ID)
	if err != nil || v.Request.Revision != r.Revision || !sameClaim(*v.Request.Claim, c) {
		t.Fatalf("refusal mutated claim: %+v %v", v, err)
	}
	// A different timezone spelling of the same instant must still bind the claim.
	equivalent := c
	equivalent.Generation.StartedAt = c.Generation.StartedAt.In(time.FixedZone("offset", 3600))
	equivalent.ClaimedAt = c.ClaimedAt.In(time.FixedZone("offset", 3600))
	if _, err = s.Mark(r.Spec.ID, r.Revision, &equivalent, Running, ""); err != nil {
		t.Fatalf("equivalent UTC claim refused: %v", err)
	}
}
