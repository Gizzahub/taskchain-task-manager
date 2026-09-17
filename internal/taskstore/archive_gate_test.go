package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Synthetic adopted state, not a substitute for adoption writer tests.
func archiveBoardFixture(t *testing.T) (string, *os.Root, archiveJournal) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	board, err := canonicalStorageBoard(r)
	if err != nil {
		t.Fatal(err)
	}
	j, rec, b, raw := archiveJournalFixture(t)
	j.BoardPath, rec.BoardPath, b.BoardPath = board, board, board
	rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, &b
	j.Records = []archiveRecord{rec}
	if err := os.MkdirAll(filepath.Join(dir, "_archive/done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, b.Target), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "todo/TASK-2.md"), []byte("---\nid: TASK-2\ntitle: dependent\ndepends-on: [TASK-001]\n---\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ids, err := loadIDs(r)
	if err != nil {
		t.Fatal(err)
	}
	ids.Reserved = []string{"TASK-1", "TASK-2"}
	if err := publishIDs(r, ids, false); err != nil {
		t.Fatal(err)
	}
	if err := saveRepairJournal(r, repairJournal{SchemaVersion: 1, BoardPath: board, Records: []repairRecord{}}, true); err != nil {
		t.Fatal(err)
	}
	if err := saveRelocationJournal(r, relocationJournal{SchemaVersion: 1, BoardPath: board, Records: []relocationRecord{}}, true); err != nil {
		t.Fatal(err)
	}
	if err := saveArchiveJournal(r, j, true); err != nil {
		t.Fatal(err)
	}
	tr, err := loadTransitionsForStorage(r)
	if err != nil {
		t.Fatal(err)
	}
	tr.StorageProtocol = 3
	if err := publishTransitionJournal(r, tr); err != nil {
		t.Fatal(err)
	}
	return dir, r, j
}

func TestArchiveGateReadyClaimTransition(t *testing.T) {
	dir, _, _ := archiveBoardFixture(t)
	ready, err := Ready(dir)
	if err != nil || len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	if len(ready[0].Card.DependsOn) != 1 || ready[0].Card.DependsOn[0] != "TASK-001" {
		t.Fatal("fixture lost its archive dependency")
	}
	claim, err := Claim(dir, ClaimRequest{ID: "TASK-2", Owner: "worker", Token: strings.Repeat("b", 32)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Transition(dir, TransitionRequest{ID: "TASK-2", Owner: "worker", Token: claim.Token, RequestID: strings.Repeat("c", 32), From: "todo", To: "doing"})
	if err != nil {
		t.Fatal(err)
	}
}

func TestArchiveGateReceiptRequiredByConsumers(t *testing.T) {
	dir, r, j := archiveBoardFixture(t)
	j.Records = []archiveRecord{}
	if err := saveArchiveJournal(r, j, false); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(dir)
	if err != nil || len(ready) != 0 {
		t.Fatalf("unbound archive ready=%v err=%v", ready, err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-2", Owner: "worker", Token: strings.Repeat("b", 32)}); err == nil {
		t.Fatal("unbound archive admitted claim")
	}

	dir2, r2, j2 := archiveBoardFixture(t)
	claim, err := Claim(dir2, ClaimRequest{ID: "TASK-2", Owner: "worker", Token: strings.Repeat("b", 32)})
	if err != nil {
		t.Fatal(err)
	}
	j2.Records = []archiveRecord{}
	if err := saveArchiveJournal(r2, j2, false); err != nil {
		t.Fatal(err)
	}
	_, err = Transition(dir2, TransitionRequest{ID: "TASK-2", Owner: "worker", Token: claim.Token, RequestID: strings.Repeat("c", 32), From: "todo", To: "doing"})
	if err == nil || !strings.Contains(err.Error(), "dependency") {
		t.Fatalf("missing dependency receipt transition error=%v", err)
	}
}

func TestArchiveGatePreservesProtocolDuringRepair(t *testing.T) {
	dir, r, _ := archiveBoardFixture(t)
	raw, err := os.ReadFile(filepath.Join(dir, "todo/TASK-2.md"))
	if err != nil {
		t.Fatal(err)
	}
	req := RepairRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("d", 32), Path: "todo/TASK-2.md", ExpectedSHA256: bytesDigest(raw)}
	if _, err := RepairStatus(dir, req, false); err != nil {
		t.Fatal(err)
	}
	j, err := loadTransitions(r)
	if err != nil || j.StorageProtocol != 3 {
		t.Fatalf("protocol=%d err=%v", j.StorageProtocol, err)
	}
}

func TestArchiveGateRelocationAndPolicySnapshot(t *testing.T) {
	dir, r, _ := archiveBoardFixture(t)
	before, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(dir, []byte(relocationPolicyFixture), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	after, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil || string(before) != string(after) {
		t.Fatal("policy activation changed archive journal")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "todo/TASK-2.md"))
	if err != nil {
		t.Fatal(err)
	}
	req := RelocationRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("e", 32), Source: "todo/TASK-2.md", Target: "plan/TASK-2.md", ExpectedSHA256: bytesDigest(raw)}
	if _, err := Relocate(dir, req, false); err != nil {
		t.Fatal(err)
	}
	j, err := loadTransitions(r)
	if err != nil || j.StorageProtocol != 3 {
		t.Fatalf("relocation downgraded protocol=%d err=%v", j.StorageProtocol, err)
	}
	policy, err := policyForJournal(r, j)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := policyActivationSnapshot(r, policy, j)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, archivesFile), 0640); err != nil {
		t.Fatal(err)
	}
	changed, err := policyActivationSnapshot(r, policy, j)
	if err != nil || changed == snapshot {
		t.Fatalf("archive mode not bound: %v", err)
	}
}

