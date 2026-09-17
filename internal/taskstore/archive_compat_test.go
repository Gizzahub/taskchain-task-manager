package taskstore

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Use the actual pre-archive executable. This is opt-in because the binary is
// built by the integration harness; the test must prove the old writer stops
// at the storage-protocol barrier rather than merely rejecting the request.
func TestArchiveStorageV2BinaryBarrier(t *testing.T) {
	archiveStorageV2BinaryBarrier(t, false)
}

func TestLegacyAdoptionStorageV2BinaryBarrier(t *testing.T) {
	archiveStorageV2BinaryBarrier(t, true)
}

func TestArchiveStorageV3BinaryBarrier(t *testing.T) {
	archiveStorageV3BinaryBarrier(t, false)
}

func TestLegacyAdoptionStorageV3BinaryBarrier(t *testing.T) {
	archiveStorageV3BinaryBarrier(t, true)
}

func archiveStorageV3BinaryBarrier(t *testing.T, legacy bool) {
	t.Helper()
	binary := os.Getenv("TASKCHAIN_ARCHIVE_V3_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_ARCHIVE_V3_BINARY to storage-v3 executable")
	}
	t.Setenv("TASKCHAIN_ARCHIVE_LEGACY_BINARY", binary)
	archiveStorageV2BinaryBarrier(t, legacy)
}

func archiveStorageV2BinaryBarrier(t *testing.T, legacy bool) {
	t.Helper()
	binary := os.Getenv("TASKCHAIN_ARCHIVE_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_ARCHIVE_LEGACY_BINARY to storage-v2 executable")
	}
	for _, shared := range []bool{false, true} {
		points := []string{"after-archive-local-protocol", "after-archive-receipt"}
		if shared {
			points = append([]string{"after-archive-common-protocol"}, points...)
		}
		for _, point := range points {
			t.Run(strings.Join([]string{map[bool]string{true: "shared", false: "local"}[shared], point}, "/"), func(t *testing.T) {
				board, other, req := archiveProcessFixture(t, shared)
				var legacyReq LegacyArchiveRequest
				if legacy {
					target := filepath.Join(board, "_archive", "done", "TASK-1.md")
					if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(filepath.Join(board, req.Source), target); err != nil {
						t.Fatal(err)
					}
					info, err := os.Stat(target)
					if err != nil {
						t.Fatal(err)
					}
					req.Source, req.Operation, req.Assertion = "_archive/done/TASK-1.md", "legacy-adoption", "synthetic legacy verification"
					legacyReq = LegacyArchiveRequest{ArchiveRequest: req, ExpectedMode: uint32(info.Mode().Perm()), ApproveCompletion: true}
				}
				commonPath := ""
				if shared {
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonPath = filepath.Join(s.root.Name(), sharedStateFile)
					if err := release(); err != nil {
						t.Fatal(err)
					}
				}

				for _, target := range uniqueArchiveTargets(board, other) {
					if out, err := runLegacyPolicyCommand(binary, "list", "--dir", target, "--json"); err != nil {
						t.Fatalf("legacy baseline read failed for %s: %s: %v", target, out, err)
					}
				}
				var boundaryErr error
				if legacy {
					_, boundaryErr = legacyArchiveWithStep(board, legacyReq, true, false, repairStopAt(point))
				} else {
					_, boundaryErr = archiveWithStep(board, req, true, false, repairStopAt(point))
				}
				if boundaryErr == nil || !strings.Contains(boundaryErr.Error(), "stop at "+point) {
					t.Fatalf("archive boundary: %v", boundaryErr)
				}

				commonBefore := []byte(nil)
				if commonPath != "" {
					var err error
					commonBefore, err = os.ReadFile(commonPath)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, target := range uniqueArchiveTargets(board, other) {
					before := boardBytes(t, target)
					for _, args := range [][]string{
						{"list", "--dir", target, "--json"},
						{"ready", "--dir", target, "--json"},
						{"create", "--dir", target, "--title", "legacy", "--json"},
						{"reserve-ids", "--dir", target, "--id", "TASK-99", "--json"},
					} {
						out, err := runLegacyPolicyCommand(binary, args...)
						var exit *exec.ExitError
						if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "storage protocol") {
							t.Fatalf("legacy did not hit archive storage barrier: %v output=%s err=%v", args, out, err)
						}
						if !reflectEqualBoard(before, boardBytes(t, target)) {
							t.Fatal("legacy command changed board")
						}
					}
				}
				if commonPath != "" {
					after, err := os.ReadFile(commonPath)
					if err != nil || !bytes.Equal(commonBefore, after) {
						t.Fatalf("legacy command changed shared state: %v", err)
					}
				}
			})
		}
	}
}

func uniqueArchiveTargets(board, other string) []string {
	if other == "" || other == board {
		return []string{board}
	}
	return []string{board, other}
}
