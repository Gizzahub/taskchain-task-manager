package taskstore

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestPolicySharedAcknowledgementBeforeMutation(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	check := func(resume bool) {
		t.Helper()
		beforeA, beforeB, common := boardBytes(t, a), boardBytes(t, b), policyCommonBytes(t, a)
		if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{Resume: resume}); err == nil || !strings.Contains(err.Error(), "all-worktrees") {
			t.Fatalf("missing scope acknowledgement: %v", err)
		}
		if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) || !reflect.DeepEqual(common, policyCommonBytes(t, a)) {
			t.Fatal("scope rejection mutated state")
		}
	}
	check(false)
	stop := errors.New("pending")
	if _, err := activatePolicyWithStep(a, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
		if at == "after-common-policy-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("pending baseline: %v", err)
	}
	check(true)
	if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true, Resume: true}); err != nil {
		t.Fatal(err)
	}
	if result, err := ActivatePolicy(b, raw, PolicyActivationOptions{}); err != nil || !result.Replayed {
		t.Fatalf("completed replay needs no all-worktrees: %+v %v", result, err)
	}
}

func TestPolicyCompletedCleanupDiagnostic(t *testing.T) {
	operation, cleanup := errors.New("board unlock"), errors.New("common unlock")
	for _, pair := range [][2]error{{operation, nil}, {nil, cleanup}, {operation, cleanup}} {
		err := finishPolicyActivation(PolicyActivationResult{Status: "completed"}, pair[0], pair[1])
		if err == nil || !strings.Contains(err.Error(), "may already be completed") {
			t.Fatalf("completion uncertainty missing: %v", err)
		}
		for _, cause := range pair {
			if cause != nil && !errors.Is(err, cause) {
				t.Fatalf("lost cause %v: %v", cause, err)
			}
		}
	}
	if err := finishPolicyActivation(PolicyActivationResult{Status: "completed"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := finishPolicyActivation(PolicyActivationResult{}, nil, cleanup); !errors.Is(err, cleanup) || strings.Contains(err.Error(), "completed") {
		t.Fatalf("unpublished operation diagnosed as complete: %v", err)
	}
}
