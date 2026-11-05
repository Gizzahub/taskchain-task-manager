package receiptpath

import "testing"

func section9() Profile  { return Profile{Base: "section-9", Amendment: "section-11"} }
func section10() Profile { return Profile{Base: "section-10", Amendment: "section-11"} }

func TestUnselectedProfileRefusesReceiptRules(t *testing.T) {
	if (Profile{}).Selected() || (Profile{Base: "section-9"}).Selected() {
		t.Fatal("codec or partial profile became a selection")
	}
	if _, err := ReviewDir(Profile{}, "TASK-1"); err == nil {
		t.Fatal("unselected profile returned a review directory")
	}
	if err := AdmitReviewPath(Profile{Base: "section-9", Amendment: "section-10"}, "TASK-1", "receipts/TASK-1/a.json"); err == nil {
		t.Fatal("unselected profile admitted a receipt")
	}
}

func TestReviewPathsFollowTheSelectedProfile(t *testing.T) {
	dir, err := ReviewDir(section9(), "TASK-1")
	if err != nil || dir != "receipts/TASK-1" {
		t.Fatalf("section-9 dir: %s %v", dir, err)
	}
	if err := AdmitReviewPath(section9(), "TASK-1", "receipts/TASK-1/review.json"); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{
		"docs/task-reviews/TASK-1/review.json",
		"receipts/TASK-2/review.json",
		"/receipts/TASK-1/review.json",
		"receipts/TASK-1/../TASK-1/review.json",
		"receipts/TASK-1/review.json/",
	} {
		if err := AdmitReviewPath(section9(), "TASK-1", rel); err == nil {
			t.Fatalf("section-9 admitted %s", rel)
		}
	}
	dir, err = ReviewDir(section10(), "TASK-1")
	if err != nil || dir != "docs/task-reviews/TASK-1" {
		t.Fatalf("section-10 dir: %s %v", dir, err)
	}
	if err := AdmitReviewPath(section10(), "TASK-1", "docs/task-reviews/TASK-1/review.json"); err != nil {
		t.Fatal(err)
	}
	if err := AdmitReviewPath(section10(), "TASK-1", "receipts/TASK-1/review.json"); err == nil {
		t.Fatal("section-10 admitted an in-board receipt")
	}
}

func TestLegacyLocationIsAWarningOnly(t *testing.T) {
	warn, ok, err := LegacyReviewWarning(section9(), "TASK-1", "docs/task-reviews/TASK-1/old.json")
	if err != nil || !ok || warn.Path != "docs/task-reviews/TASK-1/old.json" || warn.Contract == "" || warn.Action == "" {
		t.Fatalf("section-9 legacy: %+v %v %v", warn, ok, err)
	}
	if _, ok, err := LegacyReviewWarning(section9(), "TASK-1", "docs/task-reviews/TASK-1.md"); err != nil || !ok {
		t.Fatalf("flat legacy: %v %v", ok, err)
	}
	if _, ok, err := LegacyReviewWarning(section9(), "TASK-1", "receipts/TASK-1/new.json"); err != nil || ok {
		t.Fatal("new section-9 path was classified as legacy")
	}
	warn, ok, err = LegacyReviewWarning(section10(), "TASK-1", "tasks/receipts/TASK-1/old.json")
	if err != nil || !ok || warn.Cause == "" {
		t.Fatalf("section-10 legacy: %+v %v %v", warn, ok, err)
	}
}

func TestOwnershipRootsMatchTheProfile(t *testing.T) {
	got, err := OwnershipDirs(section9(), "tasks", "TASK-1")
	if err != nil || len(got) != 2 || got[0] != "tasks/receipts/TASK-1" || got[1] != "docs/task-reviews/TASK-1" {
		t.Fatalf("section-9 roots: %v %v", got, err)
	}
	got, err = OwnershipDirs(section10(), "tasks", "TASK-1")
	if err != nil || len(got) != 1 || got[0] != "docs/task-reviews/TASK-1" {
		t.Fatalf("section-10 roots: %v %v", got, err)
	}
	for _, root := range got {
		if root == "tasks/receipts/TASK-1" {
			t.Fatal("section-10 kept the in-board receipt root")
		}
	}
}

