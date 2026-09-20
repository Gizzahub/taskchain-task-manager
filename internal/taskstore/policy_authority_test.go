package taskstore

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// Synthetic installation is intentionally not the public activation protocol.
func bindAuthorityFixture(t *testing.T, dir string, binding policyAuthorityBinding) policyActivationState {
	t.Helper()
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil {
		t.Fatal(err)
	}
	p := boardpolicy.Default()
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	j.SchemaVersion, j.PolicyDigest, j.PolicyAuthority = 3, bytesDigest(canonical), &binding
	raw, err := encodeTransitionJournal(j, p)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err = filepath.Abs(owner)
	if err != nil {
		t.Fatal(err)
	}
	idsRaw, err := r.ReadFile(idsFile)
	if err != nil {
		t.Fatal(err)
	}
	idsDigest := bytesDigest(idsRaw)
	state := policyActivationState{SchemaVersion: 1, Phase: "completed", AuthorityID: binding.AuthorityID, Scope: binding.Scope, Namespace: binding.Namespace, Canonical: canonical, Digest: j.PolicyDigest, Plan: policyActivationPlan{Root: owner, Snapshot: strings.Repeat("b", 64), TargetJournal: bytesDigest(raw), OriginalIDs: idsDigest, TargetIDs: idsDigest, IDTarget: []byte{}}}
	if binding.Scope == "shared" {
		state.Plan.HEAD = strings.Repeat("c", 40)
	}
	if err := r.WriteFile(policyFile, canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.WriteFile(transitionsFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := savePolicyActivation(r, state, true); err != nil {
		t.Fatal(err)
	}
	return state
}

func policyAdmissionChecks(t *testing.T, dir string, req TransitionRequest) map[string]func() error {
	t.Helper()
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
	bundle, _ := bundleFixture(t)
	raw, err := parsedBundle(t, bundle).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]func() error{
		"init":             func() error { return Init(dir) },
		"list":             func() error { _, err := List(dir); return err },
		"ready":            func() error { _, err := Ready(dir); return err },
		"create":           func() error { _, err := Create(dir, CreateRequest{Title: "blocked"}); return err },
		"reserve":          func() error { _, err := ReserveIDs(dir, []string{"TASK-88"}, true); return err },
		"claim":            func() error { _, err := Claim(dir, claim); return err },
		"release":          func() error { _, err := Release(dir, claim); return err },
		"claim-resume":     func() error { _, err := ClaimResume(dir, claim); return err },
		"transition":       func() error { _, err := Transition(dir, req); return err },
		"recover":          func() error { _, err := Recover(dir, req); return err },
		"register-context": func() error { _, err := RegisterContext(dir, []byte(testContextIntent)); return err },
		"show-context":     func() error { _, err := ShowContext(dir, "intent", bundle.Batch.Intent.ID, 1); return err },
		"bundle":           func() error { _, err := PublishBundle(dir, raw, BundleOptions{Adopt: true}); return err },
	}
}

func assertPolicyAdmissionBlocked(t *testing.T, dir string, req TransitionRequest) {
	t.Helper()
	before := boardBytes(t, dir)
	commonBefore := policyCommonBytes(t, dir)
	for name, operation := range policyAdmissionChecks(t, dir, req) {
		err := operation()
		if err == nil || (!strings.Contains(err.Error(), "policy") && !strings.Contains(err.Error(), "activation")) {
			t.Fatalf("%s did not hit policy admission: %v", name, err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatalf("%s changed rejected board", name)
		}
	}
	if !reflect.DeepEqual(commonBefore, policyCommonBytes(t, dir)) {
		t.Fatal("rejected admission changed common state")
	}
}

func policyCommonBytes(t *testing.T, dir string) []byte {
	t.Helper()
	s, release, err := acquireSharedForPolicy(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Fatal(err)
		}
	}()
	if s == nil || s.state == nil {
		return nil
	}
	raw, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPolicyAuthorityLocalAdmissionAndHistoricalReplay(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"pending", "missing", "wrong-owner", "different-authority", "journal-missing", "policy-missing"} {
		t.Run(scenario, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			if _, err := Transition(dir, req); err != nil {
				t.Fatal(err)
			}
			state := bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})
			if _, err := List(dir); err != nil {
				t.Fatalf("valid v3 baseline: %v", err)
			}
			if _, err := Transition(dir, req); err != nil {
				t.Fatalf("legacy replay: %v", err)
			}
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "pending":
				state.Phase = "pending"
				err = savePolicyActivation(r, state, false)
			case "missing":
				err = r.Remove(policyActivationFile)
			case "wrong-owner":
				state.Plan.Root = filepath.Join(dir, "other")
				err = savePolicyActivation(r, state, false)
			case "different-authority":
				state.AuthorityID = strings.Repeat("d", 32)
				err = savePolicyActivation(r, state, false)
			case "journal-missing":
				err = r.Remove(transitionsFile)
			case "policy-missing":
				err = r.Remove(policyFile)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			assertPolicyAdmissionBlocked(t, dir, req)
		})
	}
}

func TestPolicyAuthorityWireShape(t *testing.T) {
	t.Parallel()
	p := boardpolicy.Default()
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	j := transitionJournal{SchemaVersion: 3, PolicyDigest: digest, Records: []transitionRecord{}, PolicyAuthority: &policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}}
	raw, err := encodeTransitionJournal(j, p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeTransitionJournal(raw); err != nil {
		t.Fatal(err)
	}
	for name, mutation := range map[string]func(map[string]json.RawMessage){
		"missing": func(m map[string]json.RawMessage) { delete(m, "policyAuthority") },
		"null":    func(m map[string]json.RawMessage) { m["policyAuthority"] = json.RawMessage("null") },
		"legacy":  func(m map[string]json.RawMessage) { m["schemaVersion"] = json.RawMessage("2") },
		"scope": func(m map[string]json.RawMessage) {
			m["policyAuthority"] = json.RawMessage(`{"authorityId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scope":"shared","namespace":""}`)
		},
		"unknown": func(m map[string]json.RawMessage) {
			m["policyAuthority"] = json.RawMessage(`{"authorityId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","scope":"local","namespace":"","extra":1}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			var m map[string]json.RawMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			mutation(m)
			bad, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeTransitionJournal(bad); err == nil {
				t.Fatal("invalid v3 binding accepted")
			}
		})
	}
}

func TestPolicyAuthorityCompletedSnapshotIsNotAdmissionCondition(t *testing.T) {
	t.Parallel()
	dir := claimBoard(t)
	bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})
	if _, err := Create(dir, CreateRequest{Title: "evolved board"}); err != nil {
		t.Fatal(err)
	}
	if _, err := List(dir); err != nil {
		t.Fatal(err)
	}
}
