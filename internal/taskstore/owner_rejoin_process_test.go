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

// TestOwnerRejoinProcessHelper runs one rejoin in a child process and parks at
// a named cutpoint so the parent can SIGKILL it there.  The plan and payload
// arrive as files because that is exactly how an operator transports them.
func TestOwnerRejoinProcessHelper(t *testing.T) {
	if os.Getenv("TASKCHAIN_REJOIN_HELPER") == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_REJOIN_BOARD")
	if os.Getenv("TASKCHAIN_REJOIN_MODE") == "contend" {
		if _, err := Create(board, CreateRequest{Title: "rejoin contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	planRaw, err := os.ReadFile(os.Getenv("TASKCHAIN_REJOIN_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(os.Getenv("TASKCHAIN_REJOIN_PAYLOAD"))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := DecodeOwnerRejoinPlan(planRaw)
	if err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "rejoin-ready")
	control := os.NewFile(4, "rejoin-control")
	defer ready.Close()
	defer control.Close()
	apply := applyOwnerRejoinSameCommon
	if !plan.SourceCommonAvailable {
		apply = applyOwnerRejoinIndependentClone
	}
	err = apply(board, plan, payload, func(point string) error {
		if point != os.Getenv("TASKCHAIN_REJOIN_POINT") {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var command [1]byte
		_, readErr := io.ReadFull(control, command[:])
		return errors.Join(errors.New("rejoin helper was not killed at boundary"), readErr)
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("rejoin helper did not reach interruption boundary")
}

func TestSameCommonOwnerRejoinSIGKILLReachesExactlyOneState(t *testing.T) {
	points := []string{
		"after-owner-rejoin-plan",
		"after-owner-rejoin-payload",
		"after-owner-rejoin-local-pending",
		"after-owner-rejoin-common-pending",
		"after-owner-rejoin-local-protocol",
		"after-owner-rejoin-capacity-artifact",
		"after-owner-rejoin-local-completed",
		"after-owner-rejoin-common-clear",
	}
	for _, point := range points {
		t.Run(point, func(t *testing.T) { runOwnerRejoinCrash(t, point) })
	}
}

// runOwnerRejoinCrash kills a rejoin at one cutpoint and proves the board
// reaches exactly one of its two legitimate states.  Before the resume the
// board is either untouched or not yet admitted at all - there is no third
// state a reader can act on - and after the resume it is the completed rejoin.
func runOwnerRejoinCrash(t *testing.T, point string) {
	t.Helper()
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{})
	work := t.TempDir()
	planRaw, err := OwnerRejoinPlanBytes(fx.plan)
	if err != nil {
		t.Fatal(err)
	}
	planPath, payloadPath := filepath.Join(work, "plan.json"), filepath.Join(work, "payload.bin")
	if err := os.WriteFile(planPath, planRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payloadPath, fx.payload, 0o600); err != nil {
		t.Fatal(err)
	}
	original := boardBytes(t, fx.target)
	sourceBefore := boardBytes(t, fx.source)

	s, release, err := acquireShared(fx.target, false)
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerRejoinProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_REJOIN_HELPER=hold", "TASKCHAIN_REJOIN_BOARD="+fx.target,
		"TASKCHAIN_REJOIN_PLAN="+planPath, "TASKCHAIN_REJOIN_PAYLOAD="+payloadPath, "TASKCHAIN_REJOIN_POINT="+point)
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
	if err := readyR.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var ready [6]byte
	if _, err := io.ReadFull(readyR, ready[:]); err != nil || string(ready[:]) != "ready\n" {
		t.Fatalf("boundary readiness %q: %v output=%s", ready, err, output.String())
	}

	// A concurrent writer in either worktree must be refused while the rejoin
	// holds its locks, and must leave both boards byte for byte as they were.
	parked, sourceParked := boardBytes(t, fx.target), boardBytes(t, fx.source)
	for _, target := range []string{fx.target, fx.source} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOwnerRejoinProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_REJOIN_HELPER=contend", "TASKCHAIN_REJOIN_BOARD="+target, "TASKCHAIN_REJOIN_MODE=contend")
		out, err := contender.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("rejoin contention at %s: %s %v", target, out, err)
		}
	}
	if !reflectEqualBoard(parked, boardBytes(t, fx.target)) || !reflectEqualBoard(sourceParked, boardBytes(t, fx.source)) {
		t.Fatal("a refused concurrent writer modified a board")
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

	// Locks are the killed process's, not garbage: they are removed here
	// explicitly, exactly as an operator would after confirming the death.
	for _, lockPath := range []string{filepath.Join(fx.target, ".task-manager.lock"), commonLock} {
		info, err := os.Lstat(lockPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected lock %s: %v", lockPath, err)
		}
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
	}

	// The interrupted board is never a third readable state.  It is exactly one
	// of: the board it was, the completed rejoin, or a board that refuses to be
	// read at all until it is resumed.  Anything admitted as readable must be
	// one of the two endpoints, never a half-applied mixture.
	if !reflectEqualBoard(original, boardBytes(t, fx.target)) {
		if _, err := Ready(fx.target); err == nil {
			r := mustOwnerRejoinRoot(t, fx.target)
			done, completedErr := ownerRejoinCompletedLocal(r)
			if completedErr != nil || done.RejoinID != fx.plan.RejoinID {
				t.Fatalf("a half-applied rejoin at %s was admitted as a board: %v", point, completedErr)
			}
		}
	}

	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatalf("resume after SIGKILL at %s: %v", point, err)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatalf("resumed board gated at %s: %v", point, err)
	}
	r := mustOwnerRejoinRoot(t, fx.target)
	completed, err := ownerRejoinCompletedLocal(r)
	if err != nil || completed.RejoinID != fx.plan.RejoinID {
		t.Fatalf("resume did not reach the planned completed state at %s: %v", point, err)
	}
	// The source keeps its own bytes throughout: a rejoin moves authority, and
	// a crash in the middle of it is not licence to rewrite the other board.
	if !reflectEqualBoard(sourceBefore, boardBytes(t, fx.source)) {
		t.Fatalf("source board was modified across the interrupted rejoin at %s", point)
	}
}

func TestOwnerRejoinTamperedPendingStateIsRefusedWithoutMutation(t *testing.T) {
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{})
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, func(point string) error {
		if point == "after-owner-rejoin-local-pending" {
			return errors.New("stop at the pending receipt")
		}
		return nil
	}); err == nil {
		t.Fatal("the interruption did not stop the transaction")
	}
	receipt := filepath.Join(fx.target, ownerRejoinReceiptFile)
	raw, err := os.ReadFile(receipt)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(fx.plan.RejoinID), []byte(strings.Repeat("e", 32)), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("the pending receipt did not name the rejoin")
	}
	if err := os.WriteFile(receipt, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, fx.target)
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err == nil {
		t.Fatal("a tampered pending receipt was accepted")
	}
	after := boardBytes(t, fx.target)
	delete(before, ".task-manager.lock")
	delete(after, ".task-manager.lock")
	if !reflectEqualBoard(before, after) {
		t.Fatal("a refused tampered resume mutated the board")
	}
}

