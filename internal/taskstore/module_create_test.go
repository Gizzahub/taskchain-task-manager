package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModuleCreateRejectsSymlinkWithoutReservation(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := os.Symlink(out, filepath.Join(board, "backend/todo/alias")); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(board, idsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "unsafe", Module: "backend", Category: "alias/child"}); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
	after, err := os.ReadFile(filepath.Join(board, idsFile))
	if err != nil || string(before) != string(after) {
		t.Fatal("symlink refusal changed reservations")
	}
	entries, err := os.ReadDir(out)
	if err != nil || len(entries) != 0 {
		t.Fatal("symlink refusal wrote outside board")
	}
}

func TestModuleCreateDestinations(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ kind, category, want string }{
		{"task", "auth/api", "backend/todo/auth/api/TASK-8.md"},
		{"plan", "design", "backend/plan/design/PLAN-3.md"},
	} {
		entry, err := Create(board, CreateRequest{Title: "scoped", Kind: tc.kind, Module: "backend", Category: tc.category})
		if err != nil || entry.Path != tc.want {
			t.Fatalf("entry=%+v err=%v", entry, err)
		}
	}
	if _, err := List(board); err != nil {
		t.Fatal(err)
	}
	legacy, err := Create(board, CreateRequest{Title: "legacy"})
	if err != nil || legacy.Path != "todo/TASK-9.md" {
		t.Fatalf("legacy=%+v err=%v", legacy, err)
	}
}

func TestModuleCreateRejectsUnsafeScopeWithoutReservation(t *testing.T) {
	t.Parallel()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ module, category string }{
		{"", "auth"}, {"unknown", ""}, {"backend", "../escape"},
		{"backend", "/absolute"}, {"backend", "auth/../api"},
		{"backend", ".hidden"}, {"backend", "done"}, {"backend", "auth/"},
		{"backend", strings.Repeat("x", 256)},
		{"backend", strings.Repeat(strings.Repeat("x", 200)+"/", 5) + "end"},
	} {
		before := boardBytes(t, board)
		if _, err := Create(board, CreateRequest{Title: "invalid", Module: tc.module, Category: tc.category}); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatalf("invalid request mutated board: %+v", tc)
		}
	}
}
