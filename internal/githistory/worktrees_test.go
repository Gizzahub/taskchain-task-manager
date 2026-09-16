package githistory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectLinkedWorktreesSameNamespace(t *testing.T) {
	repo := historyRepo(t)
	historyCard(t, repo, "tasks/todo/card.md", "id: TASK-1\n")
	historyCommit(t, repo)
	other := filepath.Join(t.TempDir(), "linked space")
	gitTest(t, repo, "worktree", "add", "--detach", other, "HEAD")
	gitTest(t, repo, "worktree", "lock", "--reason", "synthetic\nreason", other)
	before := gitTest(t, repo, "status", "--porcelain=v1")
	a, err := InspectWorktrees(context.Background(), repo, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	b, err := InspectWorktrees(context.Background(), other, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if a.CommonDirectory != b.CommonDirectory || a.NamespaceKey != b.NamespaceKey || len(a.NamespaceKey) != 64 || len(a.Worktrees) != 2 || a.SharedReadiness != "not_evaluated" {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	locked := false
	for _, wt := range a.Worktrees {
		if wt.Locked {
			locked = wt.Detached && wt.LockReason == "synthetic\nreason"
		}
	}
	if !locked || gitTest(t, repo, "status", "--porcelain=v1") != before {
		t.Fatal("lock metadata lost or repository modified")
	}
	if _, err := os.Stat(filepath.Join(a.CommonDirectory, "taskchain-task-manager")); !os.IsNotExist(err) {
		t.Fatalf("inspection created shared state: %v", err)
	}
}

func TestInspectRetainsPrunableWorktree(t *testing.T) {
	repo := historyRepo(t)
	historyCard(t, repo, "tasks/card.md", "id: TASK-1\n")
	historyCommit(t, repo)
	other := filepath.Join(t.TempDir(), "linked")
	gitTest(t, repo, "worktree", "add", "--detach", other, "HEAD")
	if err := os.Rename(other, other+"-moved"); err != nil {
		t.Fatal(err)
	}
	report, err := InspectWorktrees(context.Background(), repo, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, wt := range report.Worktrees {
		found = found || wt.Prunable
	}
	if !found || len(report.Worktrees) != 2 {
		t.Fatalf("missing prunable record: %+v", report)
	}
}

func TestInspectBoardBoundariesAndUnborn(t *testing.T) {
	repo := historyRepo(t)
	if err := os.Mkdir(filepath.Join(repo, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectWorktrees(context.Background(), repo, "tasks"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tasks", filepath.Join(repo, "alias")); err != nil {
		t.Fatal(err)
	}
	historyCard(t, repo, "nested/.git", "invalid nested marker")
	for _, board := range []string{"missing", "Tasks", "alias", ".git", "nested", ".."} {
		if _, err := InspectWorktrees(context.Background(), repo, board); err == nil {
			t.Errorf("accepted board %q", board)
		}
	}
}

func TestInspectRejectsConcurrentTopologyChange(t *testing.T) {
	repo := historyRepo(t)
	historyCard(t, repo, "tasks/card.md", "id: TASK-1\n")
	historyCommit(t, repo)
	_, err := inspectWorktrees(context.Background(), repo, "tasks", func() error {
		gitTest(t, repo, "worktree", "add", "--detach", filepath.Join(t.TempDir(), "new"), "HEAD")
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "topology changed") {
		t.Fatalf("changed topology accepted: %v", err)
	}
}

func TestInspectRejectsCommonDirectoryReplacement(t *testing.T) {
	repo := historyRepo(t)
	historyCard(t, repo, "tasks/card.md", "id: TASK-1\n")
	historyCommit(t, repo)
	_, err := inspectWorktrees(context.Background(), repo, "tasks", func() error {
		original := filepath.Join(repo, ".git")
		backup := filepath.Join(t.TempDir(), "old-git")
		if err := os.Rename(original, backup); err != nil {
			return err
		}
		return os.CopyFS(original, os.DirFS(backup))
	})
	if err == nil || !strings.Contains(err.Error(), "topology changed") {
		t.Fatalf("common-directory replacement accepted: %v", err)
	}
}

func TestInspectBoardReplacementAndCancellation(t *testing.T) {
	repo := historyRepo(t)
	historyCard(t, repo, "tasks/card.md", "id: TASK-1\n")
	_, err := inspectWorktrees(context.Background(), repo, "tasks", func() error {
		if err := os.Rename(filepath.Join(repo, "tasks"), filepath.Join(repo, "old-tasks")); err != nil {
			return err
		}
		return os.Mkdir(filepath.Join(repo, "tasks"), 0o755)
	})
	if err == nil || !strings.Contains(err.Error(), "board identity changed") {
		t.Fatalf("board replacement accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InspectWorktrees(ctx, repo, "tasks"); err == nil {
		t.Fatal("canceled inspection succeeded")
	}
}

func TestInspectSHA256AndDistinctBoards(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init", "--object-format=sha256", "-b", "fixture")
	for _, board := range []string{"tasks", "other-tasks"} {
		if err := os.Mkdir(filepath.Join(repo, board), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a, err := InspectWorktrees(context.Background(), repo, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	b, err := InspectWorktrees(context.Background(), repo, "other-tasks")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Worktrees[0].HEAD) != 64 || a.NamespaceKey == b.NamespaceKey {
		t.Fatalf("invalid SHA256 or namespace: %+v %+v", a, b)
	}
}
