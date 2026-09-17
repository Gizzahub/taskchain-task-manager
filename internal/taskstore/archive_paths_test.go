package taskstore

import (
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestArchiveDestinationPreservesModuleZoneAndCategory(t *testing.T) {
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	for source, target := range map[string]string{
		"done/TASK-1.md":                  "_archive/done/TASK-1.md",
		"done/auth/api/TASK-1.md":         "_archive/done/auth/api/TASK-1.md",
		"backend/done/auth/api/TASK-1.md": "backend/_archive/done/auth/api/TASK-1.md",
		"backend/plan/auth/PLAN-1.md":     "backend/_archive/plan/auth/PLAN-1.md",
		"backend/issue/auth/ISSUE-1.md":   "backend/_archive/issue/auth/ISSUE-1.md",
	} {
		_, got, err := archiveDestination(source, p)
		if err != nil || got != target {
			t.Fatalf("%s => %s %v", source, got, err)
		}
	}
	_, a, _ := archiveDestination("backend/done/auth/same.md", p)
	_, b, _ := archiveDestination("backend/done/billing/same.md", p)
	if a == b {
		t.Fatal("distinct categories collapsed")
	}
}

func TestArchiveDestinationRefusesInvalidAndAlreadyArchivedPaths(t *testing.T) {
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		"TASK-1.md", "other/done/TASK-1.md", "backend/TASK-1.md",
		"done/../TASK-1.md", "/done/TASK-1.md", "done//TASK-1.md",
		"done\\TASK-1.md", "done/.hidden/TASK-1.md", "done/README.md",
		"_archive/done/TASK-1.md", "backend/archive/done/TASK-1.md",
		"done/_archive/TASK-1.md", "done/review/TASK-1.md",
		"backend/done/pending/TASK-1.md", "done/" + strings.Repeat("x", 256) + ".md",
		"done/" + string([]byte{255}) + ".md",
	} {
		if _, _, err := archiveDestination(source, p); err == nil {
			t.Fatalf("accepted %q", source)
		}
	}
}

func TestArchiveDestinationChecksExpandedPathLimit(t *testing.T) {
	p := boardpolicy.Default()
	// Source is valid, but adding the storage segment crosses the byte limit.
	source := "done/" + strings.Repeat(strings.Repeat("a", 250)+"/", 4) + "TASK-1.md"
	if len(source) > 1023 || len(source)+len("_archive/") <= 1023 {
		t.Fatal("invalid boundary fixture")
	}
	if _, _, err := archiveDestination(source, p); err == nil || !strings.Contains(err.Error(), "destination exceeds") {
		t.Fatalf("wrong expansion result: %v", err)
	}
}
