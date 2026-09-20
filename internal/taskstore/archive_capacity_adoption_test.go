package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveCapacityAdoptionPublishesExactPayloadAndPermanentBarrier(t *testing.T) {
	t.Parallel()
	dir, r, before := archiveBoardFixture(t)
	if err := os.Chmod(filepath.Join(dir, archivesFile), 0640); err != nil {
		t.Fatal(err)
	}
	raw, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil {
		t.Fatal(err)
	}
	upgrade := strings.Repeat("a", 32)
	if err := AdoptArchiveCapacity(dir, upgrade); err != nil {
		t.Fatal(err)
	}
	tr, err := loadTransitionsForStorage(r)
	if err != nil || tr.StorageProtocol != 5 {
		t.Fatalf("protocol=%d err=%v", tr.StorageProtocol, err)
	}
	a, err := loadArchiveCapacityAdoption(r)
	if err != nil || a.Phase != "completed" || a.UpgradeID != upgrade {
		t.Fatalf("adoption=%+v err=%v", a, err)
	}
	original, target, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil || !bytes.Equal(original, raw) || mode != 0640 {
		t.Fatalf("payload err=%v mode=%o", err, mode)
	}
	if bytesDigest(target) != a.TargetSHA256 || len(target) != a.TargetLength {
		t.Fatal("payload target binding changed")
	}
	current, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil || !bytes.Equal(current, target) {
		t.Fatalf("published target bytes differ: %v", err)
	}
	currentInfo, err := os.Stat(filepath.Join(dir, archivesFile))
	if err != nil || currentInfo.Mode().Perm() != 0640 {
		t.Fatalf("published target mode=%v err=%v", currentInfo, err)
	}
	got, err := loadArchiveJournalForProtocol(r, 5)
	if err != nil || got.SchemaVersion != 2 || len(got.Records) != len(before.Records) {
		t.Fatalf("schema2 journal=%+v err=%v", got, err)
	}
	if _, err := Ready(dir); err != nil {
		t.Fatalf("schema2 archive gate rejected adopted board: %v", err)
	}
}

func TestArchiveCapacityRecoveryLoadsSchema1OriginalAfterLocalProtocol5(t *testing.T) {
	t.Parallel()
	dir, r, _ := archiveBoardFixture(t)
	original, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("9", 32)
	if _, err := archiveCapacityWithStep(dir, id, true, false, repairStopAt("after-capacity-local-protocol")); err == nil || !strings.Contains(err.Error(), "stop at after-capacity-local-protocol") {
		t.Fatalf("cutpoint err=%v", err)
	}
	tr, err := loadTransitionsForStorage(r)
	if err != nil || tr.StorageProtocol != 5 {
		t.Fatalf("protocol=%d err=%v", tr.StorageProtocol, err)
	}
	current, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("schema1 original changed before recovery: %v", err)
	}
	result, err := ArchiveCapacity(dir, id, false, true)
	if err != nil || result.Status != "completed" {
		t.Fatalf("resume=%+v err=%v", result, err)
	}
	a, err := loadArchiveCapacityAdoption(r)
	if err != nil {
		t.Fatal(err)
	}
	_, target, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	current, err = os.ReadFile(filepath.Join(dir, archivesFile))
	info, statErr := os.Stat(filepath.Join(dir, archivesFile))
	if err != nil || statErr != nil || !bytes.Equal(current, target) || uint32(info.Mode().Perm()) != mode {
		t.Fatalf("recovered target bytes/mode differ read=%v stat=%v", err, statErr)
	}
}

func TestArchiveCapacityIdentityBindingResolvesPendingOriginalAndTarget(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		point  string
		schema int
	}{
		{name: "original", point: "after-capacity-local-protocol", schema: 1},
		{name: "target", point: "after-capacity-target", schema: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, r, _ := archiveBoardFixture(t)
			id := strings.Repeat("1", 32)
			if _, err := archiveCapacityWithStep(dir, id, true, false, repairStopAt(tc.point)); err == nil || !strings.Contains(err.Error(), "stop at "+tc.point) {
				t.Fatalf("cutpoint err=%v", err)
			}
			journal, err := loadArchiveJournalForIdentityBinding(r)
			if err != nil || journal.SchemaVersion != tc.schema {
				t.Fatalf("identity archive schema=%d err=%v", journal.SchemaVersion, err)
			}
		})
	}
}

