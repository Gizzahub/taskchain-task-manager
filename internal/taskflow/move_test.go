package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// initBoardForTest writes a minimal board and makes it a git repository, so
// moveFile exercises the git mv path it prefers.
func initBoardForTest(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		if err := writeFileForTest(root, rel, []byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFileForTest(root, filepath.Join(".git", "HEAD"), []byte("ref: refs/heads/master\n")); err != nil {
		t.Fatal(err)
	}
	return root
}

const moverCard = "---\nid: TASK-001\ntitle: mover\ntype: chore\nstatus: todo\n---\n\n## Summary\n\nmoves\n"

func TestMoveCardRewritesStatusAndStampsFrontmatter(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-mover.md": moverCard})
	newRel, synced, _, err := MoveCard(root, "tasks/todo/001-mover.md", MoveDestination{Zone: "doing", Status: StatusInProgress})
	if err != nil {
		t.Fatal(err)
	}
	if newRel != "doing/001-mover.md" {
		t.Fatalf("newRel = %q", newRel)
	}
	content, _ := os.ReadFile(filepath.Join(root, "tasks", "doing", "001-mover.md"))
	if !strings.Contains(string(content), "status: doing") {
		t.Fatalf("frontmatter not stamped: %s", content)
	}
	if synced {
		t.Fatal("card has no **Status** cell; synced must be false")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-mover.md")); !os.IsNotExist(err) {
		t.Fatal("source still present")
	}
}

func TestMoveCardSyncsBodyStatusCell(t *testing.T) {
	card := strings.Replace(moverCard, "## Summary", "| **Status** | [ ] Pending |\n\n## Summary", 1)
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-mover.md": card})
	_, synced, _, err := MoveCard(root, "tasks/todo/001-mover.md", MoveDestination{Zone: "doing", Status: StatusInProgress})
	if err != nil {
		t.Fatal(err)
	}
	if !synced {
		t.Fatal("status cell present; synced must be true")
	}
	content, _ := os.ReadFile(filepath.Join(root, "tasks", "doing", "001-mover.md"))
	if !strings.Contains(string(content), "[~] In Progress") {
		t.Fatalf("cell not synced: %s", content)
	}
}

func TestMoveCardRefusals(t *testing.T) {
	root := initBoardForTest(t, map[string]string{
		"tasks/todo/001-a.md":  moverCard,
		"tasks/doing/001-a.md": moverCard,
	})
	if _, _, _, err := MoveCard(root, "outside/001-a.md", MoveDestination{Zone: "doing", Status: StatusInProgress}); err == nil ||
		!strings.Contains(err.Error(), "not under") {
		t.Fatalf("outside tasks/ err = %v", err)
	}
	if _, _, _, err := MoveCard(root, "tasks/loose/001-a.md", MoveDestination{Zone: "doing", Status: StatusInProgress}); err == nil ||
		!strings.Contains(err.Error(), "not in a zone") {
		t.Fatalf("zoneless err = %v", err)
	}
	if _, _, _, err := MoveCard(root, "tasks/todo/001-a.md", MoveDestination{Zone: "doing", Status: StatusInProgress}); err == nil ||
		!strings.Contains(err.Error(), "destination already exists") {
		t.Fatalf("occupied destination err = %v", err)
	}
}

func TestMoveCardRetargetsInboundLinks(t *testing.T) {
	root := initBoardForTest(t, map[string]string{
		"tasks/todo/001-target.md": "---\nid: TASK-001\n---\n\ntarget\n",
		"tasks/todo/002-linker.md": "---\nid: TASK-002\n---\n\nSee [target](001-target.md) and [unrelated](003-other.md).\n",
	})
	_, _, links, err := MoveCard(root, "tasks/todo/001-target.md", MoveDestination{Zone: "doing", Status: StatusInProgress})
	if err != nil {
		t.Fatal(err)
	}
	if len(links.Inbound) != 1 || links.Inbound[0].Path != "tasks/todo/002-linker.md" {
		t.Fatalf("inbound = %#v", links.Inbound)
	}
	content, _ := os.ReadFile(filepath.Join(root, "tasks", "todo", "002-linker.md"))
	want := "---\nid: TASK-002\n---\n\nSee [target](../doing/001-target.md) and [unrelated](003-other.md).\n"
	if string(content) != want {
		t.Fatalf("linker = %q, want %q", content, want)
	}
}

func TestArchiveCardKeepsZoneAndRefusesDoubleArchive(t *testing.T) {
	done := strings.Replace(moverCard, "status: todo", "status: done", 1)
	root := initBoardForTest(t, map[string]string{"tasks/done/001-mover.md": done})
	newRel, _, err := ArchiveCard(root, "tasks/done/001-mover.md")
	if err != nil {
		t.Fatal(err)
	}
	if newRel != "_archive/done/001-mover.md" {
		t.Fatalf("newRel = %q, want _archive/done/001-mover.md", newRel)
	}
	if _, _, err := ArchiveCard(root, "tasks/_archive/done/001-mover.md"); err == nil ||
		!strings.Contains(err.Error(), "already under") {
		t.Fatalf("re-archive err = %v", err)
	}
}

func TestArchiveCardKeepsSupersededWord(t *testing.T) {
	done := strings.Replace(moverCard, "status: todo", "status: superseded", 1)
	root := initBoardForTest(t, map[string]string{"tasks/done/001-mover.md": done})
	if _, _, err := ArchiveCard(root, "tasks/done/001-mover.md"); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(filepath.Join(root, "tasks", "_archive", "done", "001-mover.md"))
	if !strings.Contains(string(content), "status: superseded") {
		t.Fatalf("superseded word overwritten: %s", content)
	}
}
