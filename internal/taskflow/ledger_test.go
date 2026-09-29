package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestGitCommonDirNeedsRepository(t *testing.T) {
	if _, ok := GitCommonDir(t.TempDir()); ok {
		t.Fatal("empty directory accepted as repository")
	}
	committed := t.TempDir()
	gitInitForTest(t, committed)
	dir, ok := GitCommonDir(committed)
	if !ok {
		t.Fatal("committed repository not recognized")
	}
	if !filepath.IsAbs(dir) {
		if _, err := os.Stat(filepath.Join(committed, dir)); err != nil {
			t.Fatalf("common dir %q not resolvable: %v", dir, err)
		}
	}
}

func TestRefFloorRequiresHistory(t *testing.T) {
	root := t.TempDir()
	gitInitForTest(t, root) // has one commit, whose message carries no id
	floor, err := RefFloor(context.Background(), root, "TASK")
	if err != nil {
		t.Fatal(err)
	}
	if floor != 0 {
		t.Fatalf("RefFloor = %d, want 0", floor)
	}
	if _, err := RefFloor(context.Background(), t.TempDir(), "TASK"); err == nil {
		t.Fatal("RefFloor over a non-repository must error, not fold to zero")
	}
}
