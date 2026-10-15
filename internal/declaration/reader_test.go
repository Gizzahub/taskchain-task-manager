//go:build unix

package declaration

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReadFilePrivateAndNonMutating(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "example.json")
	original := fixture(t, "bound")
	if err := os.WriteFile(file, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "example.json"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../example.json", file, ".", "missing.json"} {
		if _, err := ReadFile(root, name); !errors.Is(err, ErrUnavailable) {
			t.Fatal(name, err)
		}
	}
	if err := os.Symlink(file, filepath.Join(root, "link.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "link.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("symlink accepted", err)
	}
	rootLink := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(rootLink, "example.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("root symlink accepted", err)
	}
	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "example.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("public file accepted", err)
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0o644 {
		t.Fatal("reader chmodded file")
	}
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "example.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("public root accepted", err)
	}
	info, _ = os.Stat(root)
	if info.Mode().Perm() != 0o755 {
		t.Fatal("reader chmodded root")
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("reader rewrote input")
	}
	entries, _ := os.ReadDir(root)
	if len(entries) != 2 {
		t.Fatal("reader created state")
	}
}

func TestOwnerProofAndFileKinds(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "directory.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	// A foreign owner is checked using a synthetic stat, without privileged chown.
	info, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if !ownerPrivate(info, 0o700) {
		t.Fatal("private current owner not recognized")
	}
	if ownerPrivate(noOwnerInfo{info}, 0o700) {
		t.Fatal("unknown owner accepted")
	}
	foreign := *info.Sys().(*syscall.Stat_t)
	foreign.Uid++
	if ownerPrivate(foreignOwnerInfo{info, &foreign}, 0o700) {
		t.Fatal("foreign owner accepted")
	}
	if err := syscall.Mkfifo(filepath.Join(root, "pipe.json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "pipe.json"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("FIFO accepted", err)
	}
}

type noOwnerInfo struct{ os.FileInfo }

func (noOwnerInfo) Sys() any { return nil }

type foreignOwnerInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (i foreignOwnerInfo) Sys() any { return i.stat }

func TestLegacyLedgerFilesNotRepaired(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"executions.json", "receipts.jsonl"} {
		file := filepath.Join(root, name)
		body := fixture(t, "legacy-execution")
		if err := os.WriteFile(file, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadFile(root, name); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		after, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(body, after) {
			t.Fatal("legacy bytes changed")
		}
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != 0o644 {
			t.Fatal("legacy mode changed")
		}
	}
}