func TestPlanStatusAndOrphanScope(t *testing.T) {
	if PlanStatusFinding("done") != "" || PlanStatusFinding("") == "" || PlanStatusFinding("manual") == "" || PlanStatusFinding("closed") == "" {
		t.Fatal("plan status vocabulary drifted")
	}
	if !PlanCleanupStatus("cancelled") || PlanCleanupStatus("pending") {
		t.Fatal("plan cleanup status drifted")
	}
	all, err := OrphanScan(section9(), "all")
	if err != nil || !all.TrackedSection9 || !all.WorktreeResidue || all.ExternalSection10 {
		t.Fatalf("section-9 all: %+v %v", all, err)
	}
	staged, err := OrphanScan(section10(), "staged")
	if err != nil || staged.ExternalSection10 || staged.WorktreeResidue || staged.TrackedSection9 {
		t.Fatalf("section-10 staged: %+v %v", staged, err)
	}
	external, err := OrphanScan(section10(), "worktree")
	if err != nil || !external.ExternalSection10 || external.TrackedSection9 {
		t.Fatalf("section-10 worktree: %+v %v", external, err)
	}
	orphan, err := ClassifyAnchorOrphan(section9(), "gate", "TASK-1", true, map[string]bool{}, true)
	if err != nil || orphan.Class != "structure" || orphan.Exit != 1 {
		t.Fatalf("tracked orphan: %+v %v", orphan, err)
	}
	residue, err := ClassifyAnchorOrphan(section9(), "worktree", "TASK-1", true, map[string]bool{}, false)
	if err != nil || residue.Class != "residue" || residue.Exit != 0 {
		t.Fatalf("residue: %+v %v", residue, err)
	}
	info, err := ClassifyAnchorOrphan(section10(), "all", "TASK-9", true, map[string]bool{}, true)
	if err != nil || info.Class != "informational" || info.Exit != 0 {
		t.Fatalf("external orphan: %+v %v", info, err)
	}
	missing, err := ClassifyAnchorOrphan(section9(), "all", "", false, map[string]bool{}, true)
	if err != nil || missing.Class != "unavailable" || missing.Exit != 2 {
		t.Fatalf("unknown id: %+v %v", missing, err)
	}
	stagedExternal, err := ClassifyAnchorOrphan(section10(), "staged", "TASK-9", true, map[string]bool{}, true)
	if err != nil || stagedExternal.Class != "not-scanned" {
		t.Fatalf("staged external scan: %+v %v", stagedExternal, err)
	}
}

func TestReceiptShapeAndPreflight(t *testing.T) {
	applies, finding, err := InBoardReceiptFinding(section9(), "receipts/TASK-1/note.md")
	if err != nil || !applies || finding != "" {
		t.Fatalf("anchor: %v %q %v", applies, finding, err)
	}
	if _, finding, err := InBoardReceiptFinding(section9(), "receipts/note.md"); err != nil || finding == "" {
		t.Fatal("flat receipts file was accepted")
	}
	if _, finding, err := InBoardReceiptFinding(section9(), "receipts/not-an-id/note.md"); err != nil || finding == "" {
		t.Fatal("non-id receipt directory was accepted")
	}
	if _, finding, err := InBoardReceiptFinding(section10(), "todo/receipts/TASK-1/note.md"); err != nil || finding == "" {
		t.Fatal("section-10 in-board receipt was accepted")
	}
	if err := PreflightZone(section9(), "decision"); err == nil {
		t.Fatal("decision zone was accepted")
	}
	if err := PreflightZone(section9(), "todo"); err != nil {
		t.Fatal(err)
	}
	generation, active := ActiveManual([]string{
		"manual-0123456789abcdef0123456789abcdef.entry.json",
		"manual-0123456789abcdef0123456789abcdef.close.json",
		"manual-ffffffffffffffffffffffffffffffff.entry.json",
	})
	if !active || generation != "ffffffffffffffffffffffffffffffff" {
		t.Fatalf("active generation: %s %v", generation, active)
	}
}
