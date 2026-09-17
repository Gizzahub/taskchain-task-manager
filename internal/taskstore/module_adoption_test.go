package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func moduleAdoptionRaw(t *testing.T) []byte {
	t.Helper()
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func moduleAdoptionBoard(t *testing.T) string {
	t.Helper()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(filepath.Join(board, "backend", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(board, "backend", "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAdoptionCard(t, board, "backend/todo/TASK-7.md", "TASK-7")
	writeAdoptionCard(t, board, "backend/plan/PLAN-2.md", "PLAN-2")
	return board
}

func writeAdoptionCard(t *testing.T, board, name, id string) {
	t.Helper()
	path := filepath.Join(board, filepath.FromSlash(name))
	if err := os.WriteFile(path, []byte("---\nid: "+id+"\ntitle: "+id+"\nstatus: pending\n---\n\n# "+id+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
}

func adoptionLedger(t *testing.T, board string) idLedger {
	t.Helper()
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ledger, err := loadIDs(r)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func TestModuleAdoptionRequiresExplicitFlagAndPreservesObservedIDs(t *testing.T) {
	board := moduleAdoptionBoard(t)
	raw := moduleAdoptionRaw(t)
	before := boardBytes(t, board)
	if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "module") {
		t.Fatalf("module adoption without flag accepted: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("refused adoption changed board")
	}
	result, err := ActivatePolicy(board, raw, PolicyActivationOptions{AdoptModules: true})
	if err != nil || result.Status != "completed" {
		t.Fatalf("adoption=%+v err=%v", result, err)
	}
	if entries, err := List(board); err != nil || len(entries) != 2 {
		t.Fatalf("adopted entries=%v err=%v", entries, err)
	}
	ledger := adoptionLedger(t, board)
	if !reflect.DeepEqual(ledger.Reserved, []string{"PLAN-2", "TASK-7"}) {
		t.Fatalf("observed IDs=%v", ledger.Reserved)
	}
	if err := os.Remove(filepath.Join(board, "backend/todo/TASK-7.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-7", Title: "must not reuse"}); err == nil {
		t.Fatal("deleted observed ID was reallocated")
	}
	if entry, err := Create(board, CreateRequest{Title: "next"}); err != nil || entry.Card.ID != "TASK-8" {
		t.Fatalf("next allocation=%+v err=%v", entry, err)
	}
}

func TestModuleAdoptionResumesEveryLocalBoundaryWithExplicitScope(t *testing.T) {
	for _, phase := range []string{"after-local-pending", "after-policy", "after-policy-ids", "after-policy-journal", "after-local-completed"} {
		t.Run(phase, func(t *testing.T) {
			board := moduleAdoptionBoard(t)
			raw := moduleAdoptionRaw(t)
			stop := errors.New("synthetic module adoption interruption")
			if _, err := activatePolicyWithStep(board, raw, PolicyActivationOptions{AdoptModules: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			if phase != "after-local-completed" {
				if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true}); err == nil {
					t.Fatal("resume without module adoption accepted")
				}
			}
			result, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true, AdoptModules: true})
			if err != nil || result.Status != "completed" {
				t.Fatalf("resume=%+v err=%v", result, err)
			}
			before := boardBytes(t, board)
			replay, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true, AdoptModules: true})
			if err != nil || !replay.Replayed || !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
		})
	}
}

func TestModuleAdoptionRejectsChangedInventoryAndCardInputs(t *testing.T) {
	t.Run("changed-card", func(t *testing.T) {
		board := moduleAdoptionBoard(t)
		raw := moduleAdoptionRaw(t)
		stop := errors.New("stop")
		if _, err := activatePolicyWithStep(board, raw, PolicyActivationOptions{AdoptModules: true}, func(at string) error {
			if at == "after-local-pending" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(board, "backend/todo/TASK-7.md"), []byte("---\nid: TASK-7\ntitle: changed\nstatus: pending\n---\n\n# Changed\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true, AdoptModules: true}); err == nil {
			t.Fatal("changed card resumed")
		}
	})
	t.Run("deleted-target-after-id-publication", func(t *testing.T) {
		board := moduleAdoptionBoard(t)
		raw := moduleAdoptionRaw(t)
		stop := errors.New("stop")
		if _, err := activatePolicyWithStep(board, raw, PolicyActivationOptions{AdoptModules: true}, func(at string) error {
			if at == "after-policy-ids" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(board, idsFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true, AdoptModules: true}); err == nil {
			t.Fatal("recovery restored a deleted target")
		}
		if _, err := os.Stat(filepath.Join(board, idsFile)); !os.IsNotExist(err) {
			t.Fatalf("deleted ID ledger was restored: %v", err)
		}
	})
	t.Run("completed-replay-needs-ledger", func(t *testing.T) {
		board := moduleAdoptionBoard(t)
		raw := moduleAdoptionRaw(t)
		if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{AdoptModules: true}); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(board, idsFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(board, raw, PolicyActivationOptions{Resume: true, AdoptModules: true}); err == nil {
			t.Fatal("completed replay accepted missing ID ledger")
		}
	})
	t.Run("unknown-root", func(t *testing.T) {
		board := moduleAdoptionBoard(t)
		if err := os.MkdirAll(filepath.Join(board, "rogue"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err == nil {
			t.Fatal("unknown root accepted")
		}
	})
	t.Run("normalized-duplicate", func(t *testing.T) {
		board := moduleAdoptionBoard(t)
		writeAdoptionCard(t, board, "backend/todo/TASK-007.md", "TASK-007")
		if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			t.Fatalf("normalized duplicate error=%v", err)
		}
	})
}

func TestModuleAdoptionRejectsPreexistingHeldClaim(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "held"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(board, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(board, "backend", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeAdoptionCard(t, board, "backend/todo/TASK-7.md", "TASK-7")
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "claim") {
		t.Fatalf("held claim adoption error=%v", err)
	}
}
