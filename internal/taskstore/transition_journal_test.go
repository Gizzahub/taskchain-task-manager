package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func journalFixture(t *testing.T) transitionJournal {
	t.Helper()
	original := []byte("---\nid: TASK-1\ntitle: one\nstatus: pending\n---\n\n| **Status** | [ ] Pending |\n")
	doc, err := card.Parse(original)
	if err != nil {
		t.Fatal(err)
	}
	patched, _, err := doc.SetStatusCell("in-progress")
	if err != nil {
		t.Fatal(err)
	}
	return transitionJournal{SchemaVersion: 1, Records: []transitionRecord{{Kind: "pending", RequestID: testToken, ID: "TASK-1", Owner: "worker", Token: strings.Repeat("b", 32), From: "todo", To: "doing", Source: "todo/TASK-1.md", Target: "doing/TASK-1.md", Mode: 0o644, Original: original, Patched: patched, Status: "pending"}}}
}

func TestTransitionJournalForgedRecordsPreserveBoard(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*transitionJournal){
		"version":           func(j *transitionJournal) { j.SchemaVersion = 2 },
		"duplicate request": func(j *transitionJournal) { j.Records = append(j.Records, j.Records[0]) },
		"multiple pending": func(j *transitionJournal) {
			r := j.Records[0]
			r.RequestID = strings.Repeat("c", 32)
			j.Records = append(j.Records, r)
		},
		"id mismatch":   func(j *transitionJournal) { j.Records[0].ID = "TASK-2" },
		"patched bytes": func(j *transitionJournal) { j.Records[0].Patched = []byte("forged") },
		"unsafe mode":   func(j *transitionJournal) { j.Records[0].Mode = 0o4644 },
		"traversal":     func(j *transitionJournal) { j.Records[0].Source = "todo/../todo/TASK-1.md" },
		"hidden": func(j *transitionJournal) {
			j.Records[0].Source, j.Records[0].Target = "todo/.hidden.md", "doing/.hidden.md"
		},
		"readme": func(j *transitionJournal) {
			j.Records[0].Source, j.Records[0].Target = "todo/readme.md", "doing/readme.md"
		},
		"oversize payload": func(j *transitionJournal) { j.Records[0].Original = make([]byte, maxCardBytes+1) },
	} {
		t.Run(name, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			j := journalFixture(t)
			mutate(&j)
			raw, err := json.Marshal(j)
			if err != nil {
				t.Fatal(err)
			}
			writeJournalFixture(t, dir, raw)
			before := boardBytes(t, dir)
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if _, err := loadTransitions(root); err == nil {
				t.Fatal("forged journal passed structural validation")
			}
			if _, err := Recover(dir, req); err == nil {
				t.Fatal("forged journal accepted")
			}
			if _, err := List(dir); err == nil {
				t.Fatal("invalid journal did not block ordinary operations")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejection changed board")
			}
		})
	}
}

func writeJournalFixture(t *testing.T, root string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, transitionsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTransitionJournalStrictValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	valid, err := json.Marshal(journalFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	write := func(raw []byte) error { return os.WriteFile(filepath.Join(root, transitionsFile), raw, 0o600) }
	if err := write(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTransitions(r); err != nil {
		t.Fatalf("valid journal rejected: %v", err)
	}
	for name, raw := range map[string][]byte{
		"unknown":      []byte(`{"schemaVersion":1,"records":[],"extra":1}`),
		"case":         []byte(`{"schemaVersion":1,"Records":[]}`),
		"null records": []byte(`{"schemaVersion":1,"records":null}`),
		"trailing":     []byte(`{"schemaVersion":1,"records":[]} {}`),
		"invalid utf8": append([]byte(`{"schemaVersion":1,"records":"`), 0xff, '"', '}'),
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(raw); err != nil {
				t.Fatal(err)
			}
			if _, err := loadTransitions(r); err == nil {
				t.Fatal("invalid journal accepted")
			}
		})
	}
}

func TestTransitionJournalCapacityChecksBothStates(t *testing.T) {
	t.Parallel()
	j := journalFixture(t)
	if err := validateTransitionCapacity(j); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50000; i++ {
		j.Records = append(j.Records, transitionRecord{Kind: "completed", RequestID: strings.Repeat("c", 31) + "0", ID: "TASK-1", Owner: "worker", Token: strings.Repeat("d", 32), From: "todo", To: "doing", Source: "todo/TASK-1.md", Target: "doing/TASK-1.md", Mode: 0o644, Status: "completed"})
	}
	if err := validateTransitionCapacity(j); err == nil {
		t.Fatal("oversized journal accepted")
	}
}
