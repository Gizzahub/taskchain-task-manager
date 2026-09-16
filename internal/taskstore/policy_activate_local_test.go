package taskstore

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestActivateLocalPolicyFreshReplayAndNoResume(t *testing.T) {
	dir := claimBoard(t)
	first, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, nil)
	if err != nil || first.Status != "completed" || first.Replayed || first.Boards != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replayed, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, nil)
	if err != nil || !replayed.Replayed || replayed.AuthorityID != first.AuthorityID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if _, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), true, nil); err != nil {
		t.Fatalf("completed resume should replay: %v", err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	claimsRaw, err := json.Marshal(claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{{ID: "TASK-1", Owner: "worker", Token: strings.Repeat("a", 32), Status: "held"}}})
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.WriteFile(claimsFile, claimsRaw, 0o600); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if result, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, nil); err != nil || !result.Replayed {
		t.Fatalf("historical replay with held claim=%+v err=%v", result, err)
	}
}

func TestActivateLocalPolicyResumesAllPublicationBoundaries(t *testing.T) {
	for _, phase := range []string{"after-local-pending", "after-policy", "after-policy-journal", "after-local-completed"} {
		t.Run(phase, func(t *testing.T) {
			dir := claimBoard(t)
			stop := errors.New("stop")
			if _, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("phase %s not reached: %v", phase, err)
			}
			result, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), true, nil)
			if err != nil || result.Status != "completed" {
				t.Fatalf("resume=%+v err=%v", result, err)
			}
		})
	}
}

func TestActivateLocalPolicyPendingResumeAndNoTransactionResume(t *testing.T) {
	dir := claimBoard(t)
	stop := errors.New("stop")
	if _, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, func(phase string) error {
		if phase == "after-local-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("pending interruption=%v", err)
	}
	if _, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, nil); err == nil || !strings.Contains(err.Error(), "resume") {
		t.Fatal("pending activation accepted without resume")
	}
	if result, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), true, nil); err != nil || result.Status != "completed" {
		t.Fatalf("resume=%+v err=%v", result, err)
	}
	other := filepathPolicy(t)
	if _, err := activateLocalPolicy(claimBoard(t), nil, other, true, nil); err == nil || !strings.Contains(err.Error(), "no recorded") {
		t.Fatal("resume without transaction accepted")
	}
}

func TestActivateLocalPolicyDifferentPolicyLeavesBoardUnchanged(t *testing.T) {
	dir := claimBoard(t)
	if _, err := activateLocalPolicy(dir, nil, boardpolicy.Default(), false, nil); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	other := filepathPolicy(t)
	if _, err := activateLocalPolicy(dir, nil, other, false, nil); err == nil {
		t.Fatal("different policy accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("different policy changed board")
	}
}

func filepathPolicy(t *testing.T) boardpolicy.Policy {
	t.Helper()
	p, err := boardpolicy.New(boardpolicy.Declaration{Zones: []string{"parking"}, ZoneStatus: map[string]string{"parking": "cancelled"}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
