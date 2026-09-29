package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeFloorCountsFrontmatterID(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/007-a.md", []byte("---\nid: TASK-007\n---\n\nx\n")); err != nil {
		t.Fatal(err)
	}
	// The frontmatter id, not the filename, is the key: a card filed under a
	// non-canonical name still raises the floor.
	if err := writeFileForTest(root, "tasks/todo/weird.md", []byte("---\nid: TASK-012\n---\n\nx\n")); err != nil {
		t.Fatal(err)
	}
	// A body id line is not a card id: writing TASK-999 in a body must not
	// raise the floor.
	if err := writeFileForTest(root, "tasks/todo/003-b.md", []byte("---\nid: TASK-003\n---\n\nreferenced TASK-999 elsewhere\n")); err != nil {
		t.Fatal(err)
	}
	// A high filename with a low frontmatter id proves nothing about the floor.
	if err := writeFileForTest(root, "tasks/todo/009-fancy.md", []byte("---\nid: TASK-002\n---\n\nx\n")); err != nil {
		t.Fatal(err)
	}
	if got := TreeFloor(root, "TASK"); got != 12 {
		t.Fatalf("TreeFloor = %d, want 12", got)
	}
	if got := TreeFloor(root, "PLAN"); got != 0 {
		t.Fatalf("TreeFloor(PLAN) = %d, want 0", got)
	}
}

func TestParseCardNumber(t *testing.T) {
	if n, ok := parseCardNumber("TASK", "TASK-12"); !ok || n != 12 {
		t.Fatalf("parseCardNumber = %d, %v", n, ok)
	}
	for _, id := range []string{"TASK-", "ISSUE-3", "TASK-x", "TASK"} {
		if _, ok := parseCardNumber("TASK", id); ok {
			t.Errorf("parseCardNumber(%q) accepted", id)
		}
	}
}

func TestReservationStoreNextAndCeiling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".git", "ce")
	store := NewReservationStore(dir)
	next, err := store.Next("TASK", 4)
	if err != nil {
		t.Fatal(err)
	}
	if next != 5 {
		t.Fatalf("Next = %d, want 5", next)
	}
	// The ledger remembers the reservation even when no card follows it.
	if next, err = store.Next("TASK", 0); err != nil || next != 6 {
		t.Fatalf("Next after reservation = %d, %v; want 6", next, err)
	}
	if next, err = store.Next("PLAN", 0); err != nil || next != 1 {
		t.Fatalf("Next(PLAN) = %d, %v; want 1", next, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "card-id-reservations", "highest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"TASK\": 6") || !strings.Contains(string(data), "\"PLAN\": 1") {
		t.Fatalf("ledger = %s", data)
	}
}

func TestReservationStoreRefusesAtCeiling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ce")
	store := NewReservationStore(dir)
	if _, err := store.Next("TASK", 999); err == nil || !strings.Contains(err.Error(), "999") {
		t.Fatalf("Next(999) err = %v, want ceiling refusal", err)
	}
	// Nothing may be written by a refused reservation.
	if _, err := os.Stat(filepath.Join(dir, "card-id-reservations", "highest.json")); !os.IsNotExist(err) {
		t.Fatal("refusal wrote a ledger")
	}
}

func TestReservationStoreConcurrentNextYieldsDistinctNumbers(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ce")
	store := NewReservationStore(dir)
	const racers = 8
	results := make(chan int, racers)
	errs := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			next, err := store.Next("TASK", 0)
			if err != nil {
				errs <- err
				return
			}
			results <- next
		}()
	}
	seen := map[int]bool{}
	for i := 0; i < racers; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case next := <-results:
			if seen[next] {
				t.Fatalf("number %d handed out twice", next)
			}
			seen[next] = true
		}
	}
}

func TestGitCommonDirNeedsRepository(t *testing.T) {
	if _, ok := GitCommonDir(t.TempDir()); ok {
		t.Fatal("empty directory accepted as repository")
	}
	committed := t.TempDir()
	gitInitForTest(t, committed)
	dir, ok := GitCommonDir(committed)
	if !ok {
		t.Fatal("committed repository not recognized")
	}
	if !filepath.IsAbs(dir) {
		if _, err := os.Stat(filepath.Join(committed, dir)); err != nil {
			t.Fatalf("common dir %q not resolvable: %v", dir, err)
		}
	}
}

func TestRefFloorRequiresHistory(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root) // has one commit, whose message carries no id
	floor, err := RefFloor(context.Background(), root, "TASK")
	if err != nil {
		t.Fatal(err)
	}
	if floor != 0 {
		t.Fatalf("RefFloor = %d, want 0", floor)
	}
	if _, err := RefFloor(context.Background(), t.TempDir(), "TASK"); err == nil {
		t.Fatal("RefFloor over a non-repository must error, not fold to zero")
	}
}
