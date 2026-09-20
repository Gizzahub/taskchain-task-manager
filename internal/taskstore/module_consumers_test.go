package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestModuleRepairReceiptSurvivesPolicyRevision(t *testing.T) {
	t.Parallel()
	board, req := moduleRepairFixture(t)
	active, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true})
	if err != nil {
		t.Fatal(err)
	}
	first, err := RepairStatus(board, req, true)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}, Zones: []string{"manual"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	revised, err := RevisePolicy(board, raw, PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest})
	if err != nil || revised.Digest == active.Digest {
		t.Fatalf("policy did not change: %+v %v", revised, err)
	}
	after, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil || !bytes.Equal(receipt, after) {
		t.Fatalf("revision rewrote repair history: %v", err)
	}
	before := boardBytes(t, board)
	again, err := RecoverStatusRepair(board, req)
	if err != nil || first != again || !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatalf("historical replay changed state: %+v %v", again, err)
	}
}

func TestModuleContextReferencesAndHistoricalReplay(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	raw := []byte(strings.Replace(testContextBatch, "TASK-1", "TASK-7", 1))
	registered, err := RegisterContext(board, raw)
	if err != nil || registered.ReferenceValidation != "verified" {
		t.Fatalf("module reference not verified: %+v %v", registered, err)
	}
	if err := os.Remove(filepath.Join(board, "backend/todo/TASK-7.md")); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	shown, err := ShowContext(board, registered.Kind, registered.ID, registered.Revision)
	if err != nil || shown.ReferenceValidation != "not_rechecked" || !bytes.Equal(shown.Canonical, registered.Canonical) {
		t.Fatalf("history changed: %+v %v", shown, err)
	}
	if _, err := RegisterContext(board, raw); err != nil {
		t.Fatal(err)
	}
	newRevision := bytes.Replace(raw, []byte(`"revision":1`), []byte(`"revision":2`), 1)
	if _, err := RegisterContext(board, newRevision); err == nil || !strings.Contains(err.Error(), "TASK") {
		t.Fatalf("missing module task admitted: %v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("read/replay or rejected revision mutated board")
	}
}
