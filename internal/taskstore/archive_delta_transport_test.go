package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func protocol4DeltaFixture(t *testing.T) (sharedState, archivePendingDelta) {
	t.Helper()
	original, pending, _ := archiveDeltaFixture(t)
	delta, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	state := validSharedV3Fixture(t)
	state.SchemaVersion = 3
	state.Phase = "active"
	state.NamespaceID = delta.Namespace
	state.StorageProtocol = 4
	state.Reserved = []string{"TASK-1"}
	state.PendingArchive = nil
	state.PendingArchiveDelta = &delta
	return state, delta
}

func TestArchiveDeltaTransportProtocol5Schema2AllRecoveryStates(t *testing.T) {
	t.Parallel()
	original, pending, _ := archiveDeltaFixture(t)
	original.SchemaVersion = 2
	delta, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	target := original
	target.Records = []archiveRecord{pending}
	states := []archiveJournal{original, target, completedArchiveJournal(target)}
	state := validSharedV3Fixture(t)
	state.NamespaceID, state.StorageProtocol = original.Namespace, 5
	state.Reserved = unionIDs(state.Reserved, []string{identityKey(pending.ID)})
	state.PendingArchiveDelta = &delta
	if err := validateSharedState(state); err != nil {
		t.Fatalf("schema2 shared delta rejected: %v", err)
	}
	for i, want := range []string{"original", "pending", "completed"} {
		raw, err := archiveJournalWire(states[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := resolveArchivePendingDelta(delta, raw, original.BoardPath, original.Namespace, state.Reserved)
		if err != nil || got.State != want {
			t.Fatalf("state=%s got=%+v err=%v", want, got, err)
		}
		decodedTarget, err := decodeArchiveCapacityJournal(got.Target)
		if err != nil || decodedTarget.SchemaVersion != 2 {
			t.Fatalf("target schema2 err=%v", err)
		}
		decodedCompleted, err := decodeArchiveCapacityJournal(got.Completed)
		if err != nil || decodedCompleted.SchemaVersion != 2 {
			t.Fatalf("completed schema2 err=%v", err)
		}
	}
}

func TestArchiveDeltaTransportProtocol5SharedSessionResolvesAllStates(t *testing.T) {
	t.Parallel()
	board, _, first := archiveProcessFixture(t, true)
	if _, err := Archive(board, first, true); err != nil {
		t.Fatal(err)
	}
	if err := AdoptArchiveCapacity(board, strings.Repeat("6", 32)); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-2", Title: "schema2 shared"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(board, "todo/TASK-2.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: checked\n"), 1)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(board, "done/TASK-2.md")); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("7", 32), Source: "done/TASK-2.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	if _, err := archiveWithStep(board, req, false, false, repairStopAt("after-archive-common-pending")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-common-pending") {
		t.Fatalf("pending boundary err=%v", err)
	}
	s, err := openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(board, archivesFile))
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := s.resolveSharedDelta(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	states := []struct {
		name string
		raw  []byte
	}{{"original", original}, {"pending", resolution.Target}, {"completed", resolution.Completed}}
	for _, state := range states {
		if err := os.WriteFile(filepath.Join(board, archivesFile), state.raw, 0600); err != nil {
			t.Fatal(err)
		}
		s, err := openArchiveSession(board, req)
		if err != nil {
			t.Fatalf("%s open: %v", state.name, err)
		}
		got, resolveErr := s.resolveSharedDelta(req)
		closeErr := s.close()
		if resolveErr != nil || closeErr != nil || got.State != state.name {
			t.Fatalf("%s got=%+v resolve=%v close=%v", state.name, got, resolveErr, closeErr)
		}
	}
}

func TestArchiveDeltaTransportProtocol4WireAndGuards(t *testing.T) {
	t.Parallel()
	state, delta := protocol4DeltaFixture(t)
	if err := validateSharedState(state); err != nil {
		t.Fatalf("valid protocol4 delta rejected: %v", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSharedShape(raw); err != nil {
		t.Fatalf("protocol4 wire rejected: %v", err)
	}
	var decoded sharedState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.StorageProtocol != 4 || decoded.PendingArchiveDelta == nil || decoded.PendingArchiveDelta.ID != delta.ID {
		t.Fatalf("delta wire lost state: %+v", decoded)
	}
	if err := validateSharedState(decoded); err != nil {
		t.Fatalf("decoded protocol4 delta rejected: %v", err)
	}
	for name, mutate := range map[string]func(*sharedState){
		"archive conflict": func(s *sharedState) {
			p := sharedRepairPending{Owner: delta.BoardPath, RequestID: delta.RequestID}
			s.PendingArchive = &p
		},
		"repair conflict":     func(s *sharedState) { s.PendingRepair = &sharedRepairPending{} },
		"relocation conflict": func(s *sharedState) { s.PendingRelocation = &sharedRepairPending{} },
		"protocol downgrade":  func(s *sharedState) { s.StorageProtocol = 3 },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := state
			mutate(&candidate)
			if err := validateSharedState(candidate); err == nil || !strings.Contains(err.Error(), "conflicting") {
				t.Fatalf("protocol4 mutual exclusion err=%v", err)
			}
		})
	}
}

func TestArchiveDeltaTransportRecoveryFromCommonPending(t *testing.T) {
	t.Parallel()
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(board, "todo/TASK-1.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: checked\n"), 1)
	if err := os.MkdirAll(filepath.Join(board, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(board, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("d", 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	if _, err := archiveWithStep(board, req, true, false, repairStopAt("after-archive-common-pending")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-common-pending") {
		t.Fatalf("delta boundary not reached: %v", err)
	}
	stateSession, release, err := acquireSharedArchiveOptions(board, false, false, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	state := *stateSession.state
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if state.StorageProtocol != 4 || state.PendingArchiveDelta == nil {
		t.Fatalf("common pending did not publish protocol4 delta: %+v", state)
	}
	result, err := RecoverArchive(board, req)
	if err != nil || result.Status != "completed" {
		t.Fatalf("delta recovery=%+v err=%v", result, err)
	}
	stateSession, release, err = acquireSharedArchiveOptions(board, false, false, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	state = *stateSession.state
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if state.PendingArchiveDelta != nil {
		t.Fatal("delta reservation remained after recovery")
	}
}

func TestArchiveDeltaTransportMissingLocalJournalIsRestoreOnly(t *testing.T) {
	t.Parallel()
	board, _, req := archiveProcessFixture(t, true)
	if _, err := archiveWithStep(board, req, true, false, repairStopAt("after-archive-common-pending")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-common-pending") {
		t.Fatalf("archive interruption was not reached: %v", err)
	}
	s, err := openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	commonPath := filepath.Join(s.shared.root.Name(), sharedStateFile)
	if s.shared.state.PendingArchiveDelta == nil {
		t.Fatal("fixture has no delta reservation")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	commonBefore, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(board, archivesFile)); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	if _, err := RecoverArchive(board, req); err == nil || !strings.Contains(err.Error(), "journal is missing") {
		t.Fatalf("missing local journal recovery err=%v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("restore-only recovery changed board")
	}
	commonAfter, err := os.ReadFile(commonPath)
	if err != nil || !bytes.Equal(commonBefore, commonAfter) {
		t.Fatalf("restore-only recovery changed common delta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(board, req.Source)); err != nil {
		t.Fatalf("source card was republished or removed: %v", err)
	}
}

func TestArchiveDeltaTransportBlocksGeneralReadersAndWriters(t *testing.T) {
	t.Parallel()
	board, other, req := archiveProcessFixture(t, true)
	if _, err := archiveWithStep(board, req, true, false, repairStopAt("after-archive-common-pending")); err == nil {
		t.Fatal("common delta interruption was not reached")
	}
	before := boardBytes(t, other)
	checks := map[string]func() error{
		"list":   func() error { _, err := List(other); return err },
		"ready":  func() error { _, err := Ready(other); return err },
		"create": func() error { _, err := Create(other, CreateRequest{Title: "blocked"}); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); err == nil || (!strings.Contains(err.Error(), "pending archive") && !strings.Contains(err.Error(), "pending archive delta")) {
				t.Fatalf("pending delta %s err=%v", name, err)
			}
		})
	}
	if !reflectEqualBoard(before, boardBytes(t, other)) {
		t.Fatal("blocked participant changed during pending delta")
	}
}

func TestArchiveDeltaTransportLegacyModeRefusesBeforeLocalRepublish(t *testing.T) {
	t.Parallel()
	board, _, req, _, _ := legacyArchiveProcessFixture(t, true)
	commonPath := legacyCommonPath(t, board, req)
	if _, err := legacyArchiveWithStep(board, req, true, false, repairStopAt("after-archive-common-pending")); err == nil {
		t.Fatal("legacy common delta interruption was not reached")
	}
	localBefore := boardBytes(t, board)
	commonBefore, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"mode", "approval"} {
		wrong := req
		if field == "mode" {
			wrong.ExpectedMode ^= 0100
		} else {
			wrong.ApproveCompletion = !wrong.ApproveCompletion
		}
		if _, err := RecoverLegacyArchive(board, wrong); err == nil || !strings.Contains(err.Error(), "request differs") {
			t.Fatalf("wrong legacy %s accepted: %v", field, err)
		}
	}
	if !reflectEqualBoard(localBefore, boardBytes(t, board)) {
		t.Fatal("wrong legacy mode changed local board")
	}
	commonAfter, err := os.ReadFile(commonPath)
	if err != nil || !bytes.Equal(commonBefore, commonAfter) {
		t.Fatalf("wrong legacy mode changed common state: %v", err)
	}
}
