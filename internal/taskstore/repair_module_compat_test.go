package taskstore

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func moduleRepairLegacyBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("TASKCHAIN_MODULE_REPAIR_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_MODULE_REPAIR_LEGACY_BINARY to the 452b5a9 executable")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("legacy repair binary: %v", err)
	}
	return binary
}

func moduleRepairLegacyBarrier(t *testing.T, binary, board, diagnostic string, before map[string]string) {
	t.Helper()
	for _, args := range [][]string{
		{"list", "--dir", board, "--json"},
		{"create", "--dir", board, "--title", "old writer", "--json"},
		{"reserve-ids", "--dir", board, "--id", "TASK-999", "--json"},
	} {
		out, err := runLegacyPolicyCommand(binary, args...)
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), diagnostic) {
			t.Fatalf("legacy command=%v diagnostic=%q output=%s err=%v", args, diagnostic, out, err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatalf("legacy command changed board: %v", args)
		}
	}
}

func TestModuleRepairLegacyBinaryBarrier(t *testing.T) {
	binary := moduleRepairLegacyBinary(t)
	board, _ := moduleRepairFixture(t)
	if out, err := runLegacyPolicyCommand(binary, "list", "--dir", board, "--json"); err != nil {
		t.Fatalf("module-aware old baseline failed: %s %v", out, err)
	}
	for _, phase := range []string{"after-journal", "after-replacement"} {
		t.Run(phase, func(t *testing.T) {
			board, req := moduleRepairFixture(t)
			if _, err := repairStatusWithStep(board, req, true, false, repairStopAt(phase)); err == nil || !strings.Contains(err.Error(), "stop at "+phase) {
				t.Fatalf("requested repair interruption not observed: %v", err)
			}
			moduleRepairLegacyBarrier(t, binary, board, "policyCanonical", boardBytes(t, board))
		})
	}
}

func sharedModuleRepairFixture(t *testing.T) (string, string, string, RepairRequest, string) {
	t.Helper()
	_, owner, other := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(owner, moduleAdoptionRaw(t), PolicyActivationOptions{AllWorktrees: true, AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireSharedForPolicy(owner)
	if err != nil {
		t.Fatal(err)
	}
	commonPath := filepath.Join(s.root.Name(), sharedStateFile)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(owner, CreateRequest{Title: "shared repair", Module: "backend", Category: "auth/api"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(owner, filepath.FromSlash(entry.Path))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n| **Status** | [x] Done |\n")...)
	if err := os.WriteFile(path, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	return owner, other, entry.Path, RepairRequest{ID: entry.Card.ID, Owner: "tester", RequestID: strings.Repeat("d", 32), Path: entry.Path, ExpectedSHA256: bytesDigest(raw)}, commonPath
}

func sharedRepairState(t *testing.T, commonPath string) sharedState {
	t.Helper()
	r, err := os.OpenRoot(filepath.Dir(commonPath))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state, err := loadSharedState(r)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestModuleRepairLegacyBinarySharedBoundariesAndRecovery(t *testing.T) {
	binary := moduleRepairLegacyBinary(t)
	for _, phase := range []string{"after-common-pending", "after-journal", "after-replacement"} {
		t.Run(phase, func(t *testing.T) {
			owner, other, _, req, commonPath := sharedModuleRepairFixture(t)
			if out, err := runLegacyPolicyCommand(binary, "list", "--dir", other, "--json"); err != nil {
				t.Fatalf("module-aware old baseline failed: %s %v", out, err)
			}
			if _, err := repairStatusWithStep(owner, req, true, false, repairStopAt(phase)); err == nil || !strings.Contains(err.Error(), "stop at "+phase) {
				t.Fatalf("requested repair interruption not observed: %v", err)
			}
			beforeOwner, beforeOther := boardBytes(t, owner), boardBytes(t, other)
			beforeCommon, err := os.ReadFile(commonPath)
			if err != nil {
				t.Fatal(err)
			}
			diagnostic := `json: unknown field "policyCanonical"`
			moduleRepairLegacyBarrier(t, binary, owner, diagnostic, beforeOwner)
			moduleRepairLegacyBarrier(t, binary, other, diagnostic, beforeOther)
			if !reflect.DeepEqual(beforeOwner, boardBytes(t, owner)) || !reflect.DeepEqual(beforeOther, boardBytes(t, other)) {
				t.Fatal("legacy writer changed a participant board")
			}
			afterCommon, err := os.ReadFile(commonPath)
			if err != nil || !bytes.Equal(beforeCommon, afterCommon) {
				t.Fatalf("legacy writer changed common state: %v", err)
			}
		})
	}

	t.Run("completed-recovery", func(t *testing.T) {
		owner, other, _, req, commonPath := sharedModuleRepairFixture(t)
		if _, err := repairStatusWithStep(owner, req, true, false, repairStopAt("after-replacement")); err == nil || !strings.Contains(err.Error(), "stop at after-replacement") {
			t.Fatalf("requested repair interruption not observed: %v", err)
		}
		if _, err := RecoverStatusRepair(owner, req); err != nil {
			t.Fatal(err)
		}
		ownerBefore := boardBytes(t, owner)
		commonBefore := sharedRepairState(t, commonPath)
		moduleRepairLegacyBarrier(t, binary, owner, `json: unknown field "policyCanonical"`, ownerBefore)
		otherBefore := boardBytes(t, other)
		if out, err := runLegacyPolicyCommand(binary, "create", "--dir", other, "--title", "old other", "--json"); err != nil {
			t.Fatalf("old other worktree was blocked: %s %v", out, err)
		}
		if !reflect.DeepEqual(ownerBefore, boardBytes(t, owner)) {
			t.Fatal("old owner rejection changed owner board")
		}
		if len(boardBytes(t, other)) <= len(otherBefore) {
			t.Fatal("successful other writer did not publish its card")
		}
		got := sharedRepairState(t, commonPath)
		if !containsAllIDs(got.Reserved, commonBefore.Reserved) || len(got.Reserved) != len(commonBefore.Reserved)+1 {
			t.Fatal("successful old writer did not preserve and extend reservations")
		}
		commonBefore.Reserved, got.Reserved = nil, nil
		if !reflect.DeepEqual(commonBefore, got) {
			t.Fatal("other writer changed shared policy/protocol state")
		}
	})
}
