package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNonWorkflowCompletionCannotUnblockDependency(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"plan", "plan/done", "issue", "archive", "archive/done", "_archive", "_archive/done", "done/nested"} {
		t.Run(zone, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, zone), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, zone, "card.md"), []byte("---\nid: TASK-1\nstatus: done\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "dependent", DependsOn: []string{"TASK-1"}}); err != nil {
				t.Fatal(err)
			}
			ready, err := Ready(root)
			if err != nil || len(ready) != 0 {
				t.Fatalf("ready=%v err=%v", ready, err)
			}
		})
	}
}

func TestCreateRejectsExistingCycleWithoutPublishing(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ndepends-on: [TASK-1]\n---\n")
	path := filepath.Join(root, "todo", "existing.md")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, root)
	if _, err := Create(root, CreateRequest{Title: "must not publish"}); err == nil || !strings.Contains(err.Error(), "self dependency") {
		t.Fatalf("error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("existing card changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "todo"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("cards=%v err=%v", entries, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, root)) {
		t.Fatal("invalid graph changed board or ID reservations")
	}
}
