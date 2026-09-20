package card

import "testing"

// Snapshot is also the single-file view behind `show`. Some directories hold a
// card without expressing its status: archive marks everything under it as the
// directory the card came from, and a kind directory holds a plan or an issue,
// not a state. A workflow name below either is provenance or a category, so the
// view must not report it as the card's current status.
func TestSnapshotTreatsStatusOpaqueZonesAsNonStatus(t *testing.T) {
	raw := []byte("---\nid: TASK-1\ntitle: t\nstatus: pending\n---\n\n# t\n")
	doc, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	opaque := []string{
		"archive/done/TASK-1.md",
		"_archive/review/TASK-1.md",
		"backend/archive/done/TASK-1.md",
		"plan/done/TASK-1.md",
		"issue/blocked/TASK-1.md",
		"backlog/doing/TASK-1.md",
		"backend/plan/done/TASK-1.md",
	}
	for _, path := range opaque {
		if got := doc.Snapshot(path).Status; got != "pending" {
			t.Fatalf("%s: a status-opaque zone produced status %q", path, got)
		}
	}
	// A workflow zone reached first still decides, including when a category
	// below it carries a reserved name.
	for _, path := range []string{"done/TASK-1.md", "done/plan/TASK-1.md", "backend/done/TASK-1.md"} {
		if got := doc.Snapshot(path).Status; got != "done" {
			t.Fatalf("%s: a real workflow zone must still win, got %q", path, got)
		}
	}
}
