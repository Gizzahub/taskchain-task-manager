package taskstore

import (
	"errors"
	"strings"
	"testing"
)

type recordingPlanWriter struct {
	writes   map[string]PlanProgress
	writeErr error
	calls    int
}

func (w *recordingPlanWriter) WritePlanProgress(planPath string, progress PlanProgress) (bool, error) {
	w.calls++
	if w.writeErr != nil {
		return false, w.writeErr
	}
	if w.writes == nil {
		w.writes = map[string]PlanProgress{}
	}
	w.writes[planPath] = progress
	return true, nil
}

func parentCensusFixture() []ParentCensusCard {
	return []ParentCensusCard{
		{Path: "plan/PLAN-1.md", ID: "PLAN-1", Kind: "plan", Children: []string{"TASK-1", "TASK-2"}},
		{Path: "todo/TASK-1.md", ID: "TASK-1", Parent: "PLAN-1", Zone: "pending"},
		{Path: "todo/TASK-2.md", ID: "TASK-2", Parent: "PLAN-1", Zone: "pending"},
	}
}

func TestParentCensusTwoChildrenFiftyToOneHundred(t *testing.T) {
	cards := parentCensusFixture()
	writer := &recordingPlanWriter{}

	cards[1].Path, cards[1].Zone = "done/TASK-1.md", "done"
	got := ReconcileParentPlan("PLAN-1", cards, writer)
	if got.Skipped != "" || !got.Written || got.Progress != 50 || got.Completed != 1 || got.Total != 2 {
		t.Fatalf("half-done census = %+v", got)
	}
	if writer.writes["plan/PLAN-1.md"] != NewPlanProgress(2, 1) {
		t.Fatalf("half-done write = %+v", writer.writes["plan/PLAN-1.md"])
	}

	cards[2].Path, cards[2].Zone = "done/TASK-2.md", "done"
	got = ReconcileParentPlan("PLAN-1", cards, writer)
	if got.Skipped != "" || !got.Written || got.Progress != 100 || got.Completed != 2 {
		t.Fatalf("both-done census = %+v", got)
	}
	if writer.writes["plan/PLAN-1.md"] != NewPlanProgress(2, 2) {
		t.Fatalf("both-done write = %+v", writer.writes["plan/PLAN-1.md"])
	}
}

func TestParentCensusArchiveChildIncluded(t *testing.T) {
	cards := parentCensusFixture()
	cards[1] = ParentCensusCard{Path: "_archive/done/TASK-1.md", ID: "TASK-1", Parent: "PLAN-1", Archived: true}
	writer := &recordingPlanWriter{}
	got := ReconcileParentPlan("PLAN-1", cards, writer)
	if got.Skipped != "" || got.Completed != 1 || got.Progress != 50 {
		t.Fatalf("archive child census = %+v", got)
	}
	zone, kind, archived := ClassifyCensusPath("_archive/done/TASK-1.md")
	if zone != "" || kind != "" || !archived {
		t.Fatalf("archive classify = %q %q %v", zone, kind, archived)
	}
}

func TestParentCensusNoParentSkips(t *testing.T) {
	writer := &recordingPlanWriter{}
	got := ReconcileParentPlan("", parentCensusFixture(), writer)
	if got.Skipped != ParentCensusSkipNoParent || writer.calls != 0 {
		t.Fatalf("no parent = %+v calls=%d", got, writer.calls)
	}
	move := AfterSuccessfulChildMove("", parentCensusFixture(), writer)
	if !move.CardOK || move.Census.Skipped != ParentCensusSkipNoParent {
		t.Fatalf("after move no parent = %+v", move)
	}
}

