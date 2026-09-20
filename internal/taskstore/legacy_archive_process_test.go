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

func TestLegacyArchiveProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKCHAIN_LEGACY_ARCHIVE_HELPER") == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_LEGACY_ARCHIVE_BOARD")
	var req LegacyArchiveRequest
	if err := json.Unmarshal([]byte(os.Getenv("TASKCHAIN_LEGACY_ARCHIVE_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("TASKCHAIN_LEGACY_ARCHIVE_MODE") == "contend" {
		if _, err := Create(board, CreateRequest{Title: "contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	ready := os.NewFile(3, "legacy-archive-ready")
	control := os.NewFile(4, "legacy-archive-control")
	defer ready.Close()
	defer control.Close()
	_, err := legacyArchiveWithStep(board, req, true, false, func(point string) error {
		if point != os.Getenv("TASKCHAIN_LEGACY_ARCHIVE_POINT") {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var command [1]byte
		_, readErr := io.ReadFull(control, command[:])
		return errors.Join(errors.New("legacy archive helper was not killed at boundary"), readErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach requested interruption boundary")
}

func TestLegacyArchiveProcessKillAndContention(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		points := []string{"after-archive-journal", "after-archive-receipt"}
		if shared {
			points = append([]string{"after-archive-common-pending"}, points...)
		}
		for _, point := range points {
			t.Run(fmt.Sprintf("shared=%v/%s", shared, point), func(t *testing.T) {
				board, other, req, raw, mode := legacyArchiveProcessFixture(t, shared)
				if _, err := Create(board, CreateRequest{ID: "TASK-90", Title: "legacy dependent", DependsOn: []string{req.ID}}); err != nil {
					t.Fatal(err)
				}
				ready, err := Ready(board)
				if err != nil {
					t.Fatal(err)
				}
				for _, entry := range ready {
					if entry.Card.ID == "TASK-90" {
						t.Fatal("unadopted legacy archive satisfied dependency")
					}
				}
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
				runLegacyArchiveCrash(t, board, other, commonLock, point, req, raw, mode)
			})
		}
	}
}

func legacyArchiveProcessFixture(t *testing.T, shared bool) (string, string, LegacyArchiveRequest, []byte, os.FileMode) {
	t.Helper()
	var board, other, source string
	if shared {
		board, other, _ = archiveProcessFixture(t, true)
		source = filepath.Join(board, "done", "TASK-1.md")
	} else {
		board, req, raw := legacyArchiveWriterFixture(t)
		info, err := os.Stat(filepath.Join(board, req.Source))
		if err != nil {
			t.Fatal(err)
		}
		return board, board, reqWithApproval(req), raw, info.Mode()
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(board, "_archive", "done", "TASK-1.md")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, target); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	req := LegacyArchiveRequest{ArchiveRequest: ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("f", 32), Source: "_archive/done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "legacy-adoption", Assertion: "operator verified historical archive", Rules: []byte(archiveCompletionRulesFixture)}, ExpectedMode: uint32(info.Mode().Perm()), ApproveCompletion: true}
	return board, other, req, raw, info.Mode()
}

func reqWithApproval(req LegacyArchiveRequest) LegacyArchiveRequest {
	req.ApproveCompletion = true
	return req
}

func runLegacyArchiveCrash(t *testing.T, board, other, commonLock, point string, req LegacyArchiveRequest, raw []byte, mode os.FileMode) {
	t.Helper()
	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readyR.Close(); readyW.Close() })
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { controlR.Close(); controlW.Close() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestLegacyArchiveProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_LEGACY_ARCHIVE_HELPER=hold", "TASKCHAIN_LEGACY_ARCHIVE_BOARD="+board, "TASKCHAIN_LEGACY_ARCHIVE_POINT="+point, "TASKCHAIN_LEGACY_ARCHIVE_REQUEST="+string(encoded))
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
	var signal [6]byte
	if _, err := io.ReadFull(readyR, signal[:]); err != nil || string(signal[:]) != "ready\n" {
		t.Fatalf("boundary readiness %q: %v", signal, err)
	}
	before, otherBefore := boardBytes(t, board), boardBytes(t, other)
	for _, target := range []string{board, other} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestLegacyArchiveProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_LEGACY_ARCHIVE_HELPER=contend", "TASKCHAIN_LEGACY_ARCHIVE_MODE=contend", "TASKCHAIN_LEGACY_ARCHIVE_BOARD="+target, "TASKCHAIN_LEGACY_ARCHIVE_REQUEST="+string(encoded))
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
	result, err := RecoverLegacyArchive(board, req)
	if err != nil || result.Status != "completed" || !result.CompletionEligible {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	root, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	journal, loadErr := loadArchiveJournal(root)
	closeErr := root.Close()
	if loadErr != nil || closeErr != nil {
		t.Fatalf("read recovered journal: %v", errors.Join(loadErr, closeErr))
	}
	if len(journal.Records) != 1 || journal.Records[0].Completion == nil || journal.Records[0].Completion.Provenance != "legacy-completion" {
		t.Fatalf("recovery lost legacy provenance: %+v", journal)
	}
	got, err := os.ReadFile(filepath.Join(board, req.Source))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("legacy source changed: %v", err)
	}
	info, err := os.Stat(filepath.Join(board, req.Source))
	if err != nil || info.Mode() != mode {
		t.Fatalf("legacy source mode changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(board, "todo", "TASK-1.md")); !os.IsNotExist(err) {
		t.Fatal("legacy recovery moved a historical card")
	}
	if _, err := List(board); err != nil {
		t.Fatal("recovered board still gated:", err)
	}
	if _, err := List(other); err != nil {
		t.Fatal("other board still gated:", err)
	}
	ready, err := Ready(board)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range ready {
		if entry.Card.ID == "TASK-90" {
			return
		}
	}
	t.Fatal("recovered explicit legacy approval did not satisfy dependency")
}
