package taskflow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStateSelection(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root,
		"tasks/todo/002-low.md", cardDoc("TASK-2", "Low", "P3", "M", ""),
		"tasks/todo/001-high.md", cardDoc("TASK-1", "High", "P0", "S", ""),
		"tasks/doing/003-live.md", cardDoc("TASK-3", "Live", "P2", "S", ""),
		"tasks/doing/004-other.md", cardDoc("TASK-4", "Other", "P2", "S", ""),
		"tasks/review/005-a.md", cardDoc("TASK-5", "A", "P2", "S", "depends-on:\n  - TASK-6\n"),
		"tasks/review/006-b.md", cardDoc("TASK-6", "B", "P2", "S", "depends-on:\n  - TASK-5\n"),
		"tasks/issue/007-open.md", issueDoc("ISSUE-7", "Open", ""),
		"tasks/issue/008-shut.md", issueDoc("ISSUE-8", "Shut", "resolution: fixed\n"),
		"tasks/plan/009-plan.md", cardDoc("PLAN-9", "Plan", "P2", "S", ""),
		"tasks/backlog/010-idea.md", cardDoc("BACKLOG-10", "Idea", "P2", "S", ""),
		"tasks/done/011-done.md", cardDoc("TASK-11", "Done", "P2", "S", ""),
	)

	top, err := SelectState(root, "todo-run", nil)
	if err != nil {
		t.Fatal(err)
	}
	if top.Status != "selection-required" || len(top.Candidates) != 1 || top.Candidates[0].ID != "TASK-1" || top.Skill != "" {
		t.Fatalf("todo=%+v", top)
	}
	var text bytes.Buffer
	RenderStateText(&text, top)
	if !strings.Contains(text.String(), "todo-run: selection-required (zone=todo)\n  TASK-1 tasks/todo/001-high.md\n") {
		t.Fatalf("text=%q", text.String())
	}
	if strings.Contains(text.String(), "skill=") {
		t.Fatalf("empty skill was printed: %s", text.String())
	}

	doing, err := SelectState(root, "doing-resume", nil)
	if err != nil || doing.Status != "selection-required" || len(doing.Candidates) != 2 {
		t.Fatalf("doing=%+v %v", doing, err)
	}
	plans, err := SelectState(root, "plan-promote", nil)
	if err != nil || len(plans.Candidates) != 1 || plans.Candidates[0].ID != "PLAN-9" {
		t.Fatalf("plan=%+v %v", plans, err)
	}
	backlog, err := SelectState(root, "backlog-promote", nil)
	if err != nil || len(backlog.Candidates) != 1 || backlog.Candidates[0].Zone != "backlog" {
		t.Fatalf("backlog=%+v %v", backlog, err)
	}

	_, err = SelectState(root, "todo-run", []string{"tasks/doing/003-live.md"})
	if err == nil || !strings.Contains(err.Error(), "is outside todo/") {
		t.Fatalf("outside: %v", err)
	}
	_, err = SelectState(root, "issue-promote", []string{"ISSUE-8"})
	if err == nil || !strings.Contains(err.Error(), "issue is not open:") {
		t.Fatalf("closed: %v", err)
	}
	_, err = SelectState(root, "review-run", []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "explicit card not found") {
		t.Fatalf("missing: %v", err)
	}
	_, err = SelectState(root, "review-run", []string{"TASK-5", "TASK-6"})
	if err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("cycle: %v", err)
	}
	issue, err := SelectState(root, "issue-promote", nil)
	if err != nil || len(issue.Candidates) != 1 || issue.Candidates[0].ID != "ISSUE-7" {
		t.Fatalf("issue=%+v %v", issue, err)
	}
	if issue.Candidates[0].Description != "Keep." || len(issue.Candidates[0].DependsOn) != 0 {
		t.Fatalf("candidate description/deps: %+v", issue.Candidates[0])
	}
}

func TestStateDuplicateIDAndArchiveHidden(t *testing.T) {
	root := t.TempDir()
	writeCards(t, root,
		"tasks/todo/001-a.md", cardDoc("TASK-1", "A", "P2", "S", ""),
		"tasks/todo/002-b.md", cardDoc("TASK-1", "B", "P2", "S", ""),
		"tasks/_archive/done/003-old.md", cardDoc("TASK-3", "Old", "P2", "S", ""),
	)
	_, err := SelectState(root, "todo-run", nil)
	if err == nil || !strings.Contains(err.Error(), "duplicate card ID TASK-1:") {
		t.Fatalf("duplicate: %v", err)
	}
	root = t.TempDir()
	writeCards(t, root, "tasks/_archive/done/003-old.md", cardDoc("TASK-3", "Old", "P2", "S", ""))
	_, err = SelectState(root, "done-finalize", []string{"TASK-3"})
	if err == nil || !strings.Contains(err.Error(), "explicit card not found") {
		t.Fatalf("archive was visible: %v", err)
	}
}

func TestStateUnreadableCardFailsTheCommand(t *testing.T) {
	root := t.TempDir()
	path := "tasks/todo/001-secret.md"
	writeCards(t, root, path, cardDoc("TASK-1", "Secret", "P2", "S", ""))
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.Chmod(full, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(full, 0o644) })
	if _, err := os.ReadFile(full); err == nil {
		t.Skip("this account can still read a mode-0 file")
	}
	_, err := SelectState(root, "todo-run", nil)
	if err == nil {
		t.Fatal("an unreadable card was skipped")
	}
}
