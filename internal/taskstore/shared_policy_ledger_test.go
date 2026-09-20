package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func validSharedV3Fixture(t *testing.T) sharedState {
	t.Helper()
	state := validSharedFixture()
	state.SchemaVersion = 3
	state.Phase = "active"
	authority := authorityFixture(t)
	authority.Phase = "active"
	authority.Pending = []policyActivationPlan{}
	state.Policy = &authority
	return state
}

func TestSharedStateV3RoundTripAndPolicyRequired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state := validSharedV3Fixture(t)
	if err := publishSharedState(r, state, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadSharedState(r)
	if err != nil || !reflect.DeepEqual(loaded, state) {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	without := state
	without.Policy = nil
	if err := validateSharedState(without); err == nil {
		t.Fatal("schema 3 without policy accepted")
	}
	withProtocol := state
	withProtocol.BundleProtocol = 1
	raw, err := json.Marshal(withProtocol)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"bundleProtocol":1`), []byte(`"bundleProtocol":0`), 1)
	if err := validateSharedShape(raw); err == nil {
		t.Fatal("schema 3 explicit protocol zero accepted")
	}
	legacy := state
	legacy.SchemaVersion = 2
	legacy.BundleProtocol = 1
	if err := validateSharedState(legacy); err == nil {
		t.Fatal("schema 2 with policy accepted")
	}
}

func TestSharedStateV3StrictMutations(t *testing.T) {
	t.Parallel()
	base := validSharedV3Fixture(t)
	if err := validateSharedState(base); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}
	mutations := map[string]func(*sharedState){
		"phase":    func(s *sharedState) { s.Phase = "initializing" },
		"protocol": func(s *sharedState) { s.BundleProtocol = 2 },
		"policy pending": func(s *sharedState) {
			s.Policy.Pending = []policyActivationPlan{{Root: "/a/tasks", HEAD: strings.Repeat("a", 40), Snapshot: strings.Repeat("b", 64), TargetJournal: strings.Repeat("c", 64), OriginalIDs: strings.Repeat("d", 64), TargetIDs: strings.Repeat("d", 64), IDTarget: []byte{}}}
		},
		"policy nil": func(s *sharedState) { s.Policy = nil },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			state := base
			policy := *base.Policy
			policy.Pending = append([]policyActivationPlan{}, base.Policy.Pending...)
			state.Policy = &policy
			mutate(&state)
			if err := validateSharedState(state); err == nil {
				t.Fatal("invalid schema 3 state accepted")
			}
		})
	}
	withBundle := base
	withBundle.BundleProtocol = 1
	withBundle.Policy = &sharedPolicyAuthority{AuthorityID: base.Policy.AuthorityID, Phase: "initializing", Canonical: base.Policy.Canonical, Digest: base.Policy.Digest, Pending: []policyActivationPlan{{Root: "/a/tasks", HEAD: strings.Repeat("a", 40), Snapshot: strings.Repeat("b", 64), TargetJournal: strings.Repeat("c", 64), OriginalIDs: strings.Repeat("d", 64), TargetIDs: strings.Repeat("d", 64), IDTarget: []byte{}}}}
	withBundle.PendingBundle = &sharedBundlePending{RequestID: strings.Repeat("a", 32), Digest: strings.Repeat("b", 64), BoardID: strings.Repeat("c", 32), Owner: "/work/board", IDs: []string{"TASK-1"}}
	validCombination := withBundle
	activePolicy := *withBundle.Policy
	activePolicy.Phase, activePolicy.Pending = "active", []policyActivationPlan{}
	validCombination.Policy = &activePolicy
	if err := validateSharedState(validCombination); err != nil {
		t.Fatalf("bundle baseline invalid: %v", err)
	}
	if err := validateSharedState(withBundle); err == nil {
		t.Fatal("policy and bundle pending accepted together")
	}
	idsRaw, err := ledgerBytes(idLedger{SchemaVersion: 3, Namespace: base.NamespaceID, Reserved: []string{"TASK-99"}})
	if err != nil {
		t.Fatal(err)
	}
	unreserved := base
	policy := *base.Policy
	policy.Pending = []policyActivationPlan{{Root: "/a/tasks", HEAD: strings.Repeat("a", 40), Snapshot: strings.Repeat("b", 64), TargetJournal: strings.Repeat("c", 64), OriginalIDs: strings.Repeat("d", 64), TargetIDs: bytesDigest(idsRaw), IDTarget: idsRaw}}
	unreserved.Policy = &policy
	if err := validateSharedState(unreserved); err == nil {
		t.Fatal("shared policy target with unreserved ID accepted")
	}
}

func TestSharedStateV3LoaderRejectsSingleFieldMutations(t *testing.T) {
	t.Parallel()
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := publishSharedState(r, validSharedV3Fixture(t), true); err != nil {
		t.Fatal(err)
	}
	valid, err := r.ReadFile(sharedStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadSharedState(r); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for name, mutate := range map[string]func(map[string]json.RawMessage){
		"null policy":     func(m map[string]json.RawMessage) { m["policy"] = json.RawMessage("null") },
		"missing policy":  func(m map[string]json.RawMessage) { delete(m, "policy") },
		"alias":           func(m map[string]json.RawMessage) { m["Policy"] = m["policy"]; delete(m, "policy") },
		"unknown":         func(m map[string]json.RawMessage) { m["extra"] = json.RawMessage("1") },
		"protocol zero":   func(m map[string]json.RawMessage) { m["bundleProtocol"] = json.RawMessage("0") },
		"protocol null":   func(m map[string]json.RawMessage) { m["bundleProtocol"] = json.RawMessage("null") },
		"protocol string": func(m map[string]json.RawMessage) { m["bundleProtocol"] = json.RawMessage(`"1"`) },
		"nested null": func(m map[string]json.RawMessage) {
			var policy map[string]json.RawMessage
			if err := json.Unmarshal(m["policy"], &policy); err != nil {
				t.Fatal(err)
			}
			policy["pending"] = json.RawMessage("null")
			raw, err := json.Marshal(policy)
			if err != nil {
				t.Fatal(err)
			}
			m["policy"] = raw
		},
	} {
		t.Run(name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(valid, &fields); err != nil {
				t.Fatal(err)
			}
			mutate(fields)
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.WriteFile(sharedStateFile, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSharedState(r); err == nil {
				t.Fatal("malformed v3 accepted")
			}
			after, err := r.ReadFile(sharedStateFile)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, after) {
				t.Fatal("rejected read mutated state")
			}
		})
	}
}

func TestSharedStateV3SerializationRetainsPolicy(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(validSharedV3Fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"policy"`)) {
		t.Fatal("schema 3 serialization dropped policy")
	}
}

func TestSharedBundlePreparationRetainsSchema3(t *testing.T) {
	t.Parallel()
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	state := *s.state
	state.SchemaVersion = 3
	state.Phase = "active"
	authority := authorityFixture(t)
	authority.Phase = "active"
	authority.Pending = []policyActivationPlan{}
	state.Policy = &authority
	if err := publishSharedState(s.root, state, false); err != nil {
		t.Fatal(err)
	}
	s.state = &state
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	record := sharedBundleRecordFixture(t, s, []string{"TASK-1"})
	j := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record}}
	next, err := s.prepareBundleReservation(r, j, record)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.SchemaVersion != 3 || next.BundleProtocol != 1 || next.Policy == nil {
		t.Fatalf("next=%+v", next)
	}
}
