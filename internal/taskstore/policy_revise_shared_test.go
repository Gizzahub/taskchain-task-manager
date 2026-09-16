package taskstore

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func sharedRevisionFixture(t *testing.T) (string, string, string, PolicyRevisionOptions) {
	t.Helper()
	repo, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	active, err := ActivatePolicy(a, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
	if err != nil {
		t.Fatal(err)
	}
	return repo, a, b, PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest, AllWorktrees: true}
}

func TestPolicyRevisionSharedRecoveryEveryBoundary(t *testing.T) {
	for _, phase := range []string{"after-common-policy-pending", "board-0/after-local-pending", "board-0/after-policy", "board-0/after-policy-ids", "board-0/after-policy-journal", "board-0/after-local-completed", "after-policy-board-0", "after-policy-board-1", "after-common-policy-active"} {
		t.Run(phase, func(t *testing.T) {
			_, a, b, options := sharedRevisionFixture(t)
			raw := revisionPolicyBytes(t)
			stop := errors.New("synthetic shared revision interruption")
			if _, err := revisePolicyWithStep(a, raw, options, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if phase != "after-common-policy-active" {
				beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
				if _, err := Create(b, CreateRequest{Title: "must block"}); err == nil {
					t.Fatal("other board writer admitted")
				}
				if _, err := ActivatePolicy(b, raw, PolicyActivationOptions{AllWorktrees: true, Resume: true}); err == nil {
					t.Fatal("initial command resumed revision")
				}
				if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
					t.Fatal("rejected writer changed state")
				}
			}
			options.Resume = true
			result, err := RevisePolicy(b, raw, options)
			if err != nil || result.Status != "completed" {
				t.Fatalf("resume=%+v %v", result, err)
			}
			for _, dir := range []string{a, b} {
				if _, err := List(dir); err != nil {
					t.Fatal(err)
				}
			}
			beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
			if result, err := RevisePolicy(a, raw, options); err != nil || !result.Replayed {
				t.Fatalf("replay=%+v %v", result, err)
			}
			if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
				t.Fatal("completed replay changed state")
			}
		})
	}
}

func TestPolicyRevisionSharedPreflightAndJoin(t *testing.T) {
	repo, a, b, options := sharedRevisionFixture(t)
	raw := revisionPolicyBytes(t)
	withoutAll := options
	withoutAll.AllWorktrees = false
	if _, err := RevisePolicy(a, raw, withoutAll); err == nil {
		t.Fatal("scope acknowledgement missing")
	}
	claim := ClaimRequest{ID: "TASK-1", Owner: "fixture", Token: testToken}
	if _, err := Claim(b, claim); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
	if _, err := RevisePolicy(a, raw, options); err == nil {
		t.Fatal("held claim admitted")
	}
	if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
		t.Fatal("preflight failure partially published")
	}
	if _, err := Release(b, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := RevisePolicy(a, raw, options); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "revision-join")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	board := filepath.Join(newRoot, "tasks")
	if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireSharedForPolicy(a)
	if err != nil {
		t.Fatal(err)
	}
	if s.state.PolicyRevisionProtocol != 1 {
		t.Fatal("join downgraded permanent barrier")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := RevisePolicy(a, raw, options); err != nil {
		t.Fatalf("original completed revision lost after another join: %v", err)
	}
}
