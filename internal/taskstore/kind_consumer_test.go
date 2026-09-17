package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func kindConsumerPolicy(t *testing.T) []byte {
	t.Helper()
	p, err := boardpolicy.New(boardpolicy.Declaration{
		Modules: []string{"backend"}, Zones: []string{"manual"},
		KindStatus: map[string]string{"plan": "done"},
		Relocations: []boardpolicy.Transition{
			{From: "done", To: []string{"plan"}}, {From: "todo", To: []string{"plan"}},
			{From: "plan", To: []string{"todo", "doing", "done"}}, {From: "manual", To: []string{"plan"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func kindConsumerMove(t *testing.T, board, id, source, target string, key byte) RelocationRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(board, source))
	if err != nil {
		t.Fatal(err)
	}
	return RelocationRequest{ID: id, Owner: "consumer", RequestID: strings.Repeat(string(key), 32), Source: source, Target: target, ExpectedSHA256: bytesDigest(raw)}
}

func kindConsumerReady(t *testing.T, board, id string) bool {
	t.Helper()
	entries, err := Ready(board)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if sameIdentity(entry.Card.ID, id) {
			return true
		}
	}
	return false
}

func TestKindConsumerDoneToKindRevokesDependencyCompletion(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	writeModuleCard(t, board, "backend/done/auth/api/TASK-1.md", "TASK-1", "done")
	if _, err := ActivatePolicy(board, kindConsumerPolicy(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	dependent, err := Create(board, CreateRequest{Title: "dependent", Module: "backend", Category: "auth/api", DependsOn: []string{"TASK-1"}})
	if err != nil || !kindConsumerReady(t, board, dependent.Card.ID) {
		t.Fatalf("dependency fixture not ready: %+v %v", dependent, err)
	}
	req := kindConsumerMove(t, board, "TASK-1", "backend/done/auth/api/TASK-1.md", "backend/plan/auth/api/TASK-1.md", 'a')
	if _, err := Relocate(board, req, true); err != nil {
		t.Fatal(err)
	}
	if kindConsumerReady(t, board, dependent.Card.ID) || kindConsumerReady(t, board, req.ID) {
		t.Fatal("kind status done was treated as executable workflow completion")
	}
	before := boardBytes(t, board)
	if _, err := Claim(board, ClaimRequest{ID: dependent.Card.ID, Owner: "consumer", Token: testToken}); err == nil {
		t.Fatal("claim bypassed relocated dependency")
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("blocked dependent claim changed board")
	}
}

func TestKindConsumerHeldClaimPendingReleaseAndIdentity(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, kindConsumerPolicy(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(board, CreateRequest{Title: "held", Module: "backend", Category: "auth/api"})
	if err != nil {
		t.Fatal(err)
	}
	claim := ClaimRequest{ID: entry.Card.ID, Owner: "consumer", Token: testToken}
	if _, err := Claim(board, claim); err != nil {
		t.Fatal(err)
	}
	req := kindConsumerMove(t, board, entry.Card.ID, entry.Path, strings.Replace(entry.Path, "/todo/", "/plan/", 1), 'b')
	before := boardBytes(t, board)
	if _, err := Relocate(board, req, true); err == nil || !strings.Contains(err.Error(), "exact held claim") {
		t.Fatalf("missing claim token accepted: %v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("claim refusal mutated board")
	}
	req.Token = claim.Token
	if _, err := relocateWithStep(board, req, true, false, repairStopAt("after-relocation-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-relocation-journal") {
		t.Fatalf("pending phase not reached: %v", err)
	}
	before = boardBytes(t, board)
	if _, err := Release(board, claim); err == nil {
		t.Fatal("pending relocation allowed release")
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("pending release changed board")
	}
	if _, err := RecoverRelocation(board, req); err != nil {
		t.Fatal(err)
	}
	if _, err := Release(board, claim); err != nil {
		t.Fatal(err)
	}
	entries, err := List(board)
	if err != nil || len(entries) != 1 || entries[0].Path != req.Target || entries[0].Card.ID != entry.Card.ID {
		t.Fatalf("identity or category changed: %+v %v", entries, err)
	}
	if kindConsumerReady(t, board, entry.Card.ID) {
		t.Fatal("kind destination became ready")
	}
}
