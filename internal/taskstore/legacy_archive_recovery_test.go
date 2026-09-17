package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyCommonPath(t *testing.T, board string, req LegacyArchiveRequest) string {
	t.Helper()
	s, err := openArchiveSession(board, req.ArchiveRequest)
	if err != nil {
		t.Fatal(err)
	}
	if s.shared == nil || s.shared.location == nil {
		_ = s.close()
		t.Fatal("shared archive session has no common location")
	}
	p := filepath.Join(s.shared.root.Name(), sharedStateFile)
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLegacyArchiveRecoveryCutpointsAndExactReplay(t *testing.T) {
	for _, point := range []string{"after-archive-common-pending", "after-archive-journal", "after-archive-receipt"} {
		t.Run(point, func(t *testing.T) {
			dir, req, raw := legacyArchiveWriterFixture(t)
			stop := errors.New("pause legacy archive")
			_, err := legacyArchiveWithStep(dir, req, true, false, func(at string) error {
				if at == point {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("cutpoint %s not reached: %v", point, err)
			}
			if point != "after-archive-receipt" {
				got, readErr := os.ReadFile(filepath.Join(dir, req.Source))
				if readErr != nil || !bytes.Equal(got, raw) {
					t.Fatalf("interrupted archive changed source: %v", readErr)
				}
			}
			if point == "after-archive-common-pending" {
				if _, err := RecoverLegacyArchive(dir, req); err == nil {
					t.Fatal("common-only pending archive unexpectedly completed")
				}
				if _, err := AdoptLegacyArchive(dir, req, true); err != nil {
					t.Fatalf("exact adopt retry after common-only interruption: %v", err)
				}
				return
			}
			result, err := RecoverLegacyArchive(dir, req)
			if err != nil || result.Status != "completed" {
				t.Fatalf("recover=%+v err=%v", result, err)
			}
			if got, err := os.ReadFile(filepath.Join(dir, req.Source)); err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("recovered card changed: %v", err)
			}
		})
	}
}

func TestLegacyArchiveChangedApprovalOrModeLeavesPendingBytes(t *testing.T) {
	dir, req, _ := legacyArchiveWriterFixture(t)
	commonPath := ""
	stop := errors.New("pause common pending")
	_, err := legacyArchiveWithStep(dir, req, true, false, func(point string) error {
		if point == "after-archive-journal" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("setup=%v", err)
	}
	localBefore := boardBytes(t, dir)
	journalBefore, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil {
		t.Fatal(err)
	}
	wrong := req
	wrong.ExpectedMode ^= 0o100
	if _, err := RecoverLegacyArchive(dir, wrong); err == nil {
		t.Fatal("wrong mode accepted")
	}
	if !reflectEqualBoard(localBefore, boardBytes(t, dir)) {
		t.Fatal("wrong mode changed board")
	}
	journalAfter, err := os.ReadFile(filepath.Join(dir, archivesFile))
	if err != nil || !bytes.Equal(journalBefore, journalAfter) {
		t.Fatalf("wrong mode changed local journal: %v", err)
	}
	if _, err := RecoverLegacyArchive(dir, req); err != nil {
		t.Fatalf("exact local recovery: %v", err)
	}

	// The local fixture has no common journal; a fresh shared fixture verifies
	// the same preflight against the common pending bytes.
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
	sharedReq := LegacyArchiveRequest{ArchiveRequest: ArchiveRequest{ID: "TASK-1", Owner: "worker", Token: "", RequestID: strings.Repeat("b", 32), Source: "_archive/done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "legacy-adoption", Assertion: "operator verified the historical archive", Rules: []byte(archiveCompletionRulesFixture)}, ExpectedMode: uint32(info.Mode().Perm())}
	commonPath = legacyCommonPath(t, board, sharedReq)
	if _, err := legacyArchiveWithStep(board, sharedReq, true, false, func(point string) error {
		if point == "after-archive-common-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("shared setup=%v", err)
	}
	sharedBefore := boardBytes(t, board)
	commonBefore, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	sharedPath := filepath.Join(board, sharedReq.Source)
	changedMode := os.FileMode(sharedReq.ExpectedMode ^ 0o100)
	if err := os.Chmod(sharedPath, changedMode); err != nil {
		t.Fatal(err)
	}
	changedInfo, err := os.Stat(sharedPath)
	if err != nil || changedInfo.Mode().Perm() != changedMode || uint32(changedInfo.Mode().Perm()) == sharedReq.ExpectedMode {
		t.Fatalf("mode mutation did not change fixture: %v", err)
	}
	mutatedModeBoard := boardBytes(t, board)
	mutatedModeCommon, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverLegacyArchive(board, sharedReq); err == nil || !strings.Contains(err.Error(), "mode") {
		t.Fatalf("changed shared mode recovery error: %v", err)
	}
	if !reflectEqualBoard(mutatedModeBoard, boardBytes(t, board)) || !bytes.Equal(mutatedModeCommon, mustReadFile(t, commonPath)) {
		t.Fatal("changed shared mode modified state")
	}
	if err := os.Chmod(sharedPath, os.FileMode(sharedReq.ExpectedMode)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sharedPath, append(append([]byte{}, raw...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	mutatedLocal := boardBytes(t, board)
	mutatedCommon, err := os.ReadFile(commonPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverLegacyArchive(board, sharedReq); err == nil {
		t.Fatal("changed shared source resumed")
	}
	if !reflectEqualBoard(mutatedLocal, boardBytes(t, board)) || !bytes.Equal(mutatedCommon, mustReadFile(t, commonPath)) {
		t.Fatal("changed shared source modified state")
	}
	if err := os.WriteFile(sharedPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	wrongShared := sharedReq
	wrongShared.ApproveCompletion = true
	if _, err := RecoverLegacyArchive(board, wrongShared); err == nil {
		t.Fatal("changed approval resumed shared pending")
	}
	commonAfter, err := os.ReadFile(commonPath)
	if err != nil || !bytes.Equal(commonBefore, commonAfter) {
		t.Fatalf("changed approval modified common state: %v", err)
	}
	if !reflectEqualBoard(sharedBefore, boardBytes(t, board)) {
		t.Fatal("changed approval modified local state")
	}
	wrongMode := sharedReq
	wrongMode.ExpectedMode ^= 0o100
	if _, err := RecoverLegacyArchive(board, wrongMode); err == nil {
		t.Fatal("changed mode resumed shared pending")
	}
	if !reflectEqualBoard(sharedBefore, boardBytes(t, board)) {
		t.Fatal("changed mode modified local state")
	}
	if !bytes.Equal(commonBefore, mustReadFile(t, commonPath)) {
		t.Fatal("changed mode modified common state")
	}
	if _, err := RecoverLegacyArchive(board, sharedReq); err != nil {
		t.Fatalf("exact shared recovery: %v", err)
	}
}

func TestLegacyArchiveRecoveryRejectsAncestorSymlinkAndChangedSource(t *testing.T) {
	dir, req, raw := legacyArchiveWriterFixture(t)
	stop := errors.New("pause journal")
	if _, err := legacyArchiveWithStep(dir, req, true, false, func(point string) error {
		if point == "after-archive-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("setup=%v", err)
	}
	before := boardBytes(t, dir)
	path := filepath.Join(dir, req.Source)
	if err := os.WriteFile(path, append(append([]byte{}, raw...), '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	mutated := boardBytes(t, dir)
	if reflectEqualBoard(before, mutated) {
		t.Fatal("source mutation did not reach the fixture")
	}
	if _, err := RecoverLegacyArchive(dir, req); err == nil || !strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Fatalf("changed source accepted: %v", err)
	}
	if !reflectEqualBoard(mutated, boardBytes(t, dir)) {
		t.Fatal("source refusal changed board")
	}
	// Restore the exact card, then replace an internal ancestor with a symlink.
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(dir, "_archive", "done")
	if err := os.Rename(ancestor, ancestor+".real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "_archive", "done.real"), ancestor); err != nil {
		t.Fatal(err)
	}
	symlinkBefore := boardBytes(t, dir)
	if _, err := RecoverLegacyArchive(dir, req); err == nil {
		t.Fatal("ancestor symlink accepted")
	}
	if !reflectEqualBoard(symlinkBefore, boardBytes(t, dir)) {
		t.Fatal("ancestor refusal changed board")
	}
}

func TestLegacyArchiveReplayAfterClaimRelease(t *testing.T) {
	dir, _, _ := archiveWriterFixture(t)
	if err := os.Rename(filepath.Join(dir, "done", "TASK-1.md"), filepath.Join(dir, "todo", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "_archive", "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo", "TASK-1.md"), filepath.Join(dir, "_archive", "done", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "_archive", "done", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	req := LegacyArchiveRequest{ArchiveRequest: ArchiveRequest{ID: "TASK-1", Owner: "worker", Token: testToken, RequestID: strings.Repeat("c", 32), Source: "_archive/done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "legacy-adoption", Assertion: "operator verified the historical archive", Rules: []byte(archiveCompletionRulesFixture)}, ExpectedMode: uint32(info.Mode().Perm())}
	if _, err := AdoptLegacyArchive(dir, req, true); err != nil {
		t.Fatal(err)
	}
	if _, err := Release(dir, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := AdoptLegacyArchive(dir, req, true); err != nil {
		t.Fatal("replay after release:", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("released historical replay changed board")
	}
}
