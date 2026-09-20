package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func relocationBoardFixture(t *testing.T) (string, RelocationRequest) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(dir, []byte(relocationPolicyFixture), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{Title: "relocate"}); err != nil {
		t.Fatal(err)
	}
	return dir, relocationRequestFor(t, dir)
}

func relocationRequestFor(t *testing.T, dir string) RelocationRequest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "todo/TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	return RelocationRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "todo/TASK-1.md", Target: "plan/TASK-1.md", ExpectedSHA256: bytesDigest(raw)}
}

func TestRelocationWriterRecoveryAndGates(t *testing.T) {
	t.Parallel()
	for _, point := range []string{"after-relocation-empty-journal", "after-relocation-common-protocol", "after-relocation-local-protocol", "after-relocation-common-pending", "after-relocation-journal", "after-stage", "after-target", "after-source", "after-relocation-receipt"} {
		t.Run(point, func(t *testing.T) {
			dir, req := relocationBoardFixture(t)
			_, err := relocateWithStep(dir, req, true, false, repairStopAt(point))
			if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if point == "after-relocation-journal" || point == "after-stage" || point == "after-target" || point == "after-source" {
				assertRepairPendingGates(t, dir, req.repairIdentity())
				if _, err := RepairStatus(dir, req.repairIdentity(), true); err == nil {
					t.Fatal("repair bypassed relocation pending")
				}
			}
			result, err := Relocate(dir, req, true)
			if err != nil || result.Status != "completed" || result.Target != req.Target {
				t.Fatalf("resume=%+v err=%v", result, err)
			}
			before := boardBytes(t, dir)
			if _, err := RecoverRelocation(dir, req); err != nil {
				t.Fatal(err)
			}
			if !reflectEqualBoard(before, boardBytes(t, dir)) {
				t.Fatal("receipt replay changed board")
			}
			if _, err := List(dir); err != nil {
				t.Fatal("completed board still gated:", err)
			}
		})
	}
}

func TestRelocationWriterPreservesRepairReceipts(t *testing.T) {
	t.Parallel()
	dir, req := relocationBoardFixture(t)
	repair := storageRepairRequest(t, dir, 'b')
	if _, err := RepairStatus(dir, repair, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	req.ExpectedSHA256 = bytesDigest(raw)
	if _, err := Relocate(dir, req, true); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverStatusRepair(dir, repair); err != nil {
		t.Fatal("historical repair replay:", err)
	}
	after, err := os.ReadFile(filepath.Join(dir, repairsFile))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("repair receipts changed:", err)
	}
	if _, err := Create(dir, CreateRequest{Title: "still works"}); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil || j.StorageProtocol != 2 {
		t.Fatalf("protocol downgraded: %d %v", j.StorageProtocol, err)
	}
}

func TestRelocationWriterRequiresAdoptionAndAbsentTarget(t *testing.T) {
	t.Parallel()
	dir, req := relocationBoardFixture(t)
	before := boardBytes(t, dir)
	if _, err := Relocate(dir, req, false); err == nil {
		t.Fatal("implicit adoption accepted")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("rejected adoption changed board")
	}
	if err := os.MkdirAll(filepath.Join(dir, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, req.Target), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before = boardBytes(t, dir)
	if _, err := Relocate(dir, req, true); err == nil {
		t.Fatal("preexisting identical target accepted")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("target collision changed board")
	}
}

func TestRelocationWriterSourceReappearingCannotComplete(t *testing.T) {
	t.Parallel()
	dir, req := relocationBoardFixture(t)
	raw, err := os.ReadFile(filepath.Join(dir, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	_, err = relocateWithStep(dir, req, true, false, func(point string) error {
		if point == "after-source" {
			return os.WriteFile(filepath.Join(dir, req.Source), raw, 0o644)
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "reappeared") {
		t.Fatalf("reappearing source accepted: %v", err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadRelocationJournal(r)
	if err != nil || len(j.Records) != 1 || j.Records[0].Kind != "pending" {
		t.Fatalf("conflict incorrectly completed: %+v %v", j, err)
	}
}

func TestRelocationWriterHeldClaimAndHistoricalReplay(t *testing.T) {
	t.Parallel()
	dir, req := relocationBoardFixture(t)
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: testToken}
	if _, err := Claim(dir, claim); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Relocate(dir, req, true); err == nil {
		t.Fatal("held claim bypassed without token")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("failed claim admission changed board")
	}
	req.Token = claim.Token
	if _, err := Relocate(dir, req, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Release(dir, claim); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, req.Target)); err != nil {
		t.Fatal(err)
	}
	before = boardBytes(t, dir)
	if _, err := RecoverRelocation(dir, req); err != nil {
		t.Fatal(err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("historical replay recreated deleted card")
	}
}