func TestArchiveCapacityIdentityBindingRejectsIncompleteCompletedAuthority(t *testing.T) {
	t.Parallel()
	t.Run("missing payload", func(t *testing.T) {
		dir, r, _ := archiveBoardFixture(t)
		if err := AdoptArchiveCapacity(dir, strings.Repeat("2", 32)); err != nil {
			t.Fatal(err)
		}
		a, err := loadArchiveCapacityAdoption(r)
		if err != nil {
			t.Fatal(err)
		}
		name, err := archiveCapacityPayloadName(a.PayloadSHA256)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Remove(name); err != nil {
			t.Fatal(err)
		}
		if _, err := loadArchiveJournalForIdentityBinding(r); err == nil {
			t.Fatal("completed identity binding accepted a missing payload")
		}
	})
	t.Run("schema downgrade", func(t *testing.T) {
		dir, r, _ := archiveBoardFixture(t)
		if err := AdoptArchiveCapacity(dir, strings.Repeat("3", 32)); err != nil {
			t.Fatal(err)
		}
		a, err := loadArchiveCapacityAdoption(r)
		if err != nil {
			t.Fatal(err)
		}
		original, _, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, archivesFile), original, os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		if _, err := loadArchiveJournalForIdentityBinding(r); err == nil || !strings.Contains(err.Error(), "not schema 2") {
			t.Fatalf("completed identity binding downgrade err=%v", err)
		}
	})
}

func TestArchiveCapacityRecoveryRejectsThirdBytesWithoutMutation(t *testing.T) {
	t.Parallel()
	dir, r, _ := archiveBoardFixture(t)
	original, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeArchiveJournal(original)
	if err != nil {
		t.Fatal(err)
	}
	j.SchemaVersion = 2
	target, err := archiveCapacityJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := publishArchiveCapacityPayload(r, original, target, 0600)
	if err != nil {
		t.Fatal(err)
	}
	a := archiveCapacityAdoption{SchemaVersion: 1, Phase: "pending", UpgradeID: strings.Repeat("b", 32), BoardPath: j.BoardPath, Namespace: j.Namespace, SourceJournalSchema: 1, TargetJournalSchema: 2, StorageProtocol: 5, JournalMode: 0600, OriginalLength: len(original), OriginalSHA256: bytesDigest(original), TargetLength: len(target), TargetSHA256: bytesDigest(target), PayloadSHA256: digest}
	if err := saveArchiveCapacityAdoption(r, a, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, archivesFile), append(original, ' '), 0600); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if err := AdoptArchiveCapacity(dir, a.UpgradeID); err == nil || !strings.Contains(err.Error(), "third state") {
		t.Fatalf("third state err=%v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("third-state recovery changed board")
	}
}

func TestArchiveCapacityPayloadRejectsModeAndThirdTarget(t *testing.T) {
	t.Parallel()
	_, r, _ := archiveBoardFixture(t)
	original, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeArchiveJournal(original)
	if err != nil {
		t.Fatal(err)
	}
	j.SchemaVersion = 2
	target, err := archiveCapacityJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archiveCapacityPayloadBytes(original, target, 0); err == nil {
		t.Fatal("zero mode accepted")
	}
	third := append([]byte(nil), target...)
	third[len(third)-1] ^= 1
	if _, err := archiveCapacityPayloadBytes(original, third, 0600); err == nil {
		t.Fatal("third target accepted")
	}
}

