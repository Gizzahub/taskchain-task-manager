package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRepairAdmissionFailuresDoNotAdopt(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"no-adopt", "resume-absent", "hash", "path", "owner", "request", "unreserved"} {
		t.Run(kind, func(t *testing.T) {
			board, req, _, _ := statusRepairFixture(t)
			switch kind {
			case "hash":
				req.ExpectedSHA256 = strings.Repeat("0", 64)
			case "path":
				req.Path = "todo/../todo/TASK-1.md"
			case "owner":
				req.Owner = ""
			case "request":
				req.RequestID = "invalid"
			case "unreserved":
				path := filepath.Join(board, req.Path)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				raw = []byte(strings.ReplaceAll(string(raw), "TASK-1", "TASK-99"))
				req.ID, req.Path, req.ExpectedSHA256 = "TASK-99", "todo/TASK-99.md", bytesDigest(raw)
				if err := os.WriteFile(filepath.Join(board, req.Path), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, board)
			var err error
			if kind == "resume-absent" {
				_, err = RecoverStatusRepair(board, req)
			} else {
				_, err = RepairStatus(board, req, kind != "no-adopt")
			}
			if err == nil {
				t.Fatal("invalid admission accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("rejected admission mutated board")
			}
		})
	}
}

func TestRepairCompletedReplayDoesNotRecreateDeletedCard(t *testing.T) {
	t.Parallel()
	board, req, _, _ := statusRepairFixture(t)
	first, err := RepairStatus(board, req, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(board, req.Path)); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	again, err := RecoverStatusRepair(board, req)
	if err != nil || again != first {
		t.Fatalf("historical receipt changed: %+v %v", again, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("receipt replay recreated deleted card or mutated state")
	}
	created, err := Create(board, CreateRequest{Title: "next"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Card.ID == req.ID {
		t.Fatal("deleted repaired ID was reused")
	}
}
