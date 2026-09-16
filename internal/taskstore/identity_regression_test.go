package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPaddedRequestClaimAndInterruptedRecovery(t *testing.T) {
	dir := aliasBoard(t, "TASK-01")
	claim := ClaimRequest{ID: "TASK-0001", Owner: "worker", Token: testToken}
	if _, err := Claim(dir, claim); err != nil {
		t.Fatalf("padded input rejected: %v", err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "other", Token: strings.Repeat("c", 32)}); err == nil || !strings.Contains(err.Error(), "already claimed") {
		t.Fatalf("identity conflict=%v", err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "todo/TASK-01.md"))
	if err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: "TASK-001", Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("d", 32), From: "todo", To: "doing"}
	stop := errors.New("synthetic interrupted target")
	if _, err := transitionWithStep(dir, req, func(phase string) error {
		if phase == "after-target" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("interruption=%v", err)
	}
	before := boardBytes(t, dir)
	wrong := req
	wrong.ID = "TASK-1"
	if _, err := Recover(dir, wrong); err == nil {
		t.Fatal("changed raw recovery request accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("wrong replay changed pending board")
	}
	if _, err := Recover(dir, req); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "doing/TASK-01.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(original, []byte("[ ] Pending"), []byte("[~] In Progress"), 1)
	if !bytes.Equal(got, want) {
		t.Fatalf("unexpected card rewrite: %q", got)
	}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimResume(dir, ClaimRequest{ID: "TASK-1", Owner: "resumer", Token: strings.Repeat("e", 32)}); err != nil {
		t.Fatalf("alias resume=%v", err)
	}
}

func TestKindInDoneCannotFulfilTaskDependency(t *testing.T) {
	dir := aliasBoard(t, "TASK-1")
	claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := Claim(dir, claim); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "done/PLAN-01.md"), []byte("---\nid: PLAN-01\nstatus: done\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo/TASK-1.md"), []byte("---\nid: TASK-1\ndepends-on: [PLAN-1]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("b", 32), From: "todo", To: "doing"}
	before := boardBytes(t, dir)
	if _, err := Transition(dir, req); err == nil || !strings.Contains(err.Error(), "dependency is not done") {
		t.Fatalf("kind completeddependency=%v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("kind rejection modified board")
	}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(dir)
	if err != nil || len(ready) != 0 {
		t.Fatalf("kind completeddependency ready=%v %v", ready, err)
	}
}

func TestV1LedgerDoesNotGainV2Syntax(t *testing.T) {
	for _, id := range []string{"TASK-0", "TASK-01", "PLAN-1", "ISSUE-1", "BACKLOG-1"} {
		t.Run(id, func(t *testing.T) {
			dir := aliasBoard(t, "TASK-1")
			if err := os.WriteFile(filepath.Join(dir, idsFile), []byte(`{"schemaVersion":1,"reserved":["`+id+`"]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := ReserveIDs(dir, nil, true); err == nil {
				t.Fatal("v1 syntax silently broadened")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("invalid v1 upgraded")
			}
		})
	}
}

func TestNumericAliasCycle(t *testing.T) {
	dir := aliasBoard(t, "TASK-001")
	if err := os.WriteFile(filepath.Join(dir, "todo/TASK-001.md"), []byte("---\nid: TASK-001\ndepends-on: [TASK-2]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo/two.md"), []byte("---\nid: TASK-002\ndepends-on: [TASK-1]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ready(dir); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("alias cycle=%v", err)
	}
}
