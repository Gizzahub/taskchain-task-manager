package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run with an actual pre-storage-protocol executable, not a simulated decoder.
func TestStorageLegacyBinaryBarrier(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_STORAGE_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_STORAGE_LEGACY_BINARY to a pre-storage executable")
	}
	baseline := filepath.Join(t.TempDir(), "tasks")
	if err := Init(baseline); err != nil {
		t.Fatal(err)
	}
	if out, err := runLegacyPolicyCommand(binary, "list", "--dir", baseline, "--json"); err != nil {
		t.Fatalf("legacy executable cannot read baseline: %s %v", out, err)
	}
	t.Run("local-completed", func(t *testing.T) {
		board, req, _, _ := statusRepairFixture(t)
		if _, err := RepairStatus(board, req, true); err != nil {
			t.Fatal(err)
		}
		assertLegacyPolicyBarrier(t, binary, board, "")
	})
	for _, phase := range []string{"after-common-protocol", "after-common-pending", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			_, owner, other := sharedFixture(t)
			if _, err := EnableShared(owner, false); err != nil {
				t.Fatal(err)
			}
			s, release, err := acquireShared(owner, false)
			if err != nil {
				t.Fatal(err)
			}
			commonPath := filepath.Join(s.root.Name(), sharedStateFile)
			if err := release(); err != nil {
				t.Fatal(err)
			}
			req := storageRepairRequest(t, owner, 'a')
			if _, err := repairStatusWithStep(owner, req, true, false, repairStopAt(phase)); err == nil || !strings.Contains(err.Error(), "stop at "+phase) {
				t.Fatalf("repair did not reach %s: %v", phase, err)
			}
			before, err := os.ReadFile(commonPath)
			if err != nil {
				t.Fatal(err)
			}
			assertLegacyPolicyBarrier(t, binary, owner, "")
			assertLegacyPolicyBarrier(t, binary, other, "")
			if after, err := os.ReadFile(commonPath); err != nil || !bytes.Equal(before, after) {
				t.Fatalf("legacy commands changed common state: %v", err)
			}
			if _, err := RepairStatus(owner, req, true); err != nil {
				t.Fatal(err)
			}
			assertLegacyPolicyBarrier(t, binary, other, owner)
		})
	}
}
