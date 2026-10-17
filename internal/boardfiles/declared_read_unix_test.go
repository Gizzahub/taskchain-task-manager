//go:build unix

package boardfiles

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDeclaredReadReturnsFindingsAndOpenProjectionUsesNoFollowNonblock(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := r.WriteFile("target.md", []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.md", filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	if _, issue, err := readDeclaredFile(r, "link.md", 16); err != nil || issue != "declared file is a symlink" {
		t.Fatalf("symlink read = %q, %v", issue, err)
	}
	if file, err := openProjection(r, "link.md"); err == nil {
		closeErr := file.Close()
		t.Fatalf("no-follow open accepted symlink (close: %v)", closeErr)
	}

	pipe := filepath.Join(dir, "pipe.md")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, issue, err := readDeclaredFile(r, "pipe.md", 16); err != nil || issue != "declared file is not a regular file" {
		t.Fatalf("FIFO read = %q, %v", issue, err)
	}
	companion, err := unix.Open(pipe, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Close(companion); err != nil {
			t.Error(err)
		}
	}()
	file, err := openProjection(r, "pipe.md")
	if err != nil {
		t.Fatal(err)
	}
	flags, flagErr := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	closeErr := file.Close()
	if flagErr != nil || closeErr != nil {
		t.Fatalf("inspect FIFO descriptor: flags=%d fcntl=%v close=%v", flags, flagErr, closeErr)
	}
	if flags&unix.O_NONBLOCK == 0 {
		t.Fatalf("FIFO descriptor lacks O_NONBLOCK: %#x", flags)
	}
	t.Logf("FIFO descriptor flags: %#x (O_NONBLOCK=%#x)", flags, unix.O_NONBLOCK)
}

func TestDeclaredReadReturnsErrorForUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	path := filepath.Join(dir, "private.md")
	if err := r.WriteFile("private.md", []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root can bypass unreadable-file mode")
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	if _, issue, err := readDeclaredFile(r, "private.md", 16); err == nil || issue != "" {
		t.Fatalf("unreadable read = issue %q, error %v", issue, err)
	}
}

func TestDeclaredReadReturnsErrorForDetectableInPlaceChange(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if err := r.WriteFile("changed.md", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, issue, err := readDeclaredFileWithHook(r, "changed.md", 16, func() error {
		return r.WriteFile("changed.md", []byte("replacement"), 0o600)
	})
	if err == nil || issue != "" {
		t.Fatalf("in-place changed read = issue %q, error %v", issue, err)
	}
}
