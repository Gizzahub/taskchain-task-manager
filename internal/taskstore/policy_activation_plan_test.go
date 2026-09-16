package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestPolicyActivationPlanIsReadOnlyAndReconstructsExactTarget(t *testing.T) {
	dir := claimBoard(t)
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before := boardBytes(t, dir)
	state, target, err := preparePolicyActivation(r, boardpolicy.Default(), policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Plan.OriginalJournal != "" || state.Plan.OriginalPolicy != "" || state.Plan.OriginalActivation != "" {
		t.Fatalf("unexpected originals: %+v", state.Plan)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("preparation changed board")
	}
	for _, phase := range []string{"original", "target"} {
		if phase == "target" {
			if err := r.WriteFile(transitionsFile, target, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		j, reconstructed, err := reconstructPolicyTarget(r, state)
		if err != nil || !bytes.Equal(target, reconstructed) || j.PolicyAuthority == nil || j.PolicyAuthority.AuthorityID != state.AuthorityID {
			t.Fatalf("%s reconstruction: %+v %v", phase, j, err)
		}
	}
	if err := r.WriteFile(transitionsFile, append(append([]byte(nil), target...), ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	before = boardBytes(t, dir)
	if _, _, err := reconstructPolicyTarget(r, state); err == nil {
		t.Fatal("changed journal accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("reconstruction repaired conflicting file")
	}
}

func TestPolicyActivationPlanUsesTargetParkingWithoutAdoption(t *testing.T) {
	dir := claimBoard(t)
	if err := os.Mkdir(filepath.Join(dir, "manual"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "manual/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	p, err := boardpolicy.New(boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: "manual", To: []string{"todo"}}}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	before := boardBytes(t, dir)
	if _, _, err := preparePolicyActivation(r, p, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, ""); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("target scan implicitly adopted policy")
	}
}

func TestPolicyActivationPlanRefusesPolicyChangeAndHeldClaim(t *testing.T) {
	for _, scenario := range []string{"immutable", "held"} {
		t.Run(scenario, func(t *testing.T) {
			dir := claimBoard(t)
			policy := boardpolicy.Default()
			if scenario == "immutable" {
				bindRuntimeFixture(t, dir, boardpolicy.Declaration{})
				var err error
				policy, err = boardpolicy.New(boardpolicy.Declaration{Zones: []string{"manual"}})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
					t.Fatal(err)
				}
			}
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			before := boardBytes(t, dir)
			if _, _, err := preparePolicyActivation(r, policy, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"}, ""); err == nil {
				t.Fatal("unsafe activation plan accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("failed plan changed board")
			}
		})
	}
}
