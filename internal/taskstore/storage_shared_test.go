package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func storageRepairRequest(t *testing.T, board string, request byte) RepairRequest {
	t.Helper()
	name := filepath.Join(board, "todo", "TASK-1.md")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n| **Status** | [x] Done |\n")...)
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return RepairRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat(string(request), 32), Path: "todo/TASK-1.md", ExpectedSHA256: bytesDigest(raw)}
}

func TestStorageSharedInterruptionAndOtherBoardGate(t *testing.T) {
	t.Parallel()
	for _, at := range []string{"after-common-protocol", "after-common-pending", "after-journal", "after-replacement", "after-receipt"} {
		t.Run(at, func(t *testing.T) {
			_, board, other := sharedFixture(t)
			if _, err := EnableShared(board, false); err != nil {
				t.Fatal(err)
			}
			req := storageRepairRequest(t, board, 'a')
			stop := errors.New("stop")
			_, err := repairStatusWithStep(board, req, true, false, func(point string) error {
				if point == at {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("boundary: %v", err)
			}
			if at != "after-common-protocol" {
				before := boardBytes(t, other)
				if _, err := Create(other, CreateRequest{Title: "blocked"}); err == nil {
					t.Fatal("other board writer ignored shared pending")
				}
				if !reflect.DeepEqual(before, boardBytes(t, other)) {
					t.Fatal("blocked other board changed")
				}
			}
			if at == "after-common-protocol" {
				_, err = RepairStatus(board, req, true)
			} else {
				_, err = RecoverStatusRepair(board, req)
			}
			if err != nil {
				t.Fatal("recovery:", err)
			}
			if _, err := List(other); err != nil {
				t.Fatal("other board not released:", err)
			}
			first, err := RecoverStatusRepair(board, req)
			if err != nil || first.Status != "completed" || !first.Changed {
				t.Fatalf("receipt: %+v %v", first, err)
			}
		})
	}
}

func TestStorageSharedTargetCannotDropHistoricalReceipt(t *testing.T) {
	t.Parallel()
	testStorageSharedTargetTampering(t, false)
}

func TestStorageSharedTargetMustBeCanonical(t *testing.T) {
	t.Parallel()
	testStorageSharedTargetTampering(t, true)
}

func testStorageSharedTargetTampering(t *testing.T, formattingOnly bool) {
	t.Helper()
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	first := storageRepairRequest(t, board, 'a')
	if _, err := RepairStatus(board, first, true); err != nil {
		t.Fatal(err)
	}
	second := storageRepairRequest(t, board, 'b')
	stop := errors.New("stop")
	if _, err := repairStatusWithStep(board, second, false, false, func(at string) error {
		if at == "after-common-pending" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	s, release, err := acquireSharedStorageOptions(board, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	target, err := decodeRepairJournal(s.state.PendingRepair.TargetJournal)
	if err != nil {
		t.Fatal(err)
	}
	if !formattingOnly {
		target.Records = target.Records[1:]
	}
	raw, err := json.Marshal(target)
	if err != nil {
		t.Fatal(err)
	}
	if formattingOnly {
		var indented bytes.Buffer
		if err := json.Indent(&indented, raw, "", "  "); err != nil {
			t.Fatal(err)
		}
		raw = indented.Bytes()
	}
	s.state.PendingRepair.TargetJournal = append(raw, '\n')
	corrupt, err := json.Marshal(s.state)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.root.WriteFile(sharedStateFile, corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	commonPath := filepath.Join(s.root.Name(), sharedStateFile)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	if _, err := RecoverStatusRepair(board, second); err == nil {
		t.Fatal("tampered common target accepted")
	}
	after, err := os.ReadFile(commonPath)
	if err != nil || !bytes.Equal(after, corrupt) || !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("failed recovery changed evidence", err)
	}
}

func TestStorageCommonMarkerRollbackRejected(t *testing.T) {
	t.Parallel()
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	req := storageRepairRequest(t, board, 'a')
	if _, err := RepairStatus(board, req, true); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	next := *s.state
	next.StorageProtocol = 0
	if err := publishSharedState(s.root, next, false); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	checks := []func() error{
		func() error { _, e := List(board); return e },
		func() error { _, e := Create(board, CreateRequest{Title: "no"}); return e },
		func() error { _, e := ReserveIDs(board, []string{"TASK-99"}, false); return e },
		func() error {
			_, e := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
			return e
		},
	}
	for _, check := range checks {
		if err := check(); err == nil {
			t.Fatal("common protocol rollback accepted")
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("failed operation changed board")
		}
	}
}

func TestStorageReceiptSurvivesPolicyAndSharedAdoption(t *testing.T) {
	t.Parallel()
	_, board, _ := sharedFixture(t)
	req := storageRepairRequest(t, board, 'a')
	first, err := RepairStatus(board, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal("shared adoption:", err)
	}
	if _, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal("policy adoption:", err)
	}
	again, err := RecoverStatusRepair(board, req)
	if err != nil || first != again {
		t.Fatalf("historical receipt: %+v %v", again, err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.state.StorageProtocol != 1 {
		t.Fatal("common marker lost")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
