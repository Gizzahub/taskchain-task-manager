package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sharedPolicyFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo, a, b := sharedFixture(t)
	result, err := EnableShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	binding := policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "shared", Namespace: result.NamespaceID}
	local := bindAuthorityFixture(t, a, binding)
	bindAuthorityFixture(t, b, binding)
	s, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.SchemaVersion = 3
	state.Policy = &sharedPolicyAuthority{AuthorityID: binding.AuthorityID, Phase: "active", Canonical: local.Canonical, Digest: local.Digest, Pending: []policyActivationPlan{}}
	if err := publishSharedState(s.root, state, false); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, b} {
		if _, err := List(dir); err != nil {
			t.Fatalf("valid shared policy: %v", err)
		}
	}
	return repo, a, b
}

func syntheticPolicyTransition() TransitionRequest {
	return TransitionRequest{ID: "TASK-1", Owner: "worker", Token: testToken, RequestID: strings.Repeat("d", 32), From: "todo", To: "doing"}
}

func TestSharedPolicyPendingBlocksEveryBoardAdmission(t *testing.T) {
	_, a, b := sharedPolicyFixture(t)
	s, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(a)
	if err != nil {
		t.Fatal(err)
	}
	local, err := loadPolicyActivation(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	state := *s.state
	policy := *state.Policy
	policy.Phase = "initializing"
	policy.Pending = []policyActivationPlan{local.Plan}
	state.Policy = &policy
	if err := publishSharedState(s.root, state, false); err != nil {
		t.Fatal(err)
	}
	beforeCommon, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, b} {
		assertPolicyAdmissionBlocked(t, dir, syntheticPolicyTransition())
		if _, err := EnableShared(dir, true); err == nil || !strings.Contains(err.Error(), "policy") {
			t.Fatalf("ID activation bypass: %v", err)
		}
	}
	s, release, err = acquireSharedForPolicy(a)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeCommon, after) {
		t.Fatal("rejected admissions changed common state")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedPolicyMarkerlessAndCopiedCompletedRequireJoin(t *testing.T) {
	repo, a, _ := sharedPolicyFixture(t)
	newRoot := filepath.Join(t.TempDir(), "new")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	dir := filepath.Join(newRoot, "tasks")
	assertPolicyAdmissionBlocked(t, dir, syntheticPolicyTransition())
	for _, file := range []string{policyFile, transitionsFile, policyActivationFile, idsFile} {
		raw, err := os.ReadFile(filepath.Join(a, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertPolicyAdmissionBlocked(t, dir, syntheticPolicyTransition())
	if _, err := EnableShared(a, false); err == nil || !strings.Contains(err.Error(), "another board") {
		t.Fatalf("copied owner via enable-shared: %v", err)
	}
}

func TestSharedPolicyCommonAuthorityMustMatchLocal(t *testing.T) {
	for _, scenario := range []string{"missing-policy", "different-policy", "missing-ids"} {
		t.Run(scenario, func(t *testing.T) {
			_, a, _ := sharedPolicyFixture(t)
			s, release, err := acquireShared(a, false)
			if err != nil {
				t.Fatal(err)
			}
			state := *s.state
			switch scenario {
			case "missing-policy":
				state.SchemaVersion = 1
				state.Policy = nil
			case "different-policy":
				p := *state.Policy
				p.AuthorityID = strings.Repeat("f", 32)
				state.Policy = &p
			case "missing-ids":
				if err := os.Remove(filepath.Join(a, idsFile)); err != nil {
					t.Fatal(err)
				}
			}
			if err := publishSharedState(s.root, state, false); err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			assertPolicyAdmissionBlocked(t, a, syntheticPolicyTransition())
		})
	}
}

func TestLocalPolicyCanPrecedeSharedIDActivation(t *testing.T) {
	_, a, b := sharedFixture(t)
	for _, dir := range []string{a, b} {
		bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})
	}
	if _, err := EnableShared(a, false); err != nil {
		t.Fatalf("local policy prevented ID activation: %v", err)
	}
	for _, dir := range []string{a, b} {
		if _, err := List(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSharedPolicyBundleAdoptionKeepsAuthority(t *testing.T) {
	_, a, _ := sharedPolicyFixture(t)
	raw := publicationRequest(t, a)
	result, err := PublishBundle(a, raw, BundleOptions{Adopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" {
		t.Fatalf("result=%+v", result)
	}
	s, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.state.SchemaVersion != 3 || s.state.Policy == nil || s.state.BundleProtocol != 1 {
		t.Fatalf("bundle downgraded authority: %+v", s.state)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if replay, err := PublishBundle(a, raw, BundleOptions{}); err != nil || !replay.Replayed {
		t.Fatalf("replay=%+v %v", replay, err)
	}
}
