package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRelocationFilesRecovery(t *testing.T) {
	for _, point := range []string{"after-stage", "after-target", "after-source"} {
		t.Run(point, func(t *testing.T) {
			r, dir := relocationFileFixture(t)
			stop := errors.New("interrupted")
			err := relocateFiles(r, "module/plan/category/card.md", "module/todo/category/card.md", []byte("original"), []byte("patched"), 0o640, func() error { return nil }, func(p string) error {
				if p == point {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("interruption: %v", err)
			}
			for i := 0; i < 2; i++ {
				if err := relocateFiles(r, "module/plan/category/card.md", "module/todo/category/card.md", []byte("original"), []byte("patched"), 0o640, func() error { return nil }, nil); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := r.Lstat("module/plan/category/card.md"); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("source still present: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "module/todo/category/card.md"))
			if err != nil || string(raw) != "patched" {
				t.Fatalf("target=%q err=%v", raw, err)
			}
		})
	}
}

func TestRelocationFilesPreserveConflicts(t *testing.T) {
	for _, conflict := range []string{"target-race", "source-edit", "target-edit", "authority"} {
		t.Run(conflict, func(t *testing.T) {
			r, dir := relocationFileFixture(t)
			authorized := true
			err := relocateFiles(r, "module/plan/category/card.md", "module/todo/category/card.md", []byte("original"), []byte("patched"), 0o640, func() error {
				if !authorized {
					return errors.New("authority changed")
				}
				return nil
			}, func(p string) error {
				name := ""
				if conflict == "target-race" && p == "after-stage" || conflict == "target-edit" && p == "after-target" {
					name = "module/todo/category/card.md"
				}
				if conflict == "source-edit" && p == "after-target" {
					name = "module/plan/category/card.md"
				}
				if conflict == "authority" && p == "after-target" {
					authorized = false
				}
				if name != "" {
					return os.WriteFile(filepath.Join(dir, name), []byte("external"), 0o640)
				}
				return nil
			})
			if err == nil {
				t.Fatal("conflict accepted")
			}
			raw, err := os.ReadFile(filepath.Join(dir, "module/plan/category/card.md"))
			want := "original"
			if conflict == "source-edit" {
				want = "external"
			}
			if err != nil || string(raw) != want {
				t.Fatalf("source destroyed: %q %v", raw, err)
			}
			if conflict == "target-race" || conflict == "target-edit" {
				raw, err := os.ReadFile(filepath.Join(dir, "module/todo/category/card.md"))
				if err != nil || string(raw) != "external" {
					t.Fatalf("external target overwritten: %q %v", raw, err)
				}
			}
		})
	}
}

func TestRelocationFilesRejectAncestorSymlink(t *testing.T) {
	r, dir := relocationFileFixture(t)
	if err := os.Symlink("module", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := relocateFiles(r, "alias/plan/category/card.md", "alias/todo/category/card.md", []byte("original"), []byte("patched"), 0o640, func() error { return nil }, nil); err == nil {
		t.Fatal("intermediate symlink accepted")
	}
	if _, err := r.Lstat("module/plan/category/card.md"); err != nil {
		t.Fatal(err)
	}
}

func relocationFileFixture(t *testing.T) (*os.Root, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "module/plan/category"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "module/plan/category/card.md")
	if err := os.WriteFile(name, []byte("original"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0o640); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r, dir
}
