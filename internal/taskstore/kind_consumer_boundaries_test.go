package taskstore

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestKindConsumerPlanIdentityAndParkingDirection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ id, source, target, status string }{
		{"PLAN-1", "backend/plan/auth/api/PLAN-1.md", "backend/todo/auth/api/PLAN-1.md", "pending"},
		{"TASK-1", "backend/manual/auth/api/TASK-1.md", "backend/plan/auth/api/TASK-1.md", "pending"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			writeModuleCard(t, board, tc.source, tc.id, tc.status)
			if _, err := ActivatePolicy(board, kindConsumerPolicy(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
				t.Fatal(err)
			}
			req := kindConsumerMove(t, board, tc.id, tc.source, tc.target, 'c')
			if _, err := Relocate(board, req, true); err != nil {
				t.Fatal(err)
			}
			entries, err := List(board)
			if err != nil || len(entries) != 1 || entries[0].Card.ID != tc.id || entries[0].Path != tc.target {
				t.Fatalf("identity/path changed: %+v %v", entries, err)
			}
			if kindConsumerReady(t, board, tc.id) {
				t.Fatal("kind identity or kind location became executable")
			}
			before := boardBytes(t, board)
			if _, err := Claim(board, ClaimRequest{ID: tc.id, Owner: "consumer", Token: testToken}); err == nil {
				t.Fatal("kind identity/location became claimable")
			}
			reverse := kindConsumerMove(t, board, tc.id, tc.target, "backend/manual/auth/api/"+filepath.Base(tc.target), 'd')
			if _, err := Relocate(board, reverse, true); err == nil || !strings.Contains(err.Error(), "edge is not explicitly allowed") {
				t.Fatalf("parking destination accepted: %v", err)
			}
			if !reflectEqualBoard(before, boardBytes(t, board)) {
				t.Fatal("rejected claim/parking return mutated board")
			}
		})
	}
}

func TestKindConsumerSameZoneRequiresRepair(t *testing.T) {
	t.Parallel()
	board, repair := moduleRepairFixture(t)
	req := kindConsumerMove(t, board, repair.ID, repair.Path, repair.Path, 'e')
	before := boardBytes(t, board)
	if _, err := Relocate(board, req, true); err == nil || !strings.Contains(err.Error(), "edge is not explicitly allowed") {
		t.Fatalf("same-zone relocation accepted: %v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("same-zone rejection changed board")
	}
	result, err := RepairStatus(board, repair, true)
	if err != nil || !result.Changed || result.Path != repair.Path {
		t.Fatalf("explicit same-zone repair failed: %+v %v", result, err)
	}
}
