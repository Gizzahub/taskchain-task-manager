package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLegacyDocumentationTransitionPreserved(t *testing.T) {
	t.Parallel()
	dir, req, _, _ := transitionFixture(t)
	stop := errors.New("interrupt before publish")
	if _, err := transitionWithStep(dir, req, func(at string) error {
		if at == "after-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil {
		t.Fatal(err)
	}
	// Model the prior binary's valid journal without running an old executable.
	j.Records[0].Source, j.Records[0].Target = "todo/INDEX.md", "doing/INDEX.md"
	if err := r.Rename("todo/custom-name.md", "todo/INDEX.md"); err != nil {
		t.Fatal(err)
	}
	if err := publishTransitionJournal(r, j); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Recover(dir, req); err == nil || !strings.Contains(err.Error(), "previous binary") {
		t.Fatalf("missing migration diagnostic: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("legacy journal or cards changed")
	}
}

func discoveryFile(t *testing.T, root, name, content string) {
	t.Helper()
	file := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryArchivesAndNonCards(t *testing.T) {
	t.Parallel()
	// Exclusion names apply below the board, never to the board's ancestors.
	root := filepath.Join(t.TempDir(), "evidence", "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"README.md", "INDEX.md", "template.md", "todo/InDeX.md", "_archive/TEMPLATE.md", "evidence/task.md", "todo/evidence/task.md", "_archive/.ce/task.md", "todo/.hidden/task.md", "todo/.hidden.md"} {
		discoveryFile(t, root, name, "not a card\nid: TASK-999\n")
	}
	discoveryFile(t, root, "archive/old.md", "---\nid: TASK-7\n---\n")
	discoveryFile(t, root, "_archive/done/old.md", "---\nid: TASK-090\nstatus: done\n---\n")
	before := boardBytes(t, root)
	entries, err := List(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v error=%v", entries, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, root)) {
		t.Fatal("list changed source bytes")
	}
	created, err := Create(root, CreateRequest{Title: "after archives"})
	if err != nil || created.Card.ID != "TASK-91" {
		t.Fatalf("archive floor=%v %v", created, err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-90", Title: "duplicate"}); err == nil {
		t.Fatal("archived alias reused")
	}
}

func TestArchiveDuplicateAndAdoption(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	discoveryFile(t, root, "_archive/card.md", "---\nid: TASK-090\n---\n")
	result, err := ReserveIDs(root, nil, true)
	if err != nil || result.MaxID != "TASK-90" {
		t.Fatalf("adoption=%v %v", result, err)
	}
	discoveryFile(t, root, "todo/card.md", "---\nid: TASK-90\n---\n")
	if _, err := List(root); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("archive duplicate accepted: %v", err)
	}
}

func TestDiscoveryDoesNotOverexclude(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"todo/INDEX-extra.md", "todo/Evidence/card.md", "todo/template/card.md", "INDEX.md/card.md", "todo/evidence.md"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			discoveryFile(t, root, name, "not a valid card")
			if _, err := List(root); err == nil {
				t.Fatalf("unexpectedly excluded %s", name)
			}
		})
	}
}

func TestExcludedEntrySymlinksStillRejected(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"evidence", "INDEX.md", "_archive", "todo/evidence", "todo/TEMPLATE.md"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			if _, err := List(root); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("symlink accepted: %v", err)
			}
		})
	}
}
