package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActivateSharedPolicyRejectsHeldClaimWithoutMutation(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(a, ClaimRequest{ID: "TASK-1", Owner: "held", Token: testToken}); err != nil {
		t.Fatal("valid held-claim baseline:", err)
	}
	beforeA, beforeB, beforeCommon := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
	if _, err := ActivatePolicy(a, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "held") {
		t.Fatalf("held claim was not rejected: %v", err)
	}
	if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(beforeCommon, policyCommonBytes(t, a)) {
		t.Fatal("held-claim rejection changed board or common state")
	}
}

func TestActivateSharedPolicyDifferentPolicyRejectsActiveAndPending(t *testing.T) {
	t.Run("active", func(t *testing.T) {
		_, a, b := sharedFixture(t)
		if _, err := EnableShared(a, false); err != nil {
			t.Fatal(err)
		}
		raw := defaultPolicyBytes(t)
		if result, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil || result.Status != "completed" {
			t.Fatalf("valid active baseline=%+v: %v", result, err)
		}
		beforeA, beforeB, beforeCommon := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
		other, err := filepathPolicy(t).Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(a, other, PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "different policy") {
			t.Fatalf("different active policy accepted: %v", err)
		}
		if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(beforeCommon, policyCommonBytes(t, a)) {
			t.Fatal("different active policy changed state")
		}
	})

	t.Run("pending", func(t *testing.T) {
		_, a, b := sharedFixture(t)
		if _, err := EnableShared(a, false); err != nil {
			t.Fatal(err)
		}
		raw := defaultPolicyBytes(t)
		stop := errors.New("stop before shared policy publication")
		if _, err := activatePolicyWithStep(a, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
			if at == "after-common-policy-pending" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatalf("valid pending baseline not reached: %v", err)
		}
		beforeA, beforeB, beforeCommon := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
		other, err := filepathPolicy(t).Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(a, other, PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "different policy") {
			t.Fatalf("different pending policy accepted: %v", err)
		}
		if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(beforeCommon, policyCommonBytes(t, a)) {
			t.Fatal("different pending policy changed state")
		}
	})
}

func TestActivateSharedPolicyPendingHeadChangeCannotResume(t *testing.T) {
	_, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	stop := errors.New("stop before shared policy publication")
	if _, err := activatePolicyWithStep(a, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
		if at == "after-common-policy-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("valid pending baseline not reached: %v", err)
	}
	// Advance the actual registered worktree HEAD after the pending plan was
	// recorded; mutating only the saved plan would not exercise inventory
	// verification.
	sharedGit(t, a, "commit", "--allow-empty", "-m", "synthetic HEAD change")
	beforeBoard, before := boardBytes(t, a), policyCommonBytes(t, a)
	if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true}); err == nil || (!strings.Contains(err.Error(), "HEAD") && !strings.Contains(err.Error(), "inventory") && !strings.Contains(err.Error(), "worktree")) {
		t.Fatalf("changed pending HEAD resumed unexpectedly: %v", err)
	}
	if !reflect.DeepEqual(beforeBoard, boardBytes(t, a)) || !reflect.DeepEqual(before, policyCommonBytes(t, a)) {
		t.Fatal("failed resume changed board or common state")
	}
}

func TestActivateSharedPolicyMissingIDLedgerIsRestoreOnly(t *testing.T) {
	_, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	if result, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil || result.Status != "completed" {
		t.Fatalf("valid shared baseline=%+v: %v", result, err)
	}
	if err := os.Remove(filepath.Join(a, idsFile)); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeCommon := boardBytes(t, a), policyCommonBytes(t, a)
	if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "id") {
		t.Fatalf("missing ID ledger was not rejected: %v", err)
	}
	if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeCommon, policyCommonBytes(t, a)) {
		t.Fatal("missing ID ledger failure changed state")
	}
}

func TestActivateSharedPolicyMarkerlessChangedIDModeCannotResume(t *testing.T) {
	for _, mode := range []string{"mode", "content"} {
		t.Run(mode, func(t *testing.T) {
			repo, a, _ := sharedFixture(t)
			if _, err := EnableShared(a, false); err != nil {
				t.Fatal(err)
			}
			raw := defaultPolicyBytes(t)
			if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
				t.Fatal("valid shared baseline:", err)
			}
			newRoot := filepath.Join(t.TempDir(), "markerless")
			sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
			dir := filepath.Join(newRoot, "tasks")
			stop := errors.New("stop before markerless local publication")
			if _, err := activatePolicyWithStep(dir, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
				if at == "after-common-policy-pending" {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("valid markerless pending baseline not reached: %v", err)
			}
			idsPath := filepath.Join(dir, idsFile)
			idsRaw, err := os.ReadFile(idsPath)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "mode" {
				if err := os.Chmod(idsPath, 0o640); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(idsPath, append(idsRaw, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			beforeBoard, beforeCommon := boardBytes(t, dir), policyCommonBytes(t, dir)
			if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true}); err == nil || (!strings.Contains(strings.ToLower(err.Error()), "id") && !strings.Contains(strings.ToLower(err.Error()), "mode")) {
				t.Fatalf("changed markerless ID %s resumed unexpectedly: %v", mode, err)
			}
			if !reflect.DeepEqual(beforeBoard, boardBytes(t, dir)) || !reflect.DeepEqual(beforeCommon, policyCommonBytes(t, dir)) {
				t.Fatal("changed markerless ID failure changed state")
			}
		})
	}
}

func TestActivateSharedPolicyCopiedPendingReceiptCannotJoin(t *testing.T) {
	repo, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	if result, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil || result.Status != "completed" {
		t.Fatalf("valid active-common baseline=%+v: %v", result, err)
	}
	sharedGit(t, repo, "add", "tasks")
	sharedGit(t, repo, "commit", "-m", "completed policy baseline")
	newRoot := filepath.Join(t.TempDir(), "copied")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	dir := filepath.Join(newRoot, "tasks")
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadPolicyActivation(r)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	state.Phase = "pending"
	if err := savePolicyActivation(r, state, false); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	beforeBoard, before := boardBytes(t, dir), policyCommonBytes(t, a)
	if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "matching completed original binding") {
		t.Fatalf("copied pending receipt was accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeBoard, boardBytes(t, dir)) || !reflect.DeepEqual(before, policyCommonBytes(t, a)) {
		t.Fatal("copied pending receipt changed board or common state")
	}
}
