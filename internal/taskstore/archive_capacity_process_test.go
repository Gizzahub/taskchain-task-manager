package taskstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestArchiveCapacityProcessHelper(t *testing.T) {
	if os.Getenv("TASKCHAIN_CAPACITY_HELPER") == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_CAPACITY_BOARD")
	if os.Getenv("TASKCHAIN_CAPACITY_MODE") == "contend" {
		if _, err := Create(board, CreateRequest{Title: "capacity contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	ready := os.NewFile(3, "capacity-ready")
	control := os.NewFile(4, "capacity-control")
	defer ready.Close()
	defer control.Close()
	_, err := archiveCapacityWithStep(board, os.Getenv("TASKCHAIN_CAPACITY_ID"), true, false, func(point string) error {
		if point != os.Getenv("TASKCHAIN_CAPACITY_POINT") {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var command [1]byte
		_, readErr := io.ReadFull(control, command[:])
		return errors.Join(errors.New("capacity helper was not killed at boundary"), readErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("capacity helper did not reach interruption boundary")
}

func TestArchiveCapacitySIGKILLContentionAndRecovery(t *testing.T) {
	points := []string{"after-capacity-common-protocol", "after-capacity-local-protocol", "after-capacity-target", "after-capacity-completed", "after-capacity-common-clear"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) { runArchiveCapacityCrash(t, point) })
	}
}

func runArchiveCapacityCrash(t *testing.T, point string) {
	t.Helper()
	board, other, req := archiveProcessFixture(t, true)
	if _, err := Archive(board, req, true); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("8", 32)
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	commonLock := filepath.Join(s.root.Name(), ".task-manager.lock")
	if err := release(); err != nil {
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestArchiveCapacityProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_CAPACITY_HELPER=hold", "TASKCHAIN_CAPACITY_BOARD="+board, "TASKCHAIN_CAPACITY_ID="+id, "TASKCHAIN_CAPACITY_POINT="+point)
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
		t.Fatalf("boundary readiness %q: %v output=%s", ready, err, output.String())
	}
	before, otherBefore := boardBytes(t, board), boardBytes(t, other)
	for _, target := range []string{board, other} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArchiveCapacityProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_CAPACITY_HELPER=contend", "TASKCHAIN_CAPACITY_BOARD="+target, "TASKCHAIN_CAPACITY_MODE=contend")
		out, err := contender.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("capacity contention: %s %v", out, err)
		}
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
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
		info, err := os.Lstat(lockPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected lock %s: %v", lockPath, err)
		}
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
	}
	result, err := ArchiveCapacity(board, id, false, true)
	if err != nil || result.Status != "completed" {
		t.Fatalf("capacity recovery=%+v err=%v", result, err)
	}
	r, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	a, err := loadArchiveCapacityAdoption(r)
	if err != nil || a.Phase != "completed" {
		t.Fatalf("receipt=%+v err=%v", a, err)
	}
	_, target, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(filepath.Join(board, archivesFile))
	if err != nil || !bytes.Equal(current, target) {
		t.Fatalf("target bytes differ: %v", err)
	}
	info, err := os.Stat(filepath.Join(board, archivesFile))
	if err != nil || uint32(info.Mode().Perm()) != mode {
		t.Fatalf("target mode differs: %v", err)
	}
	if _, err := List(board); err != nil {
		t.Fatal("recovered owner board gated:", err)
	}
	if _, err := List(other); err != nil {
		t.Fatal("recovered peer board gated:", err)
	}
}
