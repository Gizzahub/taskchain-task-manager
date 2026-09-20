package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const planProgressRules = `schema-version: 1
archive-admission:
  fields:
    review: review-result
    evidence: review-proof
    resolution: disposition
    promoted-to: promoted
    children: child-ids
  accepted-reviews: [pass, conditional, waived]
`

// The count must reach the caller as data, not as prose in a log line. A
// consumer that has to parse a sentence cannot tell 1/2 from a failure to
// count at all.
func TestPlanProgressCommandEmitsTheCount(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	rules := filepath.Join(t.TempDir(), "archive.yaml")
	if err := os.WriteFile(rules, []byte(planProgressRules), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"init", "--dir", board, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("init = %d: %s", code, diagnostics.String())
	}
	for _, title := range []string{"one", "two"} {
		out.Reset()
		if code := run([]string{"create", "--dir", board, "--title", title, "--json"}, &out, &diagnostics); code != 0 {
			t.Fatalf("create = %d: %s", code, diagnostics.String())
		}
	}
	out.Reset()
	if code := run([]string{"create", "--dir", board, "--kind", "plan", "--title", "parent", "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("create plan = %d: %s", code, diagnostics.String())
	}
	planPath := filepath.Join(board, "plan/PLAN-1.md")
	raw, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	patched := strings.Replace(string(raw), "id: PLAN-1\n", "id: PLAN-1\nchild-ids: [TASK-1, TASK-2]\n", 1)
	if err := os.WriteFile(planPath, []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(board, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(board, "todo/TASK-1.md"), filepath.Join(board, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"plan-progress", "--dir", board, "--id", "PLAN-1", "--rules", rules, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("plan-progress = %d: %s", code, diagnostics.String())
	}
	var got struct {
		ID       string `json:"id"`
		Path     string `json:"path"`
		Total    int    `json:"total"`
		Done     int    `json:"done"`
		Children []struct {
			ID       string `json:"id"`
			Complete bool   `json:"complete"`
		} `json:"children"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", out.String(), err)
	}
	if got.ID != "PLAN-1" || got.Path != "plan/PLAN-1.md" || got.Total != 2 || got.Done != 1 {
		t.Fatalf("count = %+v, want PLAN-1 plan/PLAN-1.md 1/2", got)
	}
	if len(got.Children) != 2 || !got.Children[0].Complete || got.Children[1].Complete {
		t.Fatalf("per-child verdicts lost: %+v", got.Children)
	}
}

func TestPlanProgressCommandUsageAndRefusal(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := run([]string{"plan-progress", "--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), planProgressUsage) || diagnostics.Len() != 0 {
		t.Fatalf("help = %d %q %q", code, out.String(), diagnostics.String())
	}
	out.Reset()
	if code := run([]string{"plan-progress", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 {
		t.Fatalf("missing arguments accepted: %d %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), planProgressUsage) {
		t.Fatalf("plan-progress missing from top-level help: %d %q", code, out.String())
	}
}
