package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func aliasBoard(t *testing.T, id string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	raw := "---\nid: " + id + "\ntitle: alias\nstatus: pending\n---\n\n| **Status** | [ ] Pending |\n"
	if err := os.WriteFile(filepath.Join(root, "todo", id+".md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFilesystemAliasClaimsPreserveRawReplayIdentity(t *testing.T) {
	t.Parallel()
	root := aliasBoard(t, "TASK-01")
	first := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := Claim(root, first); err != nil {
		t.Fatalf("alias claim: %v", err)
	}
	if _, err := Claim(root, ClaimRequest{ID: "TASK-01", Owner: "worker", Token: "abcdefabcdefabcdefabcdefabcdefab"}); err == nil {
		t.Fatal("new alias token bypassed held claim")
	}
	if _, err := Claim(root, ClaimRequest{ID: "TASK-01", Owner: "worker", Token: testToken}); err == nil {
		t.Fatal("raw token replay accepted under alias")
	}
	if _, err := Release(root, ClaimRequest{ID: "TASK-01", Owner: "worker", Token: testToken}); err == nil {
		t.Fatal("release accepted wrong raw receipt ID")
	}
	if got, err := Release(root, first); err != nil || got.ID != "TASK-1" {
		t.Fatalf("original release = %#v, %v", got, err)
	}
}

func TestFilesystemAliasTransitionPreservesCardBytes(t *testing.T) {
	t.Parallel()
	root := aliasBoard(t, "TASK-01")
	req := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: strings.Repeat("a", 32)}
	if _, err := Claim(root, req); err != nil {
		t.Fatal(err)
	}
	transition := TransitionRequest{ID: "TASK-1", Owner: req.Owner, Token: req.Token, RequestID: strings.Repeat("b", 32), From: "todo", To: "doing"}
	result, err := Transition(root, transition)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "doing", "TASK-01.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "id: TASK-01") {
		t.Fatal("transition rewrote card identity")
	}
	replay, err := Transition(root, transition)
	if err != nil || replay != result {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	transition.ID = "TASK-01"
	if _, err := Transition(root, transition); err == nil {
		t.Fatal("raw request alias replay accepted")
	}
}

func TestFilesystemKindCardsAreNotReadyOrDoneDependencies(t *testing.T) {
	t.Parallel()
	root := aliasBoard(t, "PLAN-1")
	if err := os.WriteFile(filepath.Join(root, "todo", "TASK-2.md"), []byte("---\nid: TASK-2\ntitle: blocked\nstatus: pending\ndepends-on: [TASK-3]\n---\n\n| **Status** | [ ] Pending |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plan", "TASK-3.md"), []byte("---\nid: TASK-3\ntitle: plan\nstatus: done\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range ready {
		if entry.Card.ID == "PLAN-1" || entry.Card.ID == "TASK-2" {
			t.Fatalf("kind card entered ready: %#v", ready)
		}
	}
}

func TestIdentityAliasesNormalizeForReadyAndHeldConflicts(t *testing.T) {
	t.Parallel()
	if !sameIdentity("TASK-01", "TASK-1") || !isWorkTask("TASK-01") || isWorkTask("PLAN-1") {
		t.Fatal("identity classification mismatch")
	}
	entries := []Entry{
		{Path: "todo/TASK-01.md", Card: card.View{ID: "TASK-01", Status: "pending", DependsOn: []string{"TASK-2"}}},
		{Path: "done/completed.md", Card: card.View{ID: "TASK-2", Status: "done"}},
	}
	ready, err := readyLocked(entries, claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{{ID: "TASK-1", Owner: "w", Token: testToken, Status: "held"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("held alias was ready: %#v", ready)
	}
	if _, err := validateClaims(claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{{ID: "TASK-1", Owner: "a", Token: testToken, Status: "held"}, {ID: "TASK-01", Owner: "b", Token: "abcdefabcdefabcdefabcdefabcdefab", Status: "held"}}}, entries); err == nil {
		t.Fatal("numeric alias double-held accepted")
	}
}

func TestIdentityKindsNeverSatisfyWorkflow(t *testing.T) {
	t.Parallel()
	entries := []Entry{
		{Path: "todo/PLAN-1.md", Card: card.View{ID: "PLAN-1", Status: "pending"}},
		{Path: "todo/TASK-2.md", Card: card.View{ID: "TASK-2", Status: "pending", DependsOn: []string{"TASK-3"}}},
		{Path: "plan/TASK-3.md", Card: card.View{ID: "TASK-3", Status: "done"}},
	}
	ready, err := readyLocked(entries, claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range ready {
		if entry.Card.ID == "PLAN-1" || entry.Card.ID == "TASK-2" {
			t.Fatalf("kind entered ready: %#v", ready)
		}
	}
}
