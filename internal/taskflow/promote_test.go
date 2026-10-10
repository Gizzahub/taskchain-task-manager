package taskflow

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromoteDryRunWritesNothing(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	writeCards(t, root, "tasks/issue/001-leak.md", issueDoc("ISSUE-1", "Leak", ""))
	before := readTree(t, root)
	result, err := Promote(context.Background(), root, PromoteOptions{Targets: []string{"issue-1"}, DryRun: true, Created: "2026-10-11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].IssueID != "ISSUE-1" {
		t.Fatalf("candidates=%+v", result.Candidates)
	}
	if readTree(t, root) != before {
		t.Fatal("dry-run changed the tree")
	}
	var text bytes.Buffer
	RenderPromoteText(&text, result)
	if text.String() != "📋 ISSUE-1 would be promoted (tasks/issue/001-leak.md)\n" {
		t.Fatalf("text=%q", text.String())
	}
}

func TestPromoteLinksBothHalvesAndSkipsClosed(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	writeCards(t, root,
		"tasks/issue/001-leak.md", issueDoc("ISSUE-1", "Leak", ""),
		"tasks/issue/002-closed.md", issueDoc("ISSUE-2", "Closed", "resolution: fixed\n"),
		"tasks/todo/009-other.md", cardDoc("TASK-9", "Other", "P2", "S", ""),
	)
	result, err := Promote(context.Background(), root, PromoteOptions{AllOpen: true, Created: "2026-10-11"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Promoted) != 1 || result.Promoted[0].IssueID != "ISSUE-1" || result.Promoted[0].TaskID != "TASK-010" {
		t.Fatalf("promoted=%+v", result.Promoted)
	}
	if len(result.Skipped) != 1 || !strings.Contains(result.Skipped[0].Reason, "resolved as fixed") {
		t.Fatalf("skipped=%+v", result.Skipped)
	}
	issue, err := os.ReadFile(filepath.Join(root, "tasks/issue/001-leak.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(issue), "promoted-to: [TASK-010]\n") || !strings.Contains(string(issue), "## Summary\n\nKeep.\n") {
		t.Fatalf("stamp changed the issue body or missed the link:\n%s", issue)
	}
	task, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.Promoted[0].TaskPath)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(task), "- ISSUE-1 — tasks/issue/001-leak.md\n") {
		t.Fatalf("sources missing:\n%s", task)
	}
	_, err = Promote(context.Background(), root, PromoteOptions{Targets: []string{"ISSUE-1"}, Created: "2026-10-11"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(filepath.Join(root, "tasks/issue/001-leak.md"))
	if err != nil || string(again) != string(issue) {
		t.Fatal("a second promote rewrote an existing link")
	}
}

func TestPromoteRefusalsAndRollback(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root)
	writeCards(t, root,
		"tasks/todo/001-task.md", cardDoc("TASK-1", "Not an issue", "P2", "S", ""),
		"tasks/issue/002-open.md", issueDoc("ISSUE-2", "Open", ""),
		"tasks/issue/003-same.md", issueDoc("ISSUE-2", "Same", ""),
		"tasks/issue/004-broken.md", "---\nid: ISSUE-4\ntitle: Broken\n",
	)
	_, err := Promote(context.Background(), root, PromoteOptions{Targets: []string{"TASK-1"}})
	if err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("outside: %v", err)
	}
	_, err = Promote(context.Background(), root, PromoteOptions{Targets: []string{"missing"}})
	if err == nil || !strings.Contains(err.Error(), `no card in this corpus answers to "missing"`) {
		t.Fatalf("missing: %v", err)
	}
	_, err = Promote(context.Background(), root, PromoteOptions{Targets: []string{"ISSUE-2"}})
	if err == nil || !strings.Contains(err.Error(), "names 2 cards") {
		t.Fatalf("ambiguous: %v", err)
	}
	_, err = Promote(context.Background(), root, PromoteOptions{Targets: []string{""}})
	if err == nil || !strings.Contains(err.Error(), "empty target") {
		t.Fatalf("empty: %v", err)
	}
	before := countMarkdown(t, root)
	_, err = Promote(context.Background(), root, PromoteOptions{Targets: []string{"tasks/issue/004-broken.md"}, Created: "2026-10-11"})
	if err == nil || !strings.Contains(err.Error(), "no frontmatter") {
		t.Fatalf("unterminated: %v", err)
	}
	if countMarkdown(t, root) != before {
		t.Fatal("a failed stamp left a TASK file behind")
	}
}

func issueDoc(id, title, extra string) string {
	return "---\nid: " + id + "\ntitle: " + title + "\ntype: bug\n" + extra + "---\n\n## Summary\n\nKeep.\n"
}

func readTree(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			return nil
		}
		body, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(root, p)
		b.WriteString(rel)
		b.Write(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func countMarkdown(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(root, "tasks"), func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			n++
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}
