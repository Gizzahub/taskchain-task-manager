package taskstore

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const relocationPolicyFixture = `schema-version: 2
board-policy:
  relocations:
    - from: todo
      to: [plan]
    - from: plan
      to: [todo]
  kind-status:
    plan: pending
`

func TestRelocationPolicyDoesNotExpandExecutionTransitions(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "work"}); err != nil {
		t.Fatal(err)
	}
	claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := Claim(board, claim); err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("a", 32), From: "todo", To: "plan"}
	before := boardBytes(t, board)
	if _, err := Transition(board, req); err == nil {
		t.Fatal("relocation edge became an execution transition")
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("rejected execution transition changed board")
	}
	req.To = "doing"
	if _, err := Transition(board, req); err != nil {
		t.Fatalf("v2 changed default workflow edges: %v", err)
	}
}

// Policy revisions need a separate durable operation, never implicit overwrite.
func TestRelocationPolicyCannotOverwriteActivatedV1(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{}); err == nil {
		t.Fatal("v2 silently replaced immutable v1 policy")
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("rejected policy replacement changed board")
	}
}