func TestParentCensusEmptyChildrenSkips(t *testing.T) {
	cards := []ParentCensusCard{
		{Path: "plan/PLAN-2.md", ID: "PLAN-2", Kind: "plan"},
		{Path: "done/TASK-3.md", ID: "TASK-3", Parent: "PLAN-2", Zone: "done"},
	}
	writer := &recordingPlanWriter{}
	got := ReconcileParentPlan("PLAN-2", cards, writer)
	if got.Skipped != ParentCensusSkipNoChildren || got.PlanPath != "plan/PLAN-2.md" || writer.calls != 0 {
		t.Fatalf("empty children = %+v calls=%d", got, writer.calls)
	}
}

func TestParentCensusWriterUnsupportedSkips(t *testing.T) {
	got := ReconcileParentPlan("PLAN-1", parentCensusFixture(), nil)
	if got.Skipped != ParentCensusSkipUnsupported || got.Written {
		t.Fatalf("unsupported writer = %+v", got)
	}
}

func TestParentCensusWriteFailureAfterCardSuccess(t *testing.T) {
	cards := parentCensusFixture()
	cards[1].Path, cards[1].Zone = "done/TASK-1.md", "done"
	writer := &recordingPlanWriter{writeErr: errors.New("read-only file system")}
	move := AfterSuccessfulChildMove("PLAN-1", cards, writer)
	if !move.CardOK {
		t.Fatal("census write failure rolled back card success")
	}
	if move.Census.Written || move.Census.Skipped != "read-only file system" {
		t.Fatalf("census = %+v, want Written=false with write error", move.Census)
	}
	if move.Census.Total != 2 || move.Census.Completed != 1 || move.Census.Progress != 50 {
		t.Fatalf("failed write still reports derived counts: %+v", move.Census)
	}
}

func TestParentCensusIgnoresDoingAndReview(t *testing.T) {
	if ChildDestinationEndsWork("in-progress", false, false) || ChildDestinationEndsWork("review", false, false) {
		t.Fatal("mid-queue destinations must not end work")
	}
	if !ChildDestinationEndsWork("done", false, false) || !ChildDestinationEndsWork("", true, false) || !ChildDestinationEndsWork("", false, true) {
		t.Fatal("done, archive, and terminal destinations must end work")
	}

	cards := parentCensusFixture()
	cards[1].Path, cards[1].Zone = "doing/TASK-1.md", "in-progress"
	cards[2].Path, cards[2].Zone = "review/TASK-2.md", "review"
	writer := &recordingPlanWriter{}
	got := ReconcileParentPlan("PLAN-1", cards, writer)
	if got.Completed != 0 || got.Progress != 0 || got.Total != 2 {
		t.Fatalf("doing/review counted as complete: %+v", got)
	}
}

func TestParentCensusTerminalChildCounts(t *testing.T) {
	cards := parentCensusFixture()
	cards[1] = ParentCensusCard{Path: "done/TASK-1.md", ID: "TASK-1", Parent: "PLAN-1", Zone: "done", Terminal: true}
	writer := &recordingPlanWriter{}
	got := ReconcileParentPlan("PLAN-1", cards, writer)
	if got.Completed != 1 || got.Progress != 50 {
		t.Fatalf("terminal child census = %+v", got)
	}
}

func TestPlanProgressStampBytes(t *testing.T) {
	raw := []byte("---\nid: PLAN-1\ntitle: probe\nchildren: [TASK-1, TASK-2]\nprogress: 0\n---\nbody\n")
	out, changed, err := StampPlanProgressBytes(raw, NewPlanProgress(2, 1))
	if err != nil || !changed {
		t.Fatalf("stamp: changed=%v err=%v", changed, err)
	}
	text := string(out)
	for _, want := range []string{"total-tasks: 2", "completed-tasks: 1", "completed-children: 1", "progress: 50"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if !strings.Contains(text, "children: [TASK-1, TASK-2]") || !strings.HasSuffix(text, "body\n") {
		t.Fatalf("stamp disturbed unrelated bytes:\n%s", text)
	}
	again, changed, err := StampPlanProgressBytes(out, NewPlanProgress(2, 1))
	if err != nil || changed || string(again) != string(out) {
		t.Fatalf("idempotent stamp failed: changed=%v err=%v", changed, err)
	}
}
