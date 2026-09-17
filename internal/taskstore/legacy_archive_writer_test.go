package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyArchiveWriterFixture(t *testing.T) (string, LegacyArchiveRequest, []byte) {
	t.Helper()
	dir, normal, _ := archiveWriterFixture(t)
	if err := os.MkdirAll(filepath.Join(dir, "_archive", "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, normal.Source), filepath.Join(dir, "_archive", "done", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	archived, err := os.ReadFile(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	return dir, LegacyArchiveRequest{ArchiveRequest: ArchiveRequest{
		ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("d", 32), Source: "_archive/done/TASK-1.md",
		ExpectedSHA256: bytesDigest(archived), Operation: "legacy-adoption", Assertion: "operator verified the historical archive",
		Rules: []byte(archiveCompletionRulesFixture),
	}, ExpectedMode: uint32(info.Mode().Perm())}, archived
}

func TestAdoptLegacyArchiveLocalPreservesCardAndRecordsExplicitObservation(t *testing.T) {
	dir, req, raw := legacyArchiveWriterFixture(t)
	result, err := AdoptLegacyArchive(dir, req, true)
	if err != nil || result.Status != "completed" || result.Source != req.Source || result.Target != req.Source || result.CompletionEligible {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, req.Source))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("legacy card changed: %v", err)
	}
	j, err := loadArchiveJournal(func() *os.Root {
		r, e := os.OpenRoot(dir)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { r.Close() })
		return r
	}())
	if err != nil || len(j.Records) != 1 || j.Records[0].State != "completed" || j.Records[0].Completion != nil {
		t.Fatalf("journal=%+v err=%v", j, err)
	}
	replay, err := AdoptLegacyArchive(dir, req, true)
	if err != nil || replay.RequestID != result.RequestID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestAdoptLegacyArchiveApprovedTaskPublishesCompletionBinding(t *testing.T) {
	dir, req, _ := legacyArchiveWriterFixture(t)
	req.ApproveCompletion = true
	result, err := AdoptLegacyArchive(dir, req, true)
	if err != nil || !result.CompletionEligible {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAdoptLegacyArchiveSharedPreservesCommonOwnership(t *testing.T) {
	board, _, normal := archiveProcessFixture(t, true)
	target := filepath.Join(board, "_archive", "done", "TASK-1.md")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(board, normal.Source), target); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	req := LegacyArchiveRequest{ArchiveRequest: ArchiveRequest{
		ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("f", 32), Source: "_archive/done/TASK-1.md",
		ExpectedSHA256: bytesDigest(raw), Operation: "legacy-adoption", Assertion: "operator verified the historical archive",
		Rules: []byte(archiveCompletionRulesFixture),
	}, ExpectedMode: uint32(info.Mode().Perm())}
	result, err := AdoptLegacyArchive(board, req, true)
	if err != nil || result.Status != "completed" || result.Target != req.Source {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestAdoptLegacyArchiveRejectsModeAndApprovalChangesBeforeAdoption(t *testing.T) {
	dir, req, _ := legacyArchiveWriterFixture(t)
	before := boardBytes(t, dir)
	wrongMode := req
	wrongMode.ExpectedMode = req.ExpectedMode ^ 0o100
	if _, err := AdoptLegacyArchive(dir, wrongMode, true); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("wrong mode error=%v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("wrong mode changed board")
	}
	approved := req
	approved.ApproveCompletion = true
	if _, err := AdoptLegacyArchive(dir, approved, true); err != nil {
		t.Fatalf("approved adoption=%v", err)
	}
}

func TestRecoverLegacyArchiveAfterJournalInterruption(t *testing.T) {
	dir, req, raw := legacyArchiveWriterFixture(t)
	stop := errors.New("stop at after-archive-journal")
	if _, err := legacyArchiveWithStep(dir, req, true, false, func(point string) error {
		if point == "after-archive-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("interruption=%v", err)
	}
	result, err := RecoverLegacyArchive(dir, req)
	if err != nil || result.Status != "completed" {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(dir, req.Source))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("recovered card changed: %v", err)
	}
}
