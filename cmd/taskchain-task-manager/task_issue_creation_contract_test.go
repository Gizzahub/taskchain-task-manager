package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskNewIssueCreationContract(t *testing.T) {
	t.Run("explicit discovery and criterion round trip", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		initGitRepo(t)
		var out, diag bytes.Buffer
		criterion := "observed | verify: `test -f absent-cli-issue-marker.txt`"
		if code := runTask([]string{"new", "issue", "--title", "Issue probe", "--slug", "issue-probe", "--discovered-in", "TASK-123", "--criterion", criterion}, &out, &diag); code != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
		}
		raw, err := os.ReadFile(filepath.Join(dir, "tasks", "issue", "001-issue-probe.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"discovered-in: \"TASK-123\"\n", "## Completion Criteria\n\n- [ ] " + criterion + "\n"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("card missing %q:\n%s", want, raw)
			}
		}
	})

	t.Run("omitted discovery writes placeholder and fails validation", func(t *testing.T) {
		dir := t.TempDir()
		t.Chdir(dir)
		initGitRepo(t)
		var out, diag bytes.Buffer
		if code := runTask([]string{"new", "issue", "--title", "Empty discovery", "--slug", "empty-discovery"}, &out, &diag); code != 1 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
		}
		if got, want := out.String(), "✅ Created tasks/issue/001-empty-discovery.md (ISSUE-001)\n   ❌ discovered-in: discovered-in is required for issue cards; pass --discovered-in REF\n"; got != want {
			t.Fatalf("stdout=%q want=%q", got, want)
		}
		if got, want := diag.String(), "Error: tasks/issue/001-empty-discovery.md was written but does not validate\n"; got != want {
			t.Fatalf("stderr=%q want=%q", got, want)
		}
		raw, err := os.ReadFile(filepath.Join(dir, "tasks", "issue", "001-empty-discovery.md"))
		if err != nil || !strings.Contains(string(raw), "discovered-in: \"\"\n") {
			t.Fatalf("raw=%q err=%v", raw, err)
		}
	})
}

func TestTaskNewIssueCreationRefusalsDoNotWriteOrReserve(t *testing.T) {
	for name, args := range map[string][]string{
		"task discovery":          {"new", "task", "--title", "t", "--discovered-in", "TASK-123"},
		"plan criterion":          {"new", "plan", "--title", "p", "--criterion", "x | verify: `true`"},
		"backlog criterion":       {"new", "backlog", "--title", "b", "--criterion", "x | verify: `true`"},
		"newline criterion":       {"new", "issue", "--title", "i", "--discovered-in", "TASK-123", "--criterion", "x\ny"},
		"carriage return":         {"new", "issue", "--title", "i", "--discovered-in", "TASK-123", "--criterion", "x\ry"},
		"missing discovery value": {"new", "issue", "--title", "i", "--discovered-in"},
		"empty discovery value":   {"new", "issue", "--title", "i", "--discovered-in", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			initGitRepo(t)
			var out, diag bytes.Buffer
			if code := runTask(args, &out, &diag); code != 2 || out.Len() != 0 || diag.Len() == 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
			}
			if _, err := os.Stat(filepath.Join(dir, "tasks")); !os.IsNotExist(err) {
				t.Fatalf("refusal wrote tasks: %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, ".git", "ce", "card-id-reservations")); !os.IsNotExist(err) {
				t.Fatalf("refusal reserved an id: %v", err)
			}
		})
	}
}
