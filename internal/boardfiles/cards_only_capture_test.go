//go:build darwin || linux

package boardfiles

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func physicalFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks/todo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/todo/a.md"), []byte("card"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestCardsOnlyPhysicalTrackedResidueAndMissing(t *testing.T) {
	root := physicalFixture(t)
	for _, dir := range []string{"tasks/empty", "tasks/.ce/deep"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// An unsupported path under .ce must never be opened or enumerated.
	if err := os.WriteFile(filepath.Join(root, "tasks/.ce/deep/bad\nname"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/todo/INDEX.md"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	s, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", []SnapshotEntry{{Path: "todo/a.md"}, {Path: "todo/gone.md"}, {Path: ".ce/deep/state"}})
	if err != nil {
		t.Fatal(err)
	}
	if !s.entries["todo/gone.md"].Missing {
		t.Fatal("missing tracked leaf omitted")
	}
	if len(s.entries["todo/a.md"].Data) == 0 || !s.entries["todo/a.md"].Tracked {
		t.Fatal("tracked data missing")
	}
	if _, ok := s.entries[".ce/deep/bad\nname"]; ok {
		t.Fatal("legacy state was enumerated")
	}
	findings, err := AssessCardsOnlySnapshot(s, boardpolicy.Default())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SnapshotIssueSeverity{}
	for _, f := range findings {
		got[f.Path] = f.Severity
	}
	if got[".ce"] != SnapshotIssueError || got["todo/INDEX.md"] != SnapshotIssueResidue || got["empty"] != SnapshotIssueResidue {
		t.Fatalf("wrong grades: %+v", findings)
	}
}

func TestCardsOnlyPhysicalRejectsObservedChanges(t *testing.T) {
	for _, kind := range []string{"bytes", "add", "root", "directory-link", "leaf-link"} {
		t.Run(kind, func(t *testing.T) {
			root := physicalFixture(t)
			_, err := captureCardsOnlyPhysicalWithHook(context.Background(), root, "tasks", []SnapshotEntry{{Path: "todo/a.md"}}, func() error {
				switch kind {
				case "bytes":
					return os.WriteFile(filepath.Join(root, "tasks/todo/a.md"), []byte("changed"), 0644)
				case "add":
					return os.WriteFile(filepath.Join(root, "tasks/todo/b.md"), nil, 0644)
				case "root":
					if err := os.Rename(filepath.Join(root, "tasks"), filepath.Join(root, "old")); err != nil {
						return err
					}
					return os.Mkdir(filepath.Join(root, "tasks"), 0755)
				case "directory-link":
					if err := os.Rename(filepath.Join(root, "tasks/todo"), filepath.Join(root, "tasks/old")); err != nil {
						return err
					}
					return os.Symlink("old", filepath.Join(root, "tasks/todo"))
				default:
					if err := os.Remove(filepath.Join(root, "tasks/todo/a.md")); err != nil {
						return err
					}
					return os.Symlink("../outside", filepath.Join(root, "tasks/todo/a.md"))
				}
			})
			if err == nil {
				t.Fatal("accepted changed observation")
			}
		})
	}
}

func TestCardsOnlyPhysicalLinksAndTrackedDirectoryReplacement(t *testing.T) {
	root := physicalFixture(t)
	if err := os.Mkdir(filepath.Join(root, "tasks/todo/replaced.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../outside", filepath.Join(root, "tasks/todo/link.md")); err != nil {
		t.Fatal(err)
	}
	s, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", []SnapshotEntry{{Path: "todo/replaced.md"}, {Path: "todo/link.md", Mode: fs.ModeSymlink}})
	if err != nil {
		t.Fatal(err)
	}
	if s.entries["todo/link.md"].Mode&fs.ModeSymlink == 0 || len(s.entries["todo/link.md"].Data) != 0 {
		t.Fatal("symlink followed")
	}
	findings, err := AssessCardsOnlySnapshot(s, boardpolicy.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("missing type findings: %+v", findings)
	}
	if err := os.Symlink("tasks", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureCardsOnlyPhysical(context.Background(), root, "alias", nil); err == nil {
		t.Fatal("accepted symlink root")
	}
}

func TestCardsOnlyPhysicalRejectsMissingTrackedSymlinkAndFoldsGitMetadata(t *testing.T) {
	root := physicalFixture(t)
	if _, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", []SnapshotEntry{{Path: "todo/gone", Mode: fs.ModeSymlink}}); err == nil {
		t.Fatal("accepted missing tracked symlink")
	}
	if err := os.MkdirAll(filepath.Join(root, "tasks/.git/private"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/.git/private/state"), []byte("opaque"), 0644); err != nil {
		t.Fatal(err)
	}
	s, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", []SnapshotEntry{{Path: ".git/private/state"}})
	if err != nil {
		t.Fatal(err)
	}
	entry, found := s.entries[".git"]
	if !found || !entry.DescendantTracked || !entry.Tracked {
		t.Fatalf("Git metadata was not folded with tracked severity: %+v", entry)
	}
	if _, found := s.entries[".git/private/state"]; found {
		t.Fatal("Git metadata descendants were enumerated")
	}
}

func TestCardsOnlyPhysicalRejectsOpaqueAndAncestorLinkReplacement(t *testing.T) {
	root := physicalFixture(t)
	if err := os.Mkdir(filepath.Join(root, "tasks/.ce"), 0755); err != nil {
		t.Fatal(err)
	}
	_, err := captureCardsOnlyPhysicalWithHook(context.Background(), root, "tasks", nil, func() error {
		if err := os.Rename(filepath.Join(root, "tasks/.ce"), filepath.Join(root, "tasks/old-ce")); err != nil {
			return err
		}
		return os.Mkdir(filepath.Join(root, "tasks/.ce"), 0755)
	})
	if err == nil {
		t.Fatal("accepted opaque directory replacement")
	}
	alias := filepath.Join(t.TempDir(), "repo")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureCardsOnlyPhysical(context.Background(), alias, "tasks", nil); err == nil {
		t.Fatal("accepted symlinked repository ancestor")
	}
}

func TestCardsOnlyPhysicalLimitsAndCancellation(t *testing.T) {
	root := physicalFixture(t)
	if err := os.WriteFile(filepath.Join(root, "tasks/todo/a.md"), []byte(strings.Repeat("x", (1<<20)+1)), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", nil); err == nil {
		t.Fatal("oversize file accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureCardsOnlyPhysical(ctx, root, "tasks", nil); err == nil {
		t.Fatal("cancelled capture accepted")
	}
}
