package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// planProgressBoard seeds a plan with two declared children, both pending.
func planProgressBoard(t *testing.T, children string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"first child", "second child"} {
		if _, err := Create(root, CreateRequest{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(root, CreateRequest{Kind: "plan", Title: "parent"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "plan/PLAN-1.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), "id: PLAN-1\n", "id: PLAN-1\nchild-ids: ["+children+"]\n", 1)
	if patched == string(raw) {
		t.Fatal("plan fixture did not gain a children field")
	}
	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// markDone moves a card into the done zone by rewriting its path, which is the
// only thing the census reads for a live card.
func markDone(t *testing.T, root, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, from))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, to)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, to), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, from)); err != nil {
		t.Fatal(err)
	}
}

func TestPlanProgressCountsDeclaredChildren(t *testing.T) {
	root := planProgressBoard(t, "TASK-1, TASK-2")
	req := PlanProgressRequest{ID: "PLAN-1", Rules: []byte(archiveCompletionRulesFixture)}

	got, err := ReadPlanProgress(root, req)
	if err != nil {
		t.Fatalf("progress with no child done: %v", err)
	}
	if got.Total != 2 || got.Done != 0 {
		t.Fatalf("pending census = %d/%d, want 0/2", got.Done, got.Total)
	}
	if got.Path != "plan/PLAN-1.md" || got.ID != "PLAN-1" {
		t.Fatalf("census did not bind the plan it counted: %+v", got)
	}

	markDone(t, root, "todo/TASK-1.md", "done/TASK-1.md")
	got, err = ReadPlanProgress(root, req)
	if err != nil {
		t.Fatalf("progress with one child done: %v", err)
	}
	if got.Total != 2 || got.Done != 1 {
		t.Fatalf("half census = %d/%d, want 1/2", got.Done, got.Total)
	}

	markDone(t, root, "todo/TASK-2.md", "done/TASK-2.md")
	got, err = ReadPlanProgress(root, req)
	if err != nil {
		t.Fatalf("progress with both children done: %v", err)
	}
	if got.Total != 2 || got.Done != 2 {
		t.Fatalf("full census = %d/%d, want 2/2", got.Done, got.Total)
	}
}

// An archived child stays counted. Completion is the archive journal binding,
// not the path: a card moved into _archive by hand is not complete, and one
// that went through Archive stays complete after it leaves the done zone.
// Both halves are asserted so neither reads as vacuous.
func TestPlanProgressCountsArchivedChild(t *testing.T) {
	root := planProgressBoard(t, "TASK-1, TASK-2")
	req := PlanProgressRequest{ID: "PLAN-1", Rules: []byte(archiveCompletionRulesFixture)}

	source := filepath.Join(root, "todo/TASK-1.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: checked\n"), 1)
	if err := os.WriteFile(source, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(root, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	done, err := ReadPlanProgress(root, req)
	if err != nil || done.Done != 1 {
		t.Fatalf("done child not counted: %+v %v", done, err)
	}

	archiveReq := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("d", 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	if _, err := Archive(root, archiveReq, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "done/TASK-1.md")); !os.IsNotExist(err) {
		t.Fatalf("child did not leave the done zone: %v", err)
	}
	after, err := ReadPlanProgress(root, req)
	if err != nil {
		t.Fatalf("progress after archive: %v", err)
	}
	if after.Done != 1 || !after.Children[0].Complete {
		t.Fatalf("archived child stopped counting: %+v", after)
	}

	// The control: a bare move into the archive path is not a completion.
	markDone(t, root, "todo/TASK-2.md", "_archive/done/TASK-2.md")
	bare, err := ReadPlanProgress(root, req)
	if err != nil {
		t.Fatalf("progress after bare move: %v", err)
	}
	if bare.Children[1].Complete {
		t.Fatal("a card moved into _archive by hand counted as complete")
	}
}

func TestPlanProgressNamesWhyItCannotCount(t *testing.T) {
	rules := []byte(archiveCompletionRulesFixture)
	for _, tc := range []struct {
		name     string
		children string
		id       string
		rules    []byte
		want     string
	}{
		{name: "no such plan", children: "TASK-1", id: "PLAN-9", rules: rules, want: "not found"},
		{name: "empty children", children: "", id: "PLAN-1", rules: rules, want: "declares no children"},
		{name: "rules do not name the children field", children: "TASK-1", id: "PLAN-1", rules: []byte("schema-version: 1\narchive-admission:\n  accepted-reviews: [pass]\n"), want: "missing archive config field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := planProgressBoard(t, tc.children)
			_, err := ReadPlanProgress(root, PlanProgressRequest{ID: tc.id, Rules: tc.rules})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// A work card is not a plan. Counting one would report a vacuous 0/0 instead of
// refusing, and a consumer cannot tell those apart.
func TestPlanProgressRefusesNonPlanCard(t *testing.T) {
	root := planProgressBoard(t, "TASK-1")
	_, err := ReadPlanProgress(root, PlanProgressRequest{ID: "TASK-1", Rules: []byte(archiveCompletionRulesFixture)})
	if err == nil || !strings.Contains(err.Error(), "not a plan") {
		t.Fatalf("work card accepted as a plan: %v", err)
	}
}