// TestOwnerRejoinUpgradedBoardExportHelper materialises a completed protocol-6
// board at a caller-named path so an out-of-process binary can be pointed at
// it.  It is inert unless that path is given, because the old-binary barrier
// is verified against a binary built from a protocol-5-era checkout, which a
// single `go test` run cannot produce for itself.
func TestOwnerRejoinUpgradedBoardExportHelper(t *testing.T) {
	into := os.Getenv("TASKCHAIN_REJOIN_EXPORT")
	if into == "" {
		return
	}
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{})
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatal(err)
	}
	// The source board shares the target's common directory, so the rejoin's
	// protocol upgrade legitimately covers it too and it cannot serve as the
	// control.  The control has to be a board on a common directory no rejoin
	// has touched, so that a refusal of the upgraded board is a statement about
	// protocol 6 rather than about everything this fixture builds.
	_, control, _ := protocol5SharedFixture(t)
	if _, err := EnableShared(control, false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(into, []byte(fx.target+"\n"+fx.source+"\n"+control+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The board lives under the test's own temporary directory, and a board is
	// bound to its own absolute path, so it cannot be copied somewhere durable
	// and must instead be held alive here until the caller says it is finished
	// with it.  The signal is a sentinel file rather than stdin, because `go
	// test` gives a test binary no stdin to wait on.
	deadline := time.Now().Add(5 * time.Minute)
	for {
		if _, err := os.Stat(into + ".done"); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("caller never released the exported board")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
