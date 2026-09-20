package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestArchiveProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKCHAIN_ARCHIVE_HELPER") == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_ARCHIVE_BOARD")
	var req ArchiveRequest
	if err := json.Unmarshal([]byte(os.Getenv("TASKCHAIN_ARCHIVE_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TASKCHAIN_ARCHIVE_MODE") == "contend" {
		if _, err := Create(board, CreateRequest{Title: "contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	ready := os.NewFile(3, "archive-ready")
	control := os.NewFile(4, "archive-control")
	defer ready.Close()
	defer control.Close()
	_, err := archiveWithStep(board, req, true, false, func(point string) error {
		if point != os.Getenv("TASKCHAIN_ARCHIVE_POINT") {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var command [1]byte
		_, readErr := io.ReadFull(control, command[:])
		return errors.Join(errors.New("archive helper was not killed at boundary"), readErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach requested interruption boundary")
}

func TestArchiveProcessKillAndContention(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		points := []string{"after-archive-journal", "after-target", "after-source", "after-archive-receipt"}
		if shared {
			points = append([]string{"after-archive-common-pending"}, points...)
		}
		for _, point := range points {
			t.Run(fmt.Sprintf("shared=%v/%s", shared, point), func(t *testing.T) {
				board, other, req := archiveProcessFixture(t, shared)
				commonLock := ""
				if shared {
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonLock = filepath.Join(s.root.Name(), ".task-manager.lock")
					if err := release(); err != nil {
						t.Fatal(err)
					}
				}
				runArchiveCrash(t, board, other, commonLock, point, req)
			})
		}
	}
}

func archiveProcessFixture(t *testing.T, shared bool) (string, string, ArchiveRequest) {
	t.Helper()
	if !shared {
		dir, req, _ := archiveWriterFixture(t)
		return dir, dir, req
	}
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(board, "todo", "TASK-1.md")
	raw := mustReadFile(t, source)
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: independent review\n"), 1)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(board, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(board, "done", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	return board, other, ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("e", 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
}

func runArchiveCrash(t *testing.T, board, other, commonLock, point string, req ArchiveRequest) {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readyR.Close(); readyW.Close(); controlR.Close(); controlW.Close() })
	original, err := os.ReadFile(filepath.Join(board, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	originalInfo, err := os.Stat(filepath.Join(board, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestArchiveProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_ARCHIVE_HELPER=hold", "TASKCHAIN_ARCHIVE_BOARD="+board, "TASKCHAIN_ARCHIVE_POINT="+point, "TASKCHAIN_ARCHIVE_REQUEST="+string(raw))
	cmd.ExtraFiles = []*os.File{readyW, controlR}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	readyW.Close()
	controlR.Close()
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	if err := readyR.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [6]byte
	if _, err := io.ReadFull(readyR, ready[:]); err != nil || string(ready[:]) != "ready\n" {
		t.Fatalf("boundary readiness %q: %v", ready, err)
	}
	before, otherBefore := boardBytes(t, board), boardBytes(t, other)
	for _, target := range []string{board, other} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_ARCHIVE_HELPER=contend", "TASKCHAIN_ARCHIVE_BOARD="+target, "TASKCHAIN_ARCHIVE_REQUEST="+string(raw), "TASKCHAIN_ARCHIVE_MODE=contend")
		out, err := contender.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("process contention: %s %v", out, err)
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || cmd.ProcessState == nil {
		t.Fatalf("helper termination not confirmed: %v %s", err, output.String())
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("helper did not terminate from SIGKILL: %v", cmd.ProcessState)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) || !reflectEqualBoard(otherBefore, boardBytes(t, other)) {
		t.Fatal("contender modified a board")
	}
	for _, lockPath := range []string{filepath.Join(board, ".task-manager.lock"), commonLock} {
		if lockPath == "" {
			continue
		}
		info, err := os.Lstat(lockPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected synthetic lock %s: %v", lockPath, err)
		}
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
	}
	result, err := RecoverArchive(board, req)
	if err != nil || result.Status != "completed" {
		t.Fatalf("post-kill recovery=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(board, req.Source)); !os.IsNotExist(err) {
		t.Fatalf("source remains after recovery: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(board, result.Target))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("recovered target bytes differ: %v", err)
	}
	info, err := os.Stat(filepath.Join(board, result.Target))
	if err != nil || info.Mode().Perm() != originalInfo.Mode().Perm() {
		t.Fatalf("recovered target mode differs: %v", err)
	}
	if _, err := List(board); err != nil {
		t.Fatal("recovered board still gated:", err)
	}
	if _, err := List(other); err != nil {
		t.Fatal("other board still gated:", err)
	}
}
