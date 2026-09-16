package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestPolicyActivationLocalPublicationResumesEveryBoundary(t *testing.T) {
	for _, phase := range []string{"after-local-pending", "after-policy", "after-policy-journal", "after-local-completed"} {
		t.Run(phase, func(t *testing.T) {
			dir := claimBoard(t)
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			unlock, err := lock(r)
			if err != nil {
				t.Fatal(err)
			}
			state, _, err := preparePolicyActivation(r, boardpolicy.Default(), policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, "")
			if err != nil {
				t.Fatal(err)
			}
			stop := errors.New("synthetic publication stop")
			err = applyPolicyActivationPlan(r, state, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if phase != "after-local-completed" {
				assertPolicyAdmissionBlocked(t, dir, syntheticPolicyTransition())
			}
			r, err = openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			unlock, err = lock(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := applyPolicyActivationPlan(r, state, nil); err != nil {
				t.Fatalf("resume: %v", err)
			}
			if err := unlock(); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := List(dir); err != nil {
				t.Fatalf("completed admission: %v", err)
			}
		})
	}
}

func TestPolicyActivationResumePreservesConflicts(t *testing.T) {
	for _, scenario := range []string{"card-change", "missing-policy", "missing-receipt", "journal-change"} {
		t.Run(scenario, func(t *testing.T) {
			dir := claimBoard(t)
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			unlock, err := lock(r)
			if err != nil {
				t.Fatal(err)
			}
			defer unlock()
			state, _, err := preparePolicyActivation(r, boardpolicy.Default(), policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, "")
			if err != nil {
				t.Fatal(err)
			}
			stop := errors.New("stop")
			if err := applyPolicyActivationPlan(r, state, func(at string) error {
				if at == "after-policy-journal" {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			switch scenario {
			case "card-change":
				raw, err := r.ReadFile("todo/TASK-1.md")
				if err != nil {
					t.Fatal(err)
				}
				if err := r.WriteFile("todo/TASK-1.md", append(raw, []byte("\nexternal edit\n")...), 0o644); err != nil {
					t.Fatal(err)
				}
			case "missing-policy":
				if err := r.Remove(policyFile); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if err := r.Remove(policyActivationFile); err != nil {
					t.Fatal(err)
				}
			case "journal-change":
				raw, err := r.ReadFile(transitionsFile)
				if err != nil {
					t.Fatal(err)
				}
				if err := r.WriteFile(transitionsFile, append(raw, ' '), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			if err := applyPolicyActivationPlan(r, state, nil); err == nil {
				t.Fatal("conflicting resume accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("failed resume overwrote conflict")
			}
		})
	}
}

func TestPolicyActivationCustomPolicyTransitionRecovery(t *testing.T) {
	dir := claimBoard(t)
	p, err := boardpolicy.New(boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "todo", To: []string{"done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(r)
	if err != nil {
		t.Fatal(err)
	}
	state, _, err := preparePolicyActivation(r, p, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyPolicyActivationPlan(r, state, nil); err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	req := syntheticPolicyTransition()
	req.To = "done"
	stop := errors.New("transition stop")
	if _, err := transitionWithStep(dir, req, func(at string) error {
		if at == "after-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("custom transition: %v", err)
	}
	if _, err := Recover(dir, req); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Transition(dir, req); err != nil {
		t.Fatalf("custom completed replay: %v", err)
	}
}