func TestArchiveCapacityAdoptionAllowsSubsequentArchive(t *testing.T) {
	t.Parallel()
	dir, first, _ := archiveWriterFixture(t)
	if _, err := Archive(dir, first, true); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := AdoptArchiveCapacity(dir, strings.Repeat("c", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-3", Title: "later"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "todo/TASK-3.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: checked\n"), 1)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(dir, "done/TASK-3.md")); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-3", Owner: "worker", RequestID: strings.Repeat("d", 32), Source: "done/TASK-3.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	if _, err := Archive(dir, req, false); err != nil {
		t.Fatal(err)
	}
	got, err := loadArchiveJournalForProtocol(r, 5)
	if err != nil || got.SchemaVersion != 2 || len(got.Records) != 2 || got.Records[1].State != "completed" {
		t.Fatalf("schema2 follow-up=%+v err=%v", got, err)
	}
}

func TestArchiveCapacityAdoptionSharedMarkerHasNoByteAuthority(t *testing.T) {
	t.Parallel()
	board, _, req := archiveProcessFixture(t, true)
	if _, err := Archive(board, req, true); err != nil {
		t.Fatal(err)
	}
	if err := AdoptArchiveCapacity(board, strings.Repeat("e", 32)); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if state.StorageProtocol != 5 || state.PendingArchiveCapacity != nil {
		t.Fatalf("shared marker did not complete: %+v", state)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("payloadSha256")) || bytes.Contains(raw, []byte("originalSha256")) || bytes.Contains(raw, []byte("targetSha256")) {
		t.Fatal("shared state copied capacity recovery authority")
	}
}

func TestArchiveCapacityProtocol5PreservesRepairAndRelocation(t *testing.T) {
	t.Parallel()
	dir, r, _ := archiveBoardFixture(t)
	if _, err := ActivatePolicy(dir, []byte(relocationPolicyFixture), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := AdoptArchiveCapacity(dir, strings.Repeat("7", 32)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "todo/TASK-2.md"))
	if err != nil {
		t.Fatal(err)
	}
	repair := RepairRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("6", 32), Path: "todo/TASK-2.md", ExpectedSHA256: bytesDigest(raw)}
	if _, err := RepairStatus(dir, repair, false); err != nil {
		t.Fatalf("protocol5 repair: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "todo/TASK-2.md"))
	if err != nil {
		t.Fatal(err)
	}
	relocate := RelocationRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("5", 32), Source: "todo/TASK-2.md", Target: "plan/TASK-2.md", ExpectedSHA256: bytesDigest(raw)}
	if _, err := Relocate(dir, relocate, false); err != nil {
		t.Fatalf("protocol5 relocation: %v", err)
	}
	tr, err := loadTransitionsForStorage(r)
	if err != nil || tr.StorageProtocol != 5 {
		t.Fatalf("protocol downgraded=%d err=%v", tr.StorageProtocol, err)
	}
}

func TestArchiveCapacityProtocol5PreservesSharedRepairAndRelocation(t *testing.T) {
	t.Parallel()
	board, _, first := archiveProcessFixture(t, true)
	if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Archive(board, first, true); err != nil {
		t.Fatal(err)
	}
	if err := AdoptArchiveCapacity(board, strings.Repeat("4", 32)); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(board, CreateRequest{ID: "TASK-2", Title: "protocol5 shared"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(board, entry.Path))
	if err != nil {
		t.Fatal(err)
	}
	repair := RepairRequest{ID: entry.Card.ID, Owner: "worker", RequestID: strings.Repeat("3", 32), Path: entry.Path, ExpectedSHA256: bytesDigest(raw)}
	if _, err := RepairStatus(board, repair, false); err != nil {
		t.Fatalf("shared protocol5 repair: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(board, entry.Path))
	if err != nil {
		t.Fatal(err)
	}
	relocate := RelocationRequest{ID: entry.Card.ID, Owner: "worker", RequestID: strings.Repeat("2", 32), Source: entry.Path, Target: "plan/" + filepath.Base(entry.Path), ExpectedSHA256: bytesDigest(raw)}
	if _, err := Relocate(board, relocate, false); err != nil {
		t.Fatalf("shared protocol5 relocation: %v", err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	protocol := s.state.StorageProtocol
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if protocol != 5 {
		t.Fatalf("common protocol downgraded: %d", protocol)
	}
}
