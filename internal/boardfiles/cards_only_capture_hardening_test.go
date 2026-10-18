//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCardsOnlyPhysicalOpenedIdentity(t *testing.T) {
	root := physicalFixture(t)
	dir, err := os.Open(filepath.Join(root, "tasks/todo"))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	before, err := captureEntryIdentity(int(dir.Fd()), "a.md")
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "tasks/todo/b.md")
	if err := os.WriteFile(other, []byte("card"), 0644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	// Model an open racing with pathname substitution: the path stays A,
	// while the returned FD is B. Path-before/path-after equality is insufficient.
	data, info, err := readCaptureOpened(context.Background(), dir, "a.md", before, file)
	if err == nil || data != nil || info != nil {
		t.Fatalf("accepted wrong opened file: data=%q info=%v err=%v", data, info, err)
	}
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("refusal leaked file: %v", err)
	}
	ids := map[string]captureIdentity{}
	board, _, err := openCaptureDirectory(context.Background(), root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	ids[""], err = captureFDIdentity(board)
	if err != nil {
		t.Fatal(err)
	}
	ids["todo"] = before // deliberately a regular file identity, not the directory
	opened, err := openRecordedCaptureDirectory(context.Background(), board, "todo", ids)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Fatal("accepted mismatching reopened directory identity")
	}
	ids["todo"], err = captureEntryIdentity(int(board.Fd()), "todo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "tasks/todo"), filepath.Join(root, "tasks/old-todo")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tasks/todo"), 0755); err != nil {
		t.Fatal(err)
	}
	ids[""], err = captureFDIdentity(board)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = openRecordedCaptureDirectory(context.Background(), board, "todo", ids)
	if opened != nil {
		opened.Close()
	}
	if err == nil {
		t.Fatal("accepted replaced queued ancestor")
	}
}

type cancelCaptureRead struct{ cancel context.CancelFunc }

func (r cancelCaptureRead) Read(p []byte) (int, error) {
	p[0] = 'x'
	r.cancel()
	return 1, io.EOF
}

func TestCardsOnlyPhysicalMidReadAndWalkCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	data, err := io.ReadAll(captureContextReader{ctx, cancelCaptureRead{cancel}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-read cancellation accepted: %q %v", data, err)
	}
	root := physicalFixture(t)
	board, _, err := openCaptureDirectory(context.Background(), root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	ctx, cancel = context.WithCancel(context.Background())
	seen := 0
	err = walkCapture(ctx, board, nil, nil, func(SnapshotEntry, fs.FileInfo, captureIdentity) error {
		seen++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || seen != 1 {
		t.Fatalf("walk did not stop at cancellation: seen=%d err=%v", seen, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	s, err := captureCardsOnlyPhysicalWithHook(ctx, root, "tasks", nil, func() error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) || s.entries != nil {
		t.Fatalf("cancelled capture returned partial snapshot: %+v %v", s, err)
	}
}

func TestCardsOnlyPhysicalRejectsDetachedDirectoryDuringListing(t *testing.T) {
	root := physicalFixture(t)
	board, verify, err := openCaptureDirectory(context.Background(), root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	rootID, err := captureFDIdentity(board)
	if err != nil {
		t.Fatal(err)
	}
	dirID, err := captureEntryIdentity(int(board.Fd()), "todo")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]captureIdentity{"": rootID, "todo": dirID}
	total := 0
	err = visitRecordedCaptureDirectory(context.Background(), board, "todo", ids, func(dir *os.File) error {
		// The replacement happens after open/identity verification, before the
		// listing takes its own before-stat. Old FD pre/post equality survives.
		if err := os.Rename(filepath.Join(root, "tasks/todo"), filepath.Join(root, "detached-todo")); err != nil {
			return err
		}
		if err := os.Mkdir(filepath.Join(root, "tasks/todo"), 0755); err != nil {
			return err
		}
		return listCaptureDirectory(context.Background(), dir, "todo", nil, nil, &total,
			func(SnapshotEntry, fs.FileInfo, captureIdentity) error { return nil }, func(string, captureIdentity) {})
	})
	if err == nil {
		t.Fatal("accepted permanently detached directory FD")
	}
	if err := verify(); err != nil {
		t.Fatalf("fixture should preserve repository/board-root identity: %v", err)
	}
}

func TestCardsOnlyPhysicalDeepDescriptorBudget(t *testing.T) {
	const marker = "TASKCHAIN_CAPTURE_FD_CHILD"
	if os.Getenv(marker) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, binary, "-test.run=^TestCardsOnlyPhysicalDeepDescriptorBudget$")
		cmd.Env = append(os.Environ(), marker+"=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("low-FD capture subprocess: %v\n%s", err, out)
		}
		return
	}
	root := physicalFixture(t)
	leaf := "tasks/" + strings.Repeat("d/", 128) + "card.md"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, leaf)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, leaf), []byte("deep"), 0644); err != nil {
		t.Fatal(err)
	}
	var old unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &old); err != nil {
		t.Fatal(err)
	}
	limit := old
	if limit.Cur > 64 {
		limit.Cur = 64
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &old); err != nil {
			t.Errorf("restore subprocess descriptor limit before TempDir cleanup: %v", err)
		}
	}()
	s, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(s.entries[strings.TrimPrefix(leaf, "tasks/")].Data) != "deep" {
		t.Fatal("deep leaf lost")
	}
	board, _, err := openCaptureDirectory(context.Background(), root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	defer board.Close()
	refusal := errors.New("stop after first entry")
	for i := 0; i < 80; i++ {
		err := walkCapture(context.Background(), board, nil, nil, func(SnapshotEntry, fs.FileInfo, captureIdentity) error { return refusal })
		if !errors.Is(err, refusal) {
			t.Fatalf("error path leaked descriptors on iteration %d: %v", i, err)
		}
	}
}

func TestCardsOnlyPhysicalSharedParentsAndCancelledMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := captureTracked(ctx, []SnapshotEntry{{Path: "todo/a.md"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("tracked metadata ignored cancellation: %v", err)
	}
	entries := []SnapshotEntry{
		{Path: "a/b/c/one.md", Tracked: true},
		{Path: "a/b/c/two.md"},
		{Path: "a/b", Mode: fs.ModeDir},
	}
	s, err := newCardsOnlySnapshot(context.Background(), SnapshotWorktree, entries)
	if err != nil {
		t.Fatal(err)
	}
	for _, parent := range []string{"a", "a/b", "a/b/c"} {
		if !s.entries[parent].Tracked {
			t.Fatalf("tracked propagation lost at %s", parent)
		}
	}
	if _, err := newCardsOnlySnapshot(ctx, SnapshotWorktree, entries); !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot metadata ignored cancellation: %v", err)
	}
}

func BenchmarkCardsOnlyPhysicalHardening(b *testing.B) {
	for _, shape := range []struct {
		name  string
		depth int
		width int
	}{{"depth64", 64, 1}, {"depth128", 128, 1}, {"width256", 1, 256}} {
		b.Run(shape.name, func(b *testing.B) {
			root, err := filepath.EvalSymlinks(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			dir := filepath.Join(root, "tasks", strings.Repeat("d/", shape.depth))
			if err := os.MkdirAll(dir, 0755); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < shape.width; i++ {
				if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.md", i)), []byte("card"), 0644); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := CaptureCardsOnlyPhysical(context.Background(), root, "tasks", nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
