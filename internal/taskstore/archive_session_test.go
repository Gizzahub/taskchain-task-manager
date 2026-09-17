package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func archiveSessionRequest() ArchiveRequest {
	return ArchiveRequest{ID: "TASK-001", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "done/TASK-001.md", ExpectedSHA256: strings.Repeat("0", 64), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
}

func TestArchiveSessionExplicitAdoptionAndMissingJournal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	s, err := openArchiveSession(dir, archiveSessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.adoptArchive(false, nil); err == nil {
		t.Fatal("implicit adoption accepted")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("refused adoption changed board")
	}
	s, err = openArchiveSession(dir, archiveSessionRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.adoptArchive(true, nil); err != nil {
		t.Fatal(err)
	}
	if s.transitions.StorageProtocol != 4 {
		t.Fatal("protocol not adopted")
	}
	if err := s.root.Remove(archivesFile); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := openArchiveSession(dir, archiveSessionRequest()); err == nil {
		reopened.close()
		t.Fatal("missing adopted journal recreated")
	}
}

func TestArchiveSessionAdoptionBoundaryResume(t *testing.T) {
	for _, point := range []string{"after-relocation-empty-journal", "after-relocation-common-protocol", "after-relocation-local-protocol", "after-archive-empty-journal", "after-archive-common-protocol", "after-archive-local-protocol"} {
		t.Run(point, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tasks")
			if err := Init(dir); err != nil {
				t.Fatal(err)
			}
			s, err := openArchiveSession(dir, archiveSessionRequest())
			if err != nil {
				t.Fatal(err)
			}
			err = s.adoptArchive(true, repairStopAt(point))
			if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			s, err = openArchiveSession(dir, archiveSessionRequest())
			if err != nil {
				t.Fatal("resume open:", err)
			}
			if err := s.adoptArchive(true, nil); err != nil {
				t.Fatal("resume:", err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(dir); err != nil {
				t.Fatal("ordinary reader after adoption:", err)
			}
		})
	}
}

func TestArchiveSessionSharedAdoptionResume(t *testing.T) {
	for _, point := range []string{"after-archive-empty-journal", "after-archive-common-protocol", "after-archive-local-protocol"} {
		t.Run(point, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			s, err := openArchiveSession(board, archiveSessionRequest())
			if err != nil {
				t.Fatal(err)
			}
			err = s.adoptArchive(true, repairStopAt(point))
			if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			s, err = openArchiveSession(board, archiveSessionRequest())
			if err != nil {
				t.Fatal(err)
			}
			if err := s.adoptArchive(true, nil); err != nil {
				t.Fatal(err)
			}
			if s.shared.state.StorageProtocol != 4 || s.archives.Namespace != s.shared.state.NamespaceID {
				t.Fatal("common archive binding not adopted")
			}
			if err := s.close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Ready(other); err != nil {
				t.Fatal("other modern participant:", err)
			}
		})
	}
}

func TestArchiveSessionRejectsOtherPendingStorage(t *testing.T) {
	dir, req := relocationBoardFixture(t)
	_, err := relocateWithStep(dir, req, true, false, repairStopAt("after-relocation-journal"))
	if err == nil || !strings.Contains(err.Error(), "stop at") {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if s, err := openArchiveSession(dir, archiveSessionRequest()); err == nil {
		s.close()
		t.Fatal("archive bypassed pending relocation")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("blocked archive changed board")
	}
	if _, err := os.Stat(filepath.Join(dir, archivesFile)); !os.IsNotExist(err) {
		t.Fatalf("unexpected archive journal: %v", err)
	}
}

func TestArchiveSessionSharedReservationRecovery(t *testing.T) {
	t.Run("legacy-full-target", func(t *testing.T) { archiveSessionReservationRecovery(t, 3) })
	t.Run("delta", func(t *testing.T) { archiveSessionReservationRecovery(t, 4) })
}

func archiveSessionReservationRecovery(t *testing.T, protocol int) {
	t.Helper()
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	req, raw, policy := archiveRequestFixture(t)
	s, err := openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.adoptArchive(true, nil); err != nil {
		t.Fatal(err)
	}
	// Construct the persisted pre-upgrade fixture without changing production's
	// explicit upgrade contract. Both old and new pending formats must recover.
	s.transitions.StorageProtocol = protocol
	if err := publishTransitionJournal(s.root, s.transitions); err != nil {
		t.Fatal(err)
	}
	state := *s.shared.state
	state.StorageProtocol = protocol
	if err := s.shared.saveStorageState(state); err != nil {
		t.Fatal(err)
	}
	rec, err := prepareArchiveRecord(req, raw, 0644, policy, s.archives.BoardPath, s.archives.Namespace, nil)
	if err != nil {
		t.Fatal(err)
	}
	target := s.archives
	target.Records = []archiveRecord{rec}
	if err := s.reserveArchive(target, req); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(other, CreateRequest{Title: "blocked"}); err == nil || !strings.Contains(err.Error(), "pending archive") {
		t.Fatalf("other writer bypassed: %v", err)
	}
	s, err = openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	wrong := req
	wrong.Owner = "another-worker"
	before := boardBytes(t, board)
	if err := s.resumeArchive(wrong); err == nil {
		t.Fatal("different request resumed")
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("wrong recovery changed board")
	}
	if err := s.resumeArchive(req); err != nil {
		t.Fatal(err)
	}
	if len(s.archives.Records) != 1 || s.archives.Records[0].State != "pending" {
		t.Fatal("reservation not restored")
	}
	if err := s.clearArchive(req); err == nil || !strings.Contains(err.Error(), "not durable") {
		t.Fatalf("early clear=%v", err)
	}
	if s.shared.state.PendingArchiveDelta == nil && s.shared.state.PendingArchive == nil {
		t.Fatal("early clear erased reservation")
	}
	done := completedArchiveJournal(s.archives)
	if err := saveArchiveJournal(s.root, done, false); err != nil {
		t.Fatal(err)
	}
	if err := s.resumeArchive(req); err != nil {
		t.Fatal("completed retry:", err)
	}
	if err := s.clearArchive(req); err != nil {
		t.Fatal(err)
	}
	if s.shared.state.PendingArchiveDelta != nil || s.shared.state.PendingArchive != nil || s.shared.state.StorageProtocol != protocol {
		t.Fatal("completion lost permanent barrier")
	}
}
