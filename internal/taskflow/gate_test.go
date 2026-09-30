package taskflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestLintIDParityAndCensusLine(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/999-mislabeled.md", []byte(validChoreCard)); err != nil {
		t.Fatal(err)
	}
	census, err := CollectLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if census.Clean() {
		t.Fatalf("census clean, want id parity issue")
	}
	if len(census.IDParity) != 1 || census.IDParity[0].Filename != 999 || census.IDParity[0].ID != 1 {
		t.Fatalf("id parity = %#v", census.IDParity)
	}
	var out strings.Builder
	RenderLintText(&out, census)
	for _, want := range []string{
		"TASK LINT — 0 STRAY(S), 0 STRUCTURAL ISSUE(S)",
		"ID PARITY (1 card(s) filed under a number their id does not name)",
		"  tasks/todo/999-mislabeled.md     filename 999 vs id 1 — the frontmatter id is authoritative, renumber the file to 1",
		"NOT DONE: nothing was moved or modified.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lint render missing %q", want)
		}
	}
	if want := "Error-line census: 0 stray card(s); 0 structural issue(s); 0 stage diagnostic(s); 1 id parity issue(s); 0 hidden in progress"; !strings.Contains("Error-line census: "+census.CensusLine(), want) {
		t.Errorf("census line = %q", census.CensusLine())
	}
}

func TestLintDoingZoneWarningStaysClean(t *testing.T) {
	root := t.TempDir()
	doing := strings.Replace(validChoreCard, "status: todo", "status: in-progress", 1)
	if err := writeFileForTest(root, "tasks/doing/001-working.md", []byte(doing)); err != nil {
		t.Fatal(err)
	}
	census, err := CollectLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if !census.Clean() {
		t.Fatalf("census not clean: %#v", census)
	}
	if len(census.Warnings) != 1 || census.Warnings[0].Code != "doing-zone-exit" {
		t.Fatalf("warnings = %#v", census.Warnings)
	}
	var out strings.Builder
	RenderLintText(&out, census)
	if !strings.Contains(out.String(), "card sits in doing/ but task TASK-1 has no active run in run-status states") {
		t.Fatalf("lint render = %q", out.String())
	}
}

func TestLintStrayAndStructuralDirs(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/009-stray.md",
		[]byte(strings.Replace(validChoreCard, "id: TASK-1", "id: TASK-9", 1))); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/mystery/010-lost.md",
		[]byte(strings.Replace(validChoreCard, "id: TASK-1", "id: TASK-10", 1))); err != nil {
		t.Fatal(err)
	}
	census, err := CollectLint(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(census.StrayCards) != 1 || census.StrayCards[0] != "tasks/009-stray.md" {
		t.Fatalf("strays = %#v", census.StrayCards)
	}
	if len(census.StructuralIssues) != 1 || !strings.Contains(census.StructuralIssues[0], "neither a zone, a kind, nor storage") {
		t.Fatalf("structural = %#v", census.StructuralIssues)
	}
	if census.ErrorCount() != 2 {
		t.Fatalf("error count = %d, want 2", census.ErrorCount())
	}
}

func TestPreflightJSONFieldOrderAndCounts(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/001-ready.md", []byte(validChoreCard)); err != nil {
		t.Fatal(err)
	}
	report, err := CollectPreflight(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "READY" || report.Runnable != 1 || report.Unrunnable != 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.Advisory[AdvisoryNoExecTier] != 1 {
		t.Fatalf("advisory = %#v", report.Advisory)
	}
	var out strings.Builder
	if err := RenderPreflightJSON(&out, report); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatalf("preflight json: %v\n%s", err, out.String())
	}
	// Field order is the byte contract: decode into a struct keeps Go's
	// declared order, so the marshaled keys are compared by position.
	wantOrder := []string{"verdict", "zones", "total", "runnable", "unrunnable", "blocking", "advisory", "cards", "strays"}
	rest := out.String()
	for _, key := range wantOrder {
		quoted := `"` + key + `":`
		idx := strings.Index(rest, quoted)
		if idx < 0 {
			t.Fatalf("preflight json missing key %q:\n%s", key, out.String())
		}
		rest = rest[idx:]
	}
	if !strings.Contains(out.String(), `"bytes": `) {
		t.Fatalf("preflight json missing bytes:\n%s", out.String())
	}
}

