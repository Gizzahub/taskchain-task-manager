package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func moduleScanPolicy(t *testing.T) boardpolicy.Policy {
	t.Helper()
	policy, err := boardpolicy.New(boardpolicy.Declaration{
		Zones:      []string{"manual"},
		ZoneStatus: map[string]string{"manual": "done"},
		Modules:    []string{"backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func moduleScanPolicyNamed(t *testing.T, modules ...string) boardpolicy.Policy {
	t.Helper()
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: modules})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func writeModuleCard(t *testing.T, board, path, id, status string) {
	t.Helper()
	full := filepath.Join(board, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: " + id + "\ntitle: " + id + "\nstatus: " + status + "\n---\n\n# " + id + "\n")
	if err := os.WriteFile(full, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func listWithModulePolicy(t *testing.T, board string, policy boardpolicy.Policy) ([]Entry, error) {
	t.Helper()
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	return listLockedWithPolicy(r, "", policy)
}

func TestListLockedWithPolicyScansModulesAndPreservesZoneSemantics(t *testing.T) {
	board := claimBoard(t)
	policy := moduleScanPolicy(t)
	writeModuleCard(t, board, "backend/todo/category/TASK-2.md", "TASK-2", "done")
	writeModuleCard(t, board, "backend/manual/category/TASK-3.md", "TASK-3", "pending")
	writeModuleCard(t, board, "backend/plan/category/PLAN-1.md", "PLAN-1", "done")
	writeModuleCard(t, board, "backend/archive/done/TASK-4.md", "TASK-4", "pending")
	for _, name := range []string{"backend/README.md", "backend/todo/INDEX.md", "backend/todo/.hidden/TASK-5.md", "backend/todo/evidence/TASK-6.md"} {
		writeModuleCard(t, board, name, "TASK-99", "pending")
	}
	entries, err := listWithModulePolicy(t, board, policy)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Entry{}
	for _, entry := range entries {
		got[entry.Path] = entry
	}
	if len(got) != 5 {
		t.Fatalf("entries=%v", got)
	}
	if got["backend/todo/category/TASK-2.md"].Card.Status != "pending" {
		t.Fatalf("workflow status=%q", got["backend/todo/category/TASK-2.md"].Card.Status)
	}
	if got["backend/manual/category/TASK-3.md"].Card.Status != "done" {
		t.Fatalf("parked status=%q", got["backend/manual/category/TASK-3.md"].Card.Status)
	}
	if got["backend/plan/category/PLAN-1.md"].Card.Status != "done" {
		t.Fatalf("kind status was stolen=%q", got["backend/plan/category/PLAN-1.md"].Card.Status)
	}
	if got["backend/archive/done/TASK-4.md"].Card.Status != "pending" {
		t.Fatalf("archive status was stolen=%q", got["backend/archive/done/TASK-4.md"].Card.Status)
	}
}

func TestListLockedWithPolicyRejectsModuleShapeAndDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func(t *testing.T, board string)
		want string
	}{
		{"unknown-zone", func(t *testing.T, board string) { _ = os.MkdirAll(filepath.Join(board, "backend", "mystery"), 0o755) }, "unsupported module zone"},
		{"direct-card", func(t *testing.T, board string) { writeModuleCard(t, board, "backend/TASK-2.md", "TASK-2", "pending") }, "inside a zone"},
		{"symlink", func(t *testing.T, board string) {
			if err := os.MkdirAll(filepath.Join(board, "backend"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(board, "backend", "todo")); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"nested-symlink", func(t *testing.T, board string) {
			if err := os.MkdirAll(filepath.Join(board, "backend", "todo", "category"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(t.TempDir(), filepath.Join(board, "backend", "todo", "category", "link")); err != nil {
				t.Fatal(err)
			}
		}, "symlink"},
		{"duplicate", func(t *testing.T, board string) {
			writeModuleCard(t, board, "backend/todo/TASK-1.md", "TASK-1", "pending")
		}, "duplicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			board := claimBoard(t)
			tc.make(t, board)
			_, err := listWithModulePolicy(t, board, moduleScanPolicy(t))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("error=%v want %q", err, tc.want)
			}
		})
	}
}

func TestListLockedWithPolicyRejectsUndeclaredRootAndNormalizedDuplicate(t *testing.T) {
	board := claimBoard(t)
	if err := os.MkdirAll(filepath.Join(board, "frontend", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := listWithModulePolicy(t, board, moduleScanPolicy(t)); err == nil || !strings.Contains(err.Error(), "unsupported task directory") {
		t.Fatalf("undeclared module root accepted: %v", err)
	}

	board = filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	writeModuleCard(t, board, "backend/todo/TASK-001.md", "TASK-001", "pending")
	writeModuleCard(t, board, "frontend/todo/TASK-1.md", "TASK-1", "pending")
	if _, err := listWithModulePolicy(t, board, moduleScanPolicyNamed(t, "backend", "frontend")); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("normalized module duplicate accepted: %v", err)
	}
}

func TestListLockedWithPolicyRejectsModuleRootSymlink(t *testing.T) {
	board := claimBoard(t)
	if err := os.Symlink(t.TempDir(), filepath.Join(board, "backend")); err != nil {
		t.Fatal(err)
	}
	if _, err := listWithModulePolicy(t, board, moduleScanPolicy(t)); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("module root symlink accepted: %v", err)
	}
}
