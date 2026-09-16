package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPendingRepairBlocksCrossProtocolAdmissionsWithoutMutation(t *testing.T) {
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	req := storageRepairRequest(t, board, 'a')
	stop := errors.New("pause pending repair")
	if _, err := repairStatusWithStep(board, req, true, false, func(point string) error {
		if point == "after-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("pending setup: %v", err)
	}

	before := boardBytes(t, board)
	checks := []struct {
		name string
		run  func() error
	}{
		{"bundle", func() error {
			s, err := openBundleSession(board)
			if s != nil {
				_ = s.close()
			}
			return err
		}},
		{"policy", func() error {
			_, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
			return err
		}},
		{"context", func() error { _, err := RegisterContext(board, []byte(testContextIntent)); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "repair") {
				t.Fatalf("admission err=%v", err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("blocked admission changed board")
			}
		})
	}
	if _, err := RecoverStatusRepair(board, req); err != nil {
		t.Fatal("recover:", err)
	}
}

func TestCompletedRepairSurvivesBundleAdoption(t *testing.T) {
	_, board, _ := sharedFixture(t)
	req := storageRepairRequest(t, board, 'c')
	first, err := RepairStatus(board, req, true)
	if err != nil || first.Status != "completed" {
		t.Fatalf("repair=%+v err=%v", first, err)
	}
	repairBefore, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	transitionsBefore, err := os.ReadFile(filepath.Join(board, transitionsFile))
	if err != nil {
		t.Fatal(err)
	}

	s, err := openBundleSession(board)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.adopt(true, nil); err != nil {
		_ = s.close()
		t.Fatal("bundle adoption:", err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	repairAfter, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(repairBefore, repairAfter) {
		t.Fatal("bundle adoption rewrote completed repair receipt")
	}
	transitionsAfter, err := os.ReadFile(filepath.Join(board, transitionsFile))
	if err != nil {
		t.Fatal(err)
	}
	var beforeJ, afterJ transitionJournal
	if beforeJ, err = decodeTransitionJournal(transitionsBefore); err != nil {
		t.Fatal(err)
	}
	if afterJ, err = decodeTransitionJournal(transitionsAfter); err != nil {
		t.Fatal(err)
	}
	if beforeJ.StorageProtocol != 1 || afterJ.StorageProtocol != 1 {
		t.Fatalf("storage protocol lost: before=%d after=%d", beforeJ.StorageProtocol, afterJ.StorageProtocol)
	}
	again, err := RecoverStatusRepair(board, req)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatalf("receipt changed after bundle adoption: first=%+v again=%+v", first, again)
	}
}

func TestPendingRepairBlocksPublicBundleOnLocalAndSharedBoards(t *testing.T) {
	for _, sharedBoard := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "shared"}[sharedBoard], func(t *testing.T) {
			var board string
			if sharedBoard {
				_, board, _ = sharedFixture(t)
				if _, err := EnableShared(board, false); err != nil {
					t.Fatal(err)
				}
			} else {
				board = configuredFixture(t)
			}
			if _, err := Create(board, CreateRequest{ID: "TASK-3", Title: "Repair target"}); err != nil {
				t.Fatal(err)
			}
			raw := publicationRequest(t, board)
			req := repairRequestForCardAt(t, board, "todo/TASK-3.md", "TASK-3", strings.Repeat("d", 32))
			var commonBefore []byte
			var commonPath string
			if sharedBoard {
				s, release, err := acquireShared(board, false)
				if err != nil {
					t.Fatal(err)
				}
				commonPath = filepath.Join(s.root.Name(), sharedStateFile)
				commonBefore, err = os.ReadFile(commonPath)
				if err != nil {
					_ = release()
					t.Fatal(err)
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
			}
			stop := errors.New("pause pending repair")
			if _, err := repairStatusWithStep(board, req, true, false, func(point string) error {
				if point == "after-journal" {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			if sharedBoard {
				var err error
				commonBefore, err = os.ReadFile(commonPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, board)
			if _, err := PublishBundle(board, raw, BundleOptions{}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "repair") {
				t.Fatalf("public bundle admission err=%v", err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("blocked public bundle changed board")
			}
			if sharedBoard {
				commonAfter, err := os.ReadFile(commonPath)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(commonBefore, commonAfter) {
					t.Fatal("blocked public bundle changed common state")
				}
			}
			if _, err := RecoverStatusRepair(board, req); err != nil {
				t.Fatal("recover:", err)
			}
		})
	}
}

func repairRequestForCardAt(t *testing.T, board, rel, id, request string) RepairRequest {
	t.Helper()
	path := filepath.Join(board, filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return RepairRequest{ID: id, Owner: "worker", RequestID: request, Path: rel, ExpectedSHA256: bytesDigest(raw)}
}

func TestPendingOtherProtocolsRejectRepairBeforeAdoption(t *testing.T) {
	t.Run("bundle", func(t *testing.T) {
		board := configuredFixture(t)
		if _, err := Create(board, CreateRequest{ID: "TASK-3", Title: "Repair target"}); err != nil {
			t.Fatal(err)
		}
		raw := publicationRequest(t, board)
		stop := errors.New("pause bundle")
		if _, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(point string) error {
			if point == "after-pending-journal" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		req := repairRequestForCardAt(t, board, "todo/TASK-3.md", "TASK-3", strings.Repeat("e", 32))
		before := boardBytes(t, board)
		if _, err := RepairStatus(board, req, true); err == nil || !strings.Contains(strings.ToLower(err.Error()), "bundle") {
			t.Fatalf("repair bypassed pending bundle: %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("rejected repair changed pending bundle")
		}
	})
	t.Run("transition", func(t *testing.T) {
		board, transitionReq, _, _ := transitionFixture(t)
		if _, err := ReserveIDs(board, []string{"TASK-1"}, true); err != nil {
			t.Fatal(err)
		}
		stop := errors.New("pause transition")
		if _, err := transitionWithStep(board, transitionReq, func(point string) error {
			if point == "after-journal" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		req := repairRequestForCardAt(t, board, "todo/custom-name.md", "TASK-1", strings.Repeat("e", 32))
		before := boardBytes(t, board)
		if _, err := RepairStatus(board, req, true); err == nil || !strings.Contains(strings.ToLower(err.Error()), "transition") {
			t.Fatalf("repair bypassed pending transition: %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("rejected repair changed pending transition")
		}
	})
	t.Run("policy", func(t *testing.T) {
		board := configuredFixture(t)
		if _, err := Create(board, CreateRequest{ID: "TASK-3", Title: "Repair target"}); err != nil {
			t.Fatal(err)
		}
		stop := errors.New("pause policy")
		if _, err := activateLocalPolicy(board, nil, currentPolicy(), false, func(point string) error {
			if point == "after-local-pending" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		req := repairRequestForCardAt(t, board, "todo/TASK-3.md", "TASK-3", strings.Repeat("f", 32))
		before := boardBytes(t, board)
		if _, err := RepairStatus(board, req, true); err == nil || !strings.Contains(strings.ToLower(err.Error()), "policy") {
			t.Fatalf("repair bypassed pending policy: %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatal("rejected repair changed pending policy")
		}
	})
}
