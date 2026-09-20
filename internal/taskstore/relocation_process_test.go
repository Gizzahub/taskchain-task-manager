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

func TestRelocationProcessHelper(t *testing.T) {
	t.Parallel()
	mode := os.Getenv("TASKCHAIN_RELOCATION_HELPER")
	if mode == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_RELOCATION_BOARD")
	var req RelocationRequest
	if err := json.Unmarshal([]byte(os.Getenv("TASKCHAIN_RELOCATION_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	if mode == "contend" {
		if _, err := Create(board, CreateRequest{Title: "contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	ready := os.NewFile(3, "relocation-ready")
	control := os.NewFile(4, "relocation-control")
	defer ready.Close()
	defer control.Close()
	_, err := relocateWithStep(board, req, true, false, func(point string) error {
		if point != os.Getenv("TASKCHAIN_RELOCATION_POINT") {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		// The parent controls this wait and kills this exact process after it
		// observes durable state. No timing-only sleep masquerades as a crash.
		var command [1]byte
		_, err := io.ReadFull(control, command[:])
		return errors.Join(errors.New("helper was not killed at boundary"), err)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach requested interruption boundary")
}

func TestRelocationProcessKillAndContention(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		for _, point := range []string{"after-relocation-common-pending", "after-relocation-journal", "after-target", "after-source", "after-relocation-receipt"} {
			if !shared && point == "after-relocation-common-pending" {
				continue
			}
			t.Run(fmt.Sprintf("shared=%v/%s", shared, point), func(t *testing.T) {
				var board, other, commonLock string
				var req RelocationRequest
				if shared {
					_, board, other = sharedFixture(t)
					if _, err := EnableShared(board, false); err != nil {
						t.Fatal(err)
					}
					if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{AllWorktrees: true}); err != nil {
						t.Fatal(err)
					}
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonLock = filepath.Join(s.root.Name(), ".task-manager.lock")
					if err := release(); err != nil {
						t.Fatal(err)
					}
					req = relocationRequestFor(t, board)
				} else {
					board, req = relocationBoardFixture(t)
					other = board
				}
				runRelocationCrash(t, board, other, commonLock, point, req)
			})
		}
	}
}

func runRelocationCrash(t *testing.T, board, other, commonLock, point string, req RelocationRequest) {
	t.Helper()
	r, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := policyForBoard(r)
	if closeErr := r.Close(); err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	original, err := os.ReadFile(filepath.Join(board, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	want, _, err := prepareRelocationPatch(original, req.ID, req.Source, req.Target, policy)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(filepath.Join(board, req.Source))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(req)
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestRelocationProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_RELOCATION_HELPER=hold", "TASKCHAIN_RELOCATION_BOARD="+board, "TASKCHAIN_RELOCATION_POINT="+point, "TASKCHAIN_RELOCATION_REQUEST="+string(raw))
	cmd.ExtraFiles = []*os.File{readyW, controlR}
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	readyW.Close()
	controlR.Close()
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
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRelocationProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_RELOCATION_HELPER=contend", "TASKCHAIN_RELOCATION_BOARD="+target, "TASKCHAIN_RELOCATION_REQUEST="+string(raw))
		out, err := contender.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("process contention: %s %v", out, err)
		}
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) || !reflectEqualBoard(otherBefore, boardBytes(t, other)) {
		t.Fatal("contending process modified a board")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || cmd.ProcessState == nil {
		t.Fatalf("helper termination was not confirmed: %v %s", err, output.String())
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("helper did not terminate from SIGKILL: %v %s", cmd.ProcessState, output.String())
	}
	// Only this terminal helper's empty synthetic lock directories are removed.
	// os.Remove refuses nonempty directories; no recursive deletion or production
	// lock expiry is involved.
	for _, name := range []string{filepath.Join(board, ".task-manager.lock"), commonLock} {
		if name == "" {
			continue
		}
		info, err := os.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected fixture lock %s: %v %v", name, info, err)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	result, err := RecoverRelocation(board, req)
	if err != nil || result.Status != "completed" {
		t.Fatalf("post-kill recovery %+v: %v", result, err)
	}
	if _, err := os.Lstat(filepath.Join(board, req.Source)); !os.IsNotExist(err) {
		t.Fatalf("source remains after recovery: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(board, req.Target))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("post-kill target bytes differ: %q %v", got, err)
	}
	info, err := os.Stat(filepath.Join(board, req.Target))
	if err != nil || info.Mode().Perm() != sourceInfo.Mode().Perm() {
		t.Fatalf("post-kill target mode changed: %v %v", info, err)
	}
	if _, err := List(board); err != nil {
		t.Fatal("recovered board still gated:", err)
	}
	if _, err := List(other); err != nil {
		t.Fatal("other board still gated:", err)
	}
}
