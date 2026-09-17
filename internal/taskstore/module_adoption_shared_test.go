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

func TestModuleAdoptionSharedRejectsRehashedSubset(t *testing.T) {
	_, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	writeModuleCard(t, a, "backend/todo/TASK-100.md", "TASK-100", "pending")
	stop := errors.New("saved common plan")
	options := PolicyActivationOptions{AllWorktrees: true, AdoptModules: true}
	if _, err := activatePolicyWithStep(a, moduleAdoptionRaw(t), options, func(phase string) error {
		if phase == "after-common-policy-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("pending boundary: %v", err)
	}
	s, release, err := acquireSharedForPolicy(a)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(s.root.Name(), sharedStateFile)
	state := *s.state
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedState(state); err != nil {
		t.Fatalf("valid baseline: %v", err)
	}
	plan := &state.Policy.Pending[0]
	ledger, err := decodeIDs(plan.IDTarget)
	if err != nil {
		t.Fatal(err)
	}
	kept := []string{}
	for _, id := range ledger.Reserved {
		if id != "TASK-100" {
			kept = append(kept, id)
		}
	}
	ledger.Reserved = kept
	plan.IDTarget, err = ledgerBytes(ledger)
	if err != nil {
		t.Fatal(err)
	}
	plan.TargetIDs = bytesDigest(plan.IDTarget)
	broken, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, broken, 0600); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, a)
	options.Resume = true
	if _, err := ActivatePolicy(a, moduleAdoptionRaw(t), options); err == nil || !strings.Contains(err.Error(), "exact frozen common reservations") {
		t.Fatalf("rehashed subset accepted: %v", err)
	}
	after, err := os.ReadFile(name)
	if err != nil || string(after) != string(broken) || !reflect.DeepEqual(before, boardBytes(t, a)) {
		t.Fatal("rejection mutated common plan or board")
	}
}

func TestModuleAdoptionSharedInitialAndRevisionRecovery(t *testing.T) {
	for _, revision := range []bool{false, true} {
		for _, phase := range []string{"after-common-policy-pending", "board-0/after-policy-ids", "after-policy-board-0", "after-common-policy-active"} {
			t.Run(map[bool]string{false: "initial", true: "revision"}[revision]+"/"+phase, func(t *testing.T) {
				_, a, b := sharedFixture(t)
				if _, err := EnableShared(a, false); err != nil {
					t.Fatal(err)
				}
				options := PolicyRevisionOptions{AllWorktrees: true, AdoptModules: true}
				if revision {
					active, err := ActivatePolicy(a, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
					if err != nil {
						t.Fatal(err)
					}
					options.ExpectedAuthorityID, options.ExpectedDigest = active.AuthorityID, active.Digest
				}
				writeModuleCard(t, a, "backend/todo/TASK-100.md", "TASK-100", "pending")
				writeModuleCard(t, b, "backend/todo/TASK-101.md", "TASK-101", "pending")
				raw := moduleAdoptionRaw(t)
				run := func(board string, resume, adopt bool, step func(string) error) (PolicyActivationResult, error) {
					if revision {
						opts := options
						opts.Resume, opts.AdoptModules = resume, adopt
						return revisePolicyWithStep(board, raw, opts, step)
					}
					return activatePolicyWithStep(board, raw, PolicyActivationOptions{AllWorktrees: true, Resume: resume, AdoptModules: adopt}, step)
				}
				beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
				if _, err := run(a, false, false, nil); err == nil {
					t.Fatal("scope expansion without acknowledgement")
				}
				if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
					t.Fatal("refusal changed participant")
				}
				stop := errors.New("module shared stop")
				if _, err := run(a, false, true, func(at string) error {
					if at == phase {
						return stop
					}
					return nil
				}); !errors.Is(err, stop) {
					t.Fatalf("boundary not reached: %v", err)
				}
				if phase != "after-common-policy-active" {
					if _, err := run(b, true, false, nil); err == nil {
						t.Fatal("flagless resume adopted IDs")
					}
					if _, err := Create(b, CreateRequest{Title: "blocked"}); err == nil {
						t.Fatal("other participant wrote during adoption")
					}
				}
				result, err := run(b, true, true, nil)
				if err != nil || result.Status != "completed" {
					t.Fatalf("resume=%+v %v", result, err)
				}
				for _, board := range []string{a, b} {
					ledger := adoptionLedger(t, board)
					if ledger.SchemaVersion != 3 || !containsAllIDs(ledger.Reserved, []string{"TASK-100", "TASK-101"}) {
						t.Fatalf("union not bound: %+v", ledger)
					}
					if _, err := List(board); err != nil {
						t.Fatal(err)
					}
				}
				beforeA, beforeB, common = boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
				if replay, err := run(a, true, true, nil); err != nil || !replay.Replayed {
					t.Fatalf("replay=%+v %v", replay, err)
				}
				if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
					t.Fatal("replay changed state")
				}
			})
		}
	}
}

func TestModuleAdoptionJoinRetainsPermanentBarrier(t *testing.T) {
	repo, a, _, options := sharedRevisionFixture(t)
	writeModuleCard(t, a, "backend/todo/TASK-100.md", "TASK-100", "pending")
	options.AdoptModules = true
	raw := moduleAdoptionRaw(t)
	if _, err := RevisePolicy(a, raw, options); err != nil {
		t.Fatal(err)
	}
	joinedRoot := filepath.Join(t.TempDir(), "module-join")
	sharedGit(t, repo, "worktree", "add", "--detach", joinedRoot, "HEAD")
	joined := filepath.Join(joinedRoot, "tasks")
	if _, err := ActivatePolicy(joined, raw, PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireSharedForPolicy(a)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(s.root.Name(), sharedStateFile)
	common, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(common, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["moduleProtocol"]) != "1" {
		t.Fatal("join lost module protocol")
	}
	delete(fields, "moduleProtocol")
	broken, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, broken, 0600); err != nil {
		t.Fatal(err)
	}
	for _, board := range []string{a, joined} {
		before := boardBytes(t, board)
		if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{AdoptModules: true}); err == nil {
			t.Fatal("join repaired lost protocol")
		}
		if _, err := Create(board, CreateRequest{Title: "blocked"}); err == nil {
			t.Fatal("writer ignored lost protocol")
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("lost protocol changed board")
		}
	}
}