func TestArchiveGateRejectsMissingOrUnadoptedJournal(t *testing.T) {
	for _, missing := range []bool{true, false} {
		t.Run(map[bool]string{true: "missing", false: "unadopted"}[missing], func(t *testing.T) {
			dir, r, _ := archiveBoardFixture(t)
			if missing {
				if err := r.Remove(archivesFile); err != nil {
					t.Fatal(err)
				}
			} else {
				tr, err := loadTransitionsForStorage(r)
				if err != nil {
					t.Fatal(err)
				}
				tr.StorageProtocol = 2
				if err := publishTransitionJournal(r, tr); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Ready(dir); err == nil {
				t.Fatal("invalid adoption accepted")
			}
			if _, err := Claim(dir, ClaimRequest{ID: "TASK-2", Owner: "worker", Token: strings.Repeat("b", 32)}); err == nil {
				t.Fatal("claim bypassed archive gate")
			}
		})
	}
}

func TestArchiveGateRejectsPendingAndChangedCard(t *testing.T) {
	for _, pending := range []bool{true, false} {
		t.Run(map[bool]string{true: "pending", false: "changed-card"}[pending], func(t *testing.T) {
			dir, r, j := archiveBoardFixture(t)
			_, _, _, raw := archiveJournalFixture(t)
			if pending {
				j.Records[0].State, j.Records[0].Original, j.Records[0].Patched = "pending", raw, raw
				if err := saveArchiveJournal(r, j, false); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(dir, j.Records[0].Target), append(raw, []byte("changed\n")...), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(dir); err == nil {
				t.Fatal("invalid archive completion accepted")
			}
			if pending {
				source, err := os.ReadFile(filepath.Join(dir, "todo/TASK-2.md"))
				if err != nil {
					t.Fatal(err)
				}
				req := RepairRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("f", 32), Path: "todo/TASK-2.md", ExpectedSHA256: bytesDigest(source)}
				before := boardBytes(t, dir)
				if _, err := RepairStatus(dir, req, false); err == nil || !strings.Contains(err.Error(), "pending") {
					t.Fatalf("repair bypassed pending archive: %v", err)
				}
				if _, err := Relocate(dir, RelocationRequest{ID: req.ID, Owner: req.Owner, RequestID: req.RequestID, Source: req.Path, Target: "plan/TASK-2.md", ExpectedSHA256: req.ExpectedSHA256}, false); err == nil || !strings.Contains(err.Error(), "pending") {
					t.Fatalf("relocation bypassed pending archive: %v", err)
				}
				if !reflectEqualBoard(before, boardBytes(t, dir)) {
					t.Fatal("blocked storage writer changed board")
				}
			}
		})
	}
}