func TestPreflightNOQueueOnEmptyBoard(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/.keep", []byte("")); err != nil {
		t.Fatal(err)
	}
	report, err := CollectPreflight(root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "NO_QUEUE" {
		t.Fatalf("verdict = %q, want NO_QUEUE", report.Verdict)
	}
	var out strings.Builder
	RenderPreflightText(&out, report)
	if got := out.String(); got != "TASK PREFLIGHT — NO_QUEUE (valid cleared/reference-only board; no execution queue)\n" {
		t.Fatalf("NO_QUEUE render = %q", got)
	}
}

func TestGateVerdictsOnCheckedBindings(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	checked := "---\nid: TASK-1\ntype: chore\ntitle: \"checked\"\npriority: P3\neffort: S\nstatus: done\nquality-review: pass\nquality-review-evidence: \"receipt\"\ncreated: 2026-09-29\n---\n\n## Summary\n\ns\n\n## Completion Criteria\n\n- [x] bound | verify: `test -f absent-gate-marker.txt` — observed: 2026-09-29\n"
	if err := writeFileForTest(root, "tasks/done/001-checked.md", []byte(checked)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	outcome, err := RunGate(context.Background(), &out, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verdict != "NOT READY" || outcome.ExitCode != 1 || outcome.Summary != "task_binding_failed" {
		t.Fatalf("outcome = %+v", outcome)
	}
	rendered := out.String()
	for _, want := range []string{
		"❌ bindings  fail",
		"   tasks/done/001-checked.md:19: test -f absent-gate-marker.txt: exit status 1",
		"NOT READY — task_binding_failed (bindings)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("gate render missing %q", want)
		}
	}
}

func TestGateUnavailableOnEmptyBoard(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	if err := writeFileForTest(root, "tasks/.keep", []byte("")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	outcome, err := RunGate(context.Background(), &out, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Verdict != "UNAVAILABLE" || outcome.ExitCode != 2 || outcome.Summary != "task_gate_unavailable" {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.Contains(out.String(), "❌ bindings  unavailable\n   no cards examined under tasks: the board was not measured") {
		t.Fatalf("gate render = %q", out.String())
	}
	if !strings.Contains(out.String(), "UNAVAILABLE — task_gate_unavailable (the gate could not judge; fix the cause above)") {
		t.Fatalf("gate render = %q", out.String())
	}
}

func TestGateJSONReadySummaryCode(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	if err := writeFileForTest(root, "tasks/todo/001-passing.md", []byte(validChoreCard)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	outcome, err := RunGate(context.Background(), &out, root, true)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ExitCode != 0 || outcome.Summary != "" {
		t.Fatalf("outcome = %+v", outcome)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatalf("gate json: %v\n%s", err, out.String())
	}
	if decoded["status"] != "ready" || decoded["summary"] != "task_gate_ready" {
		t.Fatalf("gate json = %s", out.String())
	}
	if decoded["tool_revision"] != GateToolRevision {
		t.Fatalf("gate json tool_revision = %v", decoded["tool_revision"])
	}
}

func TestGateTextReadyVerdictNamesSummaryCode(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	// A checked binding that still holds: the repository's own config file.
	passing := "---\nid: TASK-1\ntype: chore\ntitle: \"still holds\"\npriority: P3\nstatus: done\ncreated: 2026-09-29\n---\n\n## Summary\n\ns\n\n## Completion Criteria\n\n- [x] holds | verify: `test -f .git/config`\n"
	if err := writeFileForTest(root, "tasks/done/001-holds.md", []byte(passing)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	outcome, err := RunGate(context.Background(), &out, root, false)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.ExitCode != 0 {
		t.Fatalf("outcome = %+v", outcome)
	}
	if !strings.Contains(out.String(), "READY — task_gate_ready (all steps pass)") {
		t.Fatalf("gate render = %q", out.String())
	}
}

func TestCriterionFileLineIncludesFrontmatter(t *testing.T) {
	root := t.TempDir()
	checked := "---\nid: TASK-1\ntype: chore\ntitle: \"x\"\nstatus: done\n---\n\n## Summary\n\ns\n\n## Completion Criteria\n\n- [x] bound | verify: `test -f x`\n"
	if err := writeFileForTest(root, "tasks/done/001-c.md", []byte(checked)); err != nil {
		t.Fatal(err)
	}
	card, err := ReadCard(root, "done/001-c.md")
	if err != nil {
		t.Fatal(err)
	}
	criteria := card.Criteria()
	if len(criteria) != 1 {
		t.Fatalf("criteria = %d", len(criteria))
	}
	if got := card.CriterionFileLine(criteria[0]); got != 14 {
		t.Fatalf("file line = %d, want 14 (4 fields + 2 fence lines, then the body's 8th line)", got)
	}
}
