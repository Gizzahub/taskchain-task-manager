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

// Use the actual storage-v1 executable. Assert the storage-protocol diagnostic,
// not just failure: rejection of policy v2 alone would not prove this barrier.
func TestRelocationStorageV1BinaryBarrier(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_RELOCATION_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_RELOCATION_LEGACY_BINARY to storage-v1 executable")
	}
	for _, shared := range []bool{false, true} {
		for _, point := range []string{"after-relocation-common-protocol", "after-relocation-journal", "after-relocation-receipt"} {
			if !shared && point == "after-relocation-common-protocol" {
				continue
			}
			t.Run(strings.Join([]string{map[bool]string{true: "shared", false: "local"}[shared], point}, "/"), func(t *testing.T) {
				var board, other, commonPath string
				var req RelocationRequest
				if shared {
					_, board, other = sharedFixture(t)
					if _, err := EnableShared(board, false); err != nil {
						t.Fatal(err)
					}
					if out, err := runLegacyPolicyCommand(binary, "list", "--dir", other, "--json"); err != nil {
						t.Fatalf("legacy baseline failed: %s %v", out, err)
					}
					if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{AllWorktrees: true}); err != nil {
						t.Fatal(err)
					}
					req = relocationRequestFor(t, board)
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonPath = filepath.Join(s.root.Name(), sharedStateFile)
					if err := release(); err != nil {
						t.Fatal(err)
					}
				} else {
					board, req = relocationBoardFixture(t)
				}
				if _, err := relocateWithStep(board, req, true, false, repairStopAt(point)); err == nil || !strings.Contains(err.Error(), "stop at "+point) {
					t.Fatalf("boundary: %v", err)
				}
				var commonBefore []byte
				if shared {
					var err error
					commonBefore, err = os.ReadFile(commonPath)
					if err != nil {
						t.Fatal(err)
					}
				}
				for _, target := range []string{board, other} {
					if target == "" {
						continue
					}
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
							t.Fatalf("legacy did not hit storage barrier: %v %s %v", args, out, err)
						}
						if !reflectEqualBoard(before, boardBytes(t, target)) {
							t.Fatal("legacy command changed board")
						}
					}
				}
				if shared {
					after, err := os.ReadFile(commonPath)
					if err != nil || !bytes.Equal(commonBefore, after) {
						t.Fatal("legacy command changed shared state:", err)
					}
				}
			})
		}
	}
}
