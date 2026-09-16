package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelocationSharedRecoveryAndOtherBoardGate(t *testing.T) {
	for _, point := range []string{"after-relocation-empty-journal", "after-relocation-common-protocol", "after-relocation-local-protocol", "after-relocation-common-pending", "after-relocation-journal", "after-target", "after-source", "after-relocation-receipt"} {
		t.Run(point, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{AllWorktrees: true}); err != nil {
				t.Fatal(err)
			}
			req := relocationRequestFor(t, board)
			_, err := relocateWithStep(board, req, true, false, repairStopAt(point))
			if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if point == "after-relocation-common-pending" || point == "after-relocation-journal" || point == "after-target" || point == "after-source" || point == "after-relocation-receipt" {
				before := boardBytes(t, other)
				if _, err := Create(other, CreateRequest{Title: "must block"}); err == nil {
					t.Fatal("other worktree bypassed shared pending")
				}
				if !reflectEqualBoard(before, boardBytes(t, other)) {
					t.Fatal("blocked writer changed other board")
				}
				if _, err := RecoverRelocation(board, req); err != nil {
					t.Fatal("recovery:", err)
				}
			} else if _, err := Relocate(board, req, true); err != nil {
				t.Fatal("adoption retry:", err)
			}
			if _, err := List(other); err != nil {
				t.Fatal("other board still blocked:", err)
			}
			// An old-protocol participant can still use repair with the new
			// binary, but must never lower the common protocol back to one.
			repair := storageRepairRequest(t, other, 'b')
			if _, err := RepairStatus(other, repair, true); err != nil {
				t.Fatal("repair in other participant:", err)
			}
			s, release, err := acquireShared(board, false)
			if err != nil {
				t.Fatal(err)
			}
			protocol := s.state.StorageProtocol
			if err := release(); err != nil {
				t.Fatal(err)
			}
			if protocol != 2 {
				t.Fatalf("common protocol downgraded: %d", protocol)
			}
		})
	}
}

func TestRelocationKindIdentityDoesNotBecomeExecutable(t *testing.T) {
	board, _ := relocationBoardFixture(t)
	entry, err := Create(board, CreateRequest{Kind: "plan", Title: "planning"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(board, entry.Path))
	if err != nil {
		t.Fatal(err)
	}
	req := RelocationRequest{ID: entry.Card.ID, Owner: "worker", RequestID: strings.Repeat("a", 32), Source: entry.Path, Target: "todo/" + filepath.Base(entry.Path), ExpectedSHA256: bytesDigest(raw)}
	if _, err := Relocate(board, req, true); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(board)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range ready {
		if sameIdentity(candidate.Card.ID, req.ID) {
			t.Fatal("relocation converted PLAN into executable TASK")
		}
	}
	if _, err := Claim(board, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: testToken}); err == nil {
		t.Fatal("PLAN became claimable")
	}
}
