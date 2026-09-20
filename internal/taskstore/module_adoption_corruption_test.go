package taskstore

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestModuleAdoptionRejectsRehashedTargetMissingObservedID(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	rawPolicy := moduleAdoptionRaw(t)
	stop := errors.New("stop after pending activation")
	if _, err := activatePolicyWithStep(board, rawPolicy, PolicyActivationOptions{AdoptModules: true}, func(at string) error {
		if at == "after-local-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	policy, err := boardpolicy.Parse(rawPolicy)
	if err != nil {
		t.Fatal(err)
	}
	j := transitionJournal{SchemaVersion: 1, Records: []transitionRecord{}}
	target, err := ledgerBytes(idLedger{SchemaVersion: 2, Reserved: []string{"PLAN-2"}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadPolicyActivation(r)
	if err != nil {
		t.Fatal(err)
	}
	plan := state.Plan
	plan.IDTarget, plan.TargetIDs = target, bytesDigest(target)
	state.Plan = plan
	if err := savePolicyActivation(r, state, false); err != nil {
		t.Fatal(err)
	}
	if err := validateModuleIDPlan(plan, false); err != nil {
		t.Fatalf("baseline target binding invalid: %v", err)
	}
	if err := validateModuleInventory(r, policy, j, plan); err == nil || !strings.Contains(err.Error(), "omits frozen inventory") {
		t.Fatalf("incomplete rehashed target accepted: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	beforeResume := boardBytes(t, board)
	if _, err := ActivatePolicy(board, rawPolicy, PolicyActivationOptions{Resume: true, AdoptModules: true}); err == nil || !strings.Contains(err.Error(), "omits frozen inventory") {
		t.Fatalf("resume accepted incomplete target: %v", err)
	}
	if !reflect.DeepEqual(beforeResume, boardBytes(t, board)) {
		t.Fatal("resume rejection changed board")
	}
}

func TestModuleAdoptionRejectsMalformedOriginalBytesShapes(t *testing.T) {
	t.Parallel()
	base := map[string]json.RawMessage{
		"original":     json.RawMessage(`"e30="`),
		"originalMode": json.RawMessage(`420`),
	}
	for name, original := range map[string]json.RawMessage{
		"numeric array":  json.RawMessage(`[1,2,3]`),
		"null":           json.RawMessage(`null`),
		"invalid base64": json.RawMessage(`"%%%"`),
	} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]json.RawMessage{}
			for key, value := range base {
				fields[key] = value
			}
			fields["original"] = original
			raw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateModuleAdoptionShape(raw); err == nil {
				t.Fatal("malformed original bytes accepted")
			}
		})
	}
}

func TestModuleAdoptionMalformedTargetBytesRejectedWithoutWrite(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	before := boardBytes(t, board)
	validTarget, err := ledgerBytes(idLedger{SchemaVersion: 2, Reserved: []string{"PLAN-2", "TASK-7"}})
	if err != nil {
		t.Fatal(err)
	}
	validPlan := policyActivationPlan{Root: filepath.Clean(board), Snapshot: strings.Repeat("a", 64), TargetJournal: strings.Repeat("b", 64), OriginalIDs: "", TargetIDs: bytesDigest(validTarget), IDTarget: validTarget, ModuleAdoption: &moduleIDAdoption{Original: []byte{}, OriginalMode: 0}}
	if err := validateModuleIDPlan(validPlan, false); err != nil {
		t.Fatalf("valid target rejected: %v", err)
	}
	for _, target := range [][]byte{nil, []byte(`null`), []byte(`[1,2]`), []byte(`"%%%"`)} {
		plan := validPlan
		plan.IDTarget, plan.TargetIDs = target, bytesDigest(target)
		if err := validateModuleIDPlan(plan, false); err == nil {
			t.Fatalf("malformed target accepted: %q", target)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("malformed target changed board")
		}
	}
}
