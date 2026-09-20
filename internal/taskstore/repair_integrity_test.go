package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepairIntegrityDamageRequiresRestore(t *testing.T) {
	t.Parallel()
	for _, damage := range []string{"missing-journal", "missing-marker", "missing-ids", "copied-board"} {
		t.Run(damage, func(t *testing.T) {
			board, req, _, _ := statusRepairFixture(t)
			if _, err := repairStatusWithStep(board, req, true, false, repairStopAt("after-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-journal") {
				t.Fatalf("pending setup: %v", err)
			}
			switch damage {
			case "missing-journal", "missing-ids":
				name := repairsFile
				if damage == "missing-ids" {
					name = idsFile
				}
				if err := os.Rename(filepath.Join(board, name), filepath.Join(board, name+".backup")); err != nil {
					t.Fatal(err)
				}
			case "missing-marker":
				path := filepath.Join(board, transitionsFile)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				j, err := decodeTransitionJournal(raw)
				if err != nil {
					t.Fatal(err)
				}
				j.StorageProtocol = 0
				raw, err = json.Marshal(j)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "copied-board":
				copyPath := filepath.Join(t.TempDir(), "copied-tasks")
				if err := os.CopyFS(copyPath, os.DirFS(board)); err != nil {
					t.Fatal(err)
				}
				board = copyPath
			}
			before := boardBytes(t, board)
			for _, operation := range []func() error{
				func() error { _, err := RecoverStatusRepair(board, req); return err },
				func() error { _, err := RepairStatus(board, req, true); return err },
				func() error { return Init(board) },
				func() error { _, err := ReserveIDs(board, []string{"TASK-99"}, true); return err },
			} {
				if err := operation(); err == nil {
					t.Fatal("damaged binding accepted")
				}
				if !reflect.DeepEqual(before, boardBytes(t, board)) {
					t.Fatal("damaged binding was silently rewritten")
				}
			}
		})
	}
}

func TestRepairHeldClaimReleaseAndHistoricalReplay(t *testing.T) {
	t.Parallel()
	board, req, _, _ := statusRepairFixture(t)
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: testToken}
	if _, err := Claim(board, claim); err != nil {
		t.Fatal(err)
	}
	req.Token = claim.Token
	if _, err := repairStatusWithStep(board, req, true, false, repairStopAt("after-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-journal") {
		t.Fatalf("pending setup: %v", err)
	}
	before := boardBytes(t, board)
	wrong := req
	wrong.Token = strings.Repeat("f", 32)
	if _, err := RecoverStatusRepair(board, wrong); err == nil {
		t.Fatal("wrong token recovered repair")
	}
	if _, err := Release(board, claim); err == nil {
		t.Fatal("pending claim released")
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("rejected requests changed pending claim or repair")
	}
	first, err := RecoverStatusRepair(board, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Release(board, claim); err != nil {
		t.Fatal(err)
	}
	before = boardBytes(t, board)
	again, err := RecoverStatusRepair(board, req)
	if err != nil || again != first || !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatalf("historical receipt after release: %+v %v", again, err)
	}
}
