package taskstore

import (
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestModuleTransitionRejectsOverlongTarget(t *testing.T) {
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	source := "backend/todo/" + strings.Repeat("a", 250) + "/" + strings.Repeat("b", 250) + "/" + strings.Repeat("c", 250) + "/" + strings.Repeat("d", 247) + "/TASK-1.md"
	if len(source) != 1023 {
		t.Fatalf("fixture length=%d", len(source))
	}
	if _, err := classifyModulePath(source, policy); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionTargetPath(source, "todo", "doing", policy); err == nil {
		t.Fatal("transition accepted a destination beyond the reader path limit")
	}
}

func TestModulePathUsesDeclaredScopeAndExactZone(t *testing.T) {
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}, Zones: []string{"manual"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"todo", "doing", "review", "blocked", "done", "manual", "plan", "issue", "backlog", "archive", "_archive"} {
		name := "backend/" + zone + "/auth/session/TASK-1.md"
		got, err := classifyModulePath(name, policy)
		if err != nil || got.Module != "backend" || got.Zone != zone || got.Filename != "TASK-1.md" {
			t.Fatalf("%s: %+v, %v", name, got, err)
		}
		if got.inZone("review") != "backend/review/auth/session/TASK-1.md" {
			t.Fatalf("category or filename changed: %+v", got)
		}
	}
	for _, name := range []string{
		"todo/TASK-1.md", "frontend/todo/TASK-1.md", "backend/TASK-1.md",
		"backend/category/todo/TASK-1.md", "backend/pending/TASK-1.md",
		"backend/todo/done/TASK-1.md", "backend/plan/completed/TASK-1.md",
		"backend/todo/manual/TASK-1.md", "backend/todo/issue/TASK-1.md",
		"backend/todo/archive/TASK-1.md", "backend/todo/evidence/TASK-1.md",
		"backend/todo/.hidden/TASK-1.md", "backend/todo/README.md",
		"backend/todo/TASK-1.txt", "/backend/todo/TASK-1.md",
		"backend/../backend/todo/TASK-1.md", "backend//todo/TASK-1.md",
		"backend\\todo/TASK-1.md", "backend/todo/TASK-1\x00.md",
	} {
		if _, err := classifyModulePath(name, policy); err == nil {
			t.Errorf("accepted %q", name)
		}
	}
	if _, err := classifyModulePath("backend/todo/TASK-1.md", boardpolicy.Default()); err == nil {
		t.Fatal("default policy discovered undeclared module")
	}
}

func TestModuleArchivePreservesHistoricalZoneWithoutReclassifying(t *testing.T) {
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, zone := range []string{"archive", "_archive"} {
		for _, tail := range []string{"done/auth/TASK-1.md", "plan/PLAN-1.md", "issue/done/ISSUE-1.md", "2026-01-01/doing/TASK-2.md"} {
			name := "backend/" + zone + "/" + tail
			got, err := classifyModulePath(name, policy)
			if err != nil || got.Zone != zone || got.inZone(zone) != name || policy.Workflow(got.Zone) {
				t.Fatalf("archive reclassified: %s: %+v, %v", name, got, err)
			}
		}
	}
}
