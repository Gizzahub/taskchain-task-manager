package taskstore

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPolicyRevisionJoinRetainsBarrierBinding(t *testing.T) {
	t.Parallel()
	repo, a, _, options := sharedRevisionFixture(t)
	raw := revisionPolicyBytes(t)
	if _, err := RevisePolicy(a, raw, options); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "barrier-join")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	joined := filepath.Join(newRoot, "tasks")
	if _, err := ActivatePolicy(joined, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireSharedForPolicy(a)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.root.Name(), sharedStateFile)
	stateRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(stateRaw, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "policyRevisionProtocol")
	broken, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, joined} {
		before := boardBytes(t, dir)
		if _, err := Create(dir, CreateRequest{Title: "blocked"}); err == nil || !strings.Contains(err.Error(), "barrier") {
			t.Fatalf("lost barrier admitted: %v", err)
		}
		if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err == nil {
			t.Fatal("join repaired missing barrier")
		}
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatal("missing barrier changed board")
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(broken) {
		t.Fatal("barrier loss silently repaired")
	}
}

func TestPolicyRevisionSharedResumePreflightsEveryBoard(t *testing.T) {
	t.Parallel()
	_, a, b, options := sharedRevisionFixture(t)
	raw := revisionPolicyBytes(t)
	stop := errors.New("common-only revision stop")
	if _, err := revisePolicyWithStep(a, raw, options, func(at string) error {
		if at == "after-common-policy-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	path := filepath.Join(b, "todo/TASK-1.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(original, []byte("\nexternal edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
	options.Resume = true
	if _, err := RevisePolicy(a, raw, options); err == nil {
		t.Fatal("changed second board admitted")
	}
	if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
		t.Fatal("conflict advanced another participant")
	}
}

func TestPolicyRevisionRejectsLostHistoricalBinding(t *testing.T) {
	t.Parallel()
	for _, downgrade := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing-previous", true: "downgrade"}[downgrade], func(t *testing.T) {
			dir, options := localRevisionFixture(t)
			if _, err := RevisePolicy(dir, revisionPolicyBytes(t), options); err != nil {
				t.Fatal(err)
			}
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			j, err := loadTransitions(r)
			if err != nil {
				t.Fatal(err)
			}
			if downgrade {
				j.SchemaVersion, j.PolicyHistory = 3, nil
			} else {
				delete(j.PolicyHistory, options.ExpectedDigest)
			}
			raw, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.WriteFile(transitionsFile, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := List(dir); err == nil || !strings.Contains(err.Error(), "historical journal binding") {
				t.Fatalf("lost history admitted: %v", err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("history loss changed files")
			}
		})
	}
}
