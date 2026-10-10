package taskflow

import (
	"bytes"
	"strings"
	"testing"
)

func TestRecommendRanksPendingAndExplainsEmpty(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root,
		"tasks/todo/002-low.md", cardDoc("TASK-2", "Low one", "P3", "M", ""),
		"tasks/todo/001-high.md", cardDoc("TASK-1", "Bug one", "P2", "S", ""),
		"tasks/backlog/009-later.md", cardDoc("TASK-9", "Later", "P0", "XS", "status: pending\n"),
	)
	report, err := Recommend(root, RecommendOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recommendations) != 2 || report.Recommendations[0].Card.ID != "TASK-1" {
		t.Fatalf("ranking=%v", idsOf(report))
	}
	if report.Recommendations[0].Score != 53 {
		t.Fatalf("score=%d", report.Recommendations[0].Score)
	}
	var text bytes.Buffer
	RenderRecommendText(&text, report)
	if !strings.Contains(text.String(), "📋 Bug one\n   File: tasks/todo/001-high.md\n   Priority: Medium | Effort: S | Score: 53\n") {
		t.Fatalf("text=\n%s", text.String())
	}

	limited, err := Recommend(root, RecommendOptions{Limit: 1})
	if err != nil || len(limited.Recommendations) != 1 {
		t.Fatalf("limit: %v %v", limited, err)
	}
	holding, err := Recommend(root, RecommendOptions{Category: "backlog", Limit: 5})
	if err != nil || len(holding.Recommendations) != 1 || holding.Recommendations[0].Card.ID != "TASK-9" {
		t.Fatalf("category: %+v %v", holding, err)
	}
}

func TestRecommendDependencyFaultsAndEffort(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root,
		"tasks/todo/010-wait.md", cardDoc("TASK-10", "Wait", "P2", "S", "depends-on:\n  - TASK-2\n"),
		"tasks/doing/002-open.md", cardDoc("TASK-2", "Open", "P2", "S", ""),
		"tasks/todo/011-dangling.md", cardDoc("TASK-11", "Dangle", "P2", "S", "depends-on:\n  - TASK-999\n"),
		"tasks/todo/012-case.md", cardDoc("TASK-12", "Case", "P2", "S", "depends-on:\n  - task-2\n"),
		"tasks/todo/013-unknown.md", cardDoc("TASK-13", "Unknown effort", "P3", "weird", ""),
		"tasks/todo/014-large.md", cardDoc("TASK-14", "Large", "P0", "XL", ""),
		"tasks/done/003-old.md", cardDoc("TASK-3", "Old", "P2", "S", ""),
		"tasks/todo/015-ready.md", cardDoc("TASK-15", "Ready", "P3", "S", "depends-on:\n  - TASK-3\n"),
	)
	report, err := Recommend(root, RecommendOptions{MaxEffort: "S", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, rec := range report.Recommendations {
		got[rec.Card.ID] = true
	}
	if got["TASK-10"] || got["TASK-11"] || got["TASK-12"] || got["TASK-14"] {
		t.Fatalf("excluded cards were ranked: %v", got)
	}
	if !got["TASK-13"] || !got["TASK-15"] {
		t.Fatalf("ready cards missing: %v", got)
	}
	reasons := map[string]string{}
	for _, fault := range report.DependencyFaults {
		reasons[fault.ID] = fault.Reason
	}
	if reasons["TASK-11"] != "dangling-dependency" || reasons["TASK-12"] != "case-mismatch-dependency" {
		t.Fatalf("faults=%v", reasons)
	}
	if _, reported := reasons["TASK-10"]; reported {
		t.Fatal("an unmet predecessor was reported as a fault")
	}
}

func TestRecommendEmptyReasons(t *testing.T) {
	missing := t.TempDir()
	report, err := Recommend(missing, RecommendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.EmptyReason != "no-pending-cards" || report.Recommendations == nil && len(report.Recommendations) != 0 {
		t.Fatalf("missing dir: %+v", report.EmptyReason)
	}
	var raw bytes.Buffer
	if err := RenderRecommendJSON(&raw, report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw.String(), "\"recommendations\": []") {
		t.Fatalf("json=%s", raw.String())
	}

	empty := t.TempDir()
	if err := writeFileForTest(empty, "tasks/.keep", []byte("")); err != nil {
		t.Fatal(err)
	}
	queued, err := Recommend(empty, RecommendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if queued.EmptyReason != "no-queue-in-scope" || len(queued.MissingZones) != 5 {
		t.Fatalf("empty tree: %s %v", queued.EmptyReason, queued.MissingZones)
	}
	var text bytes.Buffer
	RenderRecommendText(&text, queued)
	if !strings.Contains(text.String(), "No pending tasks (no-queue-in-scope): tasks/todo/, tasks/doing/, tasks/review/, tasks/blocked/ and tasks/done/ do not exist") {
		t.Fatalf("text=%s", text.String())
	}
}

func cardDoc(id, title, priority, effort, extra string) string {
	return "---\nid: " + id + "\ntitle: " + title + "\npriority: " + priority + "\neffort: " + effort + "\ncreated: 2026-10-11\n" + extra + "---\n\n## Summary\n\nWork.\n\n## Completion Criteria\n\n- [ ] <observable condition>\n"
}

func writeCards(t *testing.T, root string, pairs ...string) {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("pairs")
	}
	for i := 0; i < len(pairs); i += 2 {
		if err := writeFileForTest(root, pairs[i], []byte(pairs[i+1])); err != nil {
			t.Fatal(err)
		}
	}
}

func idsOf(report *RecommendReport) []string {
	var ids []string
	for _, rec := range report.Recommendations {
		ids = append(ids, rec.Card.ID)
	}
	return ids
}
