package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestTaskGateRefusesMissingTasksDir pins the measurement refusal: a board
// with no tasks directory has nothing to measure, so the gate refuses before
// running a step, on stderr, at exit 2.
func TestTaskGateRefusesMissingTasksDir(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"gate"}, &out, &diag)
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
	if got := out.String(); got != "" {
		t.Fatalf("stdout=%q, want empty", got)
	}
	if got := diag.String(); got != "Error: no tasks directory to measure\n" {
		t.Fatalf("stderr=%q", got)
	}
}

// TestTaskGateUnknownArgumentIsAUsageRefusal keeps the dispatch split: what
// the caller typed wrong is exit 2 with the valid flags named.
func TestTaskGateUnknownArgumentIsAUsageRefusal(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	var out, diag bytes.Buffer
	code := runTask([]string{"gate", "--bogus"}, &out, &diag)
	if code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
	if !strings.Contains(diag.String(), "unknown argument: --bogus") {
		t.Fatalf("stderr=%q", diag.String())
	}
}

// TestTaskLintRefusalCarriesOnlyTheCensus keeps stdout the report and stderr
// the census: the error line is what a caller gates on, so it must name every
// category the verdict counted.
func TestTaskLintRefusalCarriesOnlyTheCensus(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/999-mislabeled.md",
		"---\nid: TASK-001\ntitle: mislabeled\ntype: chore\npriority: P3\nstatus: todo\ncreated: 2026-09-29\n---\n\n## Summary\n\nx\n\n## Completion Criteria\n\n- [ ] b | verify: `test -f x`\n")

	var out, diag bytes.Buffer
	code := runTask([]string{"lint"}, &out, &diag)
	if code != 1 {
		t.Fatalf("code=%d, want 1", code)
	}
	if !strings.Contains(out.String(), "ID PARITY (1 card(s) filed under a number their id does not name)") {
		t.Fatalf("stdout=%q", out.String())
	}
	if got := diag.String(); got != "Error: 0 stray card(s); 0 structural issue(s); 0 stage diagnostic(s); 1 id parity issue(s); 0 hidden in progress\n" {
		t.Fatalf("stderr=%q", got)
	}
}

// TestTaskPreflightJSONVerdictIsData pins the JSON mode's shape at the
// dispatch layer: a READY queue exits 0 with the verdict as requested data.
func TestTaskPreflightJSONVerdictIsData(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/001-ready.md",
		"---\nid: TASK-001\ntitle: ready\ntype: chore\npriority: P3\nstatus: todo\ncreated: 2026-09-29\n---\n\n## Summary\n\nx\n\n## Completion Criteria\n\n- [ ] b | verify: `test -f absent-marker.txt`\n")

	var out, diag bytes.Buffer
	code := runTask([]string{"preflight", "--json"}, &out, &diag)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, diag.String())
	}
	if !strings.HasPrefix(out.String(), "{\n  \"verdict\": \"READY\",") {
		t.Fatalf("stdout=%q", out.String())
	}
	if !strings.Contains(out.String(), "\"no-exec-tier\": 1") {
		t.Fatalf("stdout missing advisory: %q", out.String())
	}
}

// TestTaskGateReExecutesCheckedBindings drives the pinned re-execution flow
// end to end: a checked binding whose marker is gone fails the gate at exit 1
// with the summary code on stderr.
func TestTaskGateReExecutesCheckedBindings(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/done/001-checked.md",
		"---\nid: TASK-001\ntitle: checked\ntype: chore\npriority: P3\neffort: S\nstatus: done\nquality-review: pass\nquality-review-evidence: \"receipt\"\ncreated: 2026-09-29\n---\n\n## Summary\n\ns\n\n## Completion Criteria\n\n- [x] bound | verify: `test -f absent-gate-marker.txt` — observed: 2026-09-29\n")

	var out, diag bytes.Buffer
	code := runTask([]string{"gate"}, &out, &diag)
	if code != 1 {
		t.Fatalf("code=%d, want 1 (stdout=%s)", code, out.String())
	}
	if got := diag.String(); got != "Error: task_binding_failed\n" {
		t.Fatalf("stderr=%q", got)
	}
	if !strings.Contains(out.String(), "NOT READY — task_binding_failed (bindings)") {
		t.Fatalf("stdout=%q", out.String())
	}
}
