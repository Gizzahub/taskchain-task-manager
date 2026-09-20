package taskstore

import (
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// statusByID lists a board through both scans and indexes what each card reports.
func statusByID(t *testing.T, board string, policy boardpolicy.Policy) map[string]string {
	t.Helper()
	entries, err := listWithModulePolicy(t, board, policy)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Card.ID] = e.Card.Status
	}
	return got
}

// The top-level scan and the module scan fill the same result slice, so they
// must grade the same card body identically. They did not: a module path pins
// the zone at a fixed position and rejects workflow-looking categories, while
// the top-level scan derived status from any matching segment and so read an
// archived card's provenance directory as its current status.
func TestZoneStatusAgreesAcrossScansForArchiveProvenance(t *testing.T) {
	board := claimBoard(t)
	policy := moduleScanPolicyNamed(t, "backend")
	writeModuleCard(t, board, "archive/done/TASK-91.md", "TASK-91", "pending")
	writeModuleCard(t, board, "backend/archive/done/TASK-92.md", "TASK-92", "pending")

	got := statusByID(t, board, policy)
	if got["TASK-91"] != "pending" || got["TASK-92"] != "pending" {
		t.Fatalf("archive provenance must not become status: top-level=%q module=%q", got["TASK-91"], got["TASK-92"])
	}
}

// A kind directory carries no status of its own, so a workflow-looking category
// beneath it is filing, not a zone. Frontmatter is the only source there.
func TestZoneStatusIgnoresWorkflowLookingCategoryUnderKindZone(t *testing.T) {
	board := claimBoard(t)
	policy := moduleScanPolicyNamed(t, "backend")
	writeModuleCard(t, board, "plan/done/TASK-93.md", "TASK-93", "custom")

	if got := statusByID(t, board, policy)["TASK-93"]; got != "custom" {
		t.Fatalf("kind zone must return frontmatter unchanged, got %q", got)
	}
}

// A parked zone declared without a status stores the empty string, and Status()
// treats empty as unmapped. Frontmatter is the only source, in both scans. This
// held before the unification too; it is pinned so the unification cannot
// quietly start inventing a status for a zone that declares none.
func TestZoneStatusParkedWithoutDeclaredStatusKeepsFrontmatter(t *testing.T) {
	board := claimBoard(t)
	policy, err := boardpolicy.New(boardpolicy.Declaration{Zones: []string{"icebox"}, Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	writeModuleCard(t, board, "icebox/TASK-94.md", "TASK-94", "custom")
	writeModuleCard(t, board, "backend/icebox/TASK-95.md", "TASK-95", "custom")

	got := statusByID(t, board, policy)
	if got["TASK-94"] != "custom" || got["TASK-95"] != "custom" {
		t.Fatalf("statusless parked zone must return frontmatter: top-level=%q module=%q", got["TASK-94"], got["TASK-95"])
	}
}

// A declared parked status still wins over frontmatter, in both scans. Like the
// test above, this passed before the unification -- it guards the branch the
// unification replaced, not new behaviour.
func TestZoneStatusParkedWithDeclaredStatusOverridesFrontmatter(t *testing.T) {
	board := claimBoard(t)
	policy, err := boardpolicy.New(boardpolicy.Declaration{
		Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "done"}, Modules: []string{"backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeModuleCard(t, board, "manual/TASK-96.md", "TASK-96", "pending")
	writeModuleCard(t, board, "backend/manual/TASK-97.md", "TASK-97", "pending")

	got := statusByID(t, board, policy)
	if got["TASK-96"] != "done" || got["TASK-97"] != "done" {
		t.Fatalf("declared parked status must win: top-level=%q module=%q", got["TASK-96"], got["TASK-97"])
	}
}

// The workflow zone still wins over frontmatter. This is the contract the
// unification must not have weakened.
func TestZoneStatusWorkflowZoneStillOverridesFrontmatter(t *testing.T) {
	board := claimBoard(t)
	policy := moduleScanPolicyNamed(t, "backend")
	writeModuleCard(t, board, "done/TASK-98.md", "TASK-98", "open")
	writeModuleCard(t, board, "backend/done/TASK-99.md", "TASK-99", "open")

	got := statusByID(t, board, policy)
	if got["TASK-98"] != "done" || got["TASK-99"] != "done" {
		t.Fatalf("workflow zone must win: top-level=%q module=%q", got["TASK-98"], got["TASK-99"])
	}
}
