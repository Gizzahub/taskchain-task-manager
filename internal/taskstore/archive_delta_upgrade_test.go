package taskstore

import (
	"strings"
	"testing"
)

func TestArchiveDeltaUpgradeFromProtocol3ResumesCommonFirst(t *testing.T) {
	t.Parallel()
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	req := archiveSessionRequest()
	s, err := openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.adoptArchive(true, nil); err != nil {
		t.Fatal(err)
	}
	// Persist a fully adopted old-version fixture with no pending requests.
	s.transitions.StorageProtocol = 3
	if err := publishTransitionJournal(s.root, s.transitions); err != nil {
		t.Fatal(err)
	}
	old := *s.shared.state
	old.StorageProtocol = 3
	if err := s.shared.saveStorageState(old); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	if err := s.adoptArchive(false, nil); err == nil || !strings.Contains(err.Error(), "requires --adopt") {
		t.Fatalf("implicit upgrade err=%v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) || s.shared.state.StorageProtocol != 3 {
		t.Fatal("refused upgrade changed state")
	}
	if err := s.adoptArchive(true, repairStopAt("after-archive-common-protocol")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-common-protocol") {
		t.Fatalf("common-first boundary err=%v", err)
	}
	if s.transitions.StorageProtocol != 3 || s.shared.state.StorageProtocol != 4 {
		t.Fatal("fixture did not stop between common and local barriers")
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = openArchiveSession(board, req)
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := s.adoptArchive(true, nil); err != nil {
		t.Fatal(err)
	}
	if s.transitions.StorageProtocol != 4 || s.shared.state.StorageProtocol != 4 {
		t.Fatal("upgrade retry did not preserve both permanent barriers")
	}
}
