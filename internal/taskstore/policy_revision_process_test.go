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
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type policyRevisionProcessRequest struct {
	Raw     []byte                `json:"raw"`
	Options PolicyRevisionOptions `json:"options"`
}

func TestPolicyRevisionProcessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKCHAIN_POLICY_REVISION_HELPER") == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_POLICY_REVISION_BOARD")
	if os.Getenv("TASKCHAIN_POLICY_REVISION_HELPER") == "contend" {
		if _, err := Create(board, CreateRequest{Title: "contender"}); err == nil || !strings.Contains(err.Error(), "lock") {
			t.Fatalf("contender did not encounter held lock: %v", err)
		}
		return
	}
	var req policyRevisionProcessRequest
	if err := json.Unmarshal([]byte(os.Getenv("TASKCHAIN_POLICY_REVISION_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "policy-revision-ready")
	control := os.NewFile(4, "policy-revision-control")
	defer ready.Close()
	defer control.Close()
	point := os.Getenv("TASKCHAIN_POLICY_REVISION_POINT")
	_, err := revisePolicyWithStep(board, req.Raw, req.Options, func(at string) error {
		if at != point {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var command [1]byte
		return errors.Join(errors.New("helper was not killed at boundary"), func() error {
			_, err := io.ReadFull(control, command[:])
			return err
		}())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach requested interruption boundary")
}

func TestPolicyRevisionProcessKillAndContention(t *testing.T) {
	t.Parallel()
	local := []string{"after-local-pending", "after-policy", "after-policy-journal", "after-local-completed"}
	shared := []string{"after-common-policy-pending", "board-0/after-policy-journal", "board-0/after-local-completed", "after-common-policy-active"}
	for _, tc := range []struct {
		shared bool
		points []string
	}{
		{false, local}, {true, shared},
	} {
		for _, point := range tc.points {
			t.Run(fmt.Sprintf("shared=%v/%s", tc.shared, point), func(t *testing.T) {
				var board, other, commonLock string
				var options PolicyRevisionOptions
				if tc.shared {
					_, board, other, options = sharedRevisionFixture(t)
					s, release, err := acquireShared(board, false)
					if err != nil {
						t.Fatal(err)
					}
					commonLock = filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, ".task-manager.lock")
					if err := release(); err != nil {
						t.Fatal(err)
					}
				} else {
					board, options = localRevisionFixture(t)
					other = board
				}
				runPolicyRevisionCrash(t, board, other, commonLock, point, options)
			})
		}
	}
}

func runPolicyRevisionCrash(t *testing.T, board, other, commonLock, point string, options PolicyRevisionOptions) {
	t.Helper()
	raw := revisionPolicyBytes(t)
	request, err := json.Marshal(policyRevisionProcessRequest{Raw: raw, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	cardsBefore := map[string][]byte{}
	modesBefore := map[string]os.FileMode{}
	for _, dir := range []string{board, other} {
		card := filepath.Join(dir, "todo", "TASK-1.md")
		raw, err := os.ReadFile(card)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(card)
		if err != nil {
			t.Fatal(err)
		}
		cardsBefore[dir], modesBefore[dir] = raw, info.Mode().Perm()
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readyR.Close()
	defer readyW.Close()
	controlR, controlW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer controlR.Close()
	defer controlW.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPolicyRevisionProcessHelper$")
	cmd.Env = append(os.Environ(),
		"TASKCHAIN_POLICY_REVISION_HELPER=hold",
		"TASKCHAIN_POLICY_REVISION_BOARD="+board,
		"TASKCHAIN_POLICY_REVISION_POINT="+point,
		"TASKCHAIN_POLICY_REVISION_REQUEST="+string(request),
	)
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
	boardsBefore := map[string]map[string]string{board: boardBytes(t, board), other: boardBytes(t, other)}
	commonBefore := []byte(nil)
	publishedAuthority := ""
	if commonLock != "" {
		commonBefore = readCommonBytes(t, commonLock)
		var state sharedState
		if err := json.Unmarshal(commonBefore, &state); err != nil || state.Policy == nil {
			t.Fatalf("published common revision unavailable: %v", err)
		}
		publishedAuthority = state.Policy.AuthorityID
	} else {
		raw, err := os.ReadFile(filepath.Join(board, policyActivationFile))
		if err != nil {
			t.Fatal(err)
		}
		var state policyActivationState
		if err := json.Unmarshal(raw, &state); err != nil {
			t.Fatal(err)
		}
		publishedAuthority = state.AuthorityID
	}
	if publishedAuthority == "" || publishedAuthority == options.ExpectedAuthorityID {
		t.Fatal("helper did not publish a new revision authority")
	}
	for _, target := range []string{board, other} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPolicyRevisionProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_POLICY_REVISION_HELPER=contend", "TASKCHAIN_POLICY_REVISION_BOARD="+target)
		out, err := contender.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("process contention: %s %v", out, err)
		}
	}
	for dir, before := range boardsBefore {
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatalf("contending process modified board %s", dir)
		}
	}
	if commonLock != "" && !reflect.DeepEqual(commonBefore, readCommonBytes(t, commonLock)) {
		t.Fatal("contending process modified common policy")
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
	lockNames := []string{filepath.Join(board, ".task-manager.lock"), filepath.Join(other, ".task-manager.lock"), commonLock}
	seenLocks := map[string]bool{}
	for _, name := range lockNames {
		if name == "" {
			continue
		}
		if seenLocks[name] {
			continue
		}
		seenLocks[name] = true
		info, err := os.Lstat(name)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected fixture lock %s: %v %v", name, info, err)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	options.Resume = true
	result, err := RevisePolicy(board, raw, options)
	if err != nil || result.Status != "completed" {
		t.Fatalf("post-kill recovery %+v: %v", result, err)
	}
	if result.AuthorityID != publishedAuthority || result.Digest != bytesDigest(raw) {
		t.Fatalf("recovery replanned saved revision: %+v, saved authority=%s", result, publishedAuthority)
	}
	for _, dir := range []string{board, other} {
		if _, err := List(dir); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cardsBefore[dir], readCardBytes(t, dir)) {
			t.Fatalf("recovery changed card %s", dir)
		}
		info, err := os.Stat(filepath.Join(dir, "todo", "TASK-1.md"))
		if err != nil || info.Mode().Perm() != modesBefore[dir] {
			t.Fatalf("recovery changed card mode %s: %v", dir, err)
		}
	}
	recoveredBoards := map[string]map[string]string{board: boardBytes(t, board), other: boardBytes(t, other)}
	recoveredCommon := []byte(nil)
	if commonLock != "" {
		recoveredCommon = policyCommonBytes(t, board)
	}
	replayed, err := RevisePolicy(board, raw, options)
	if err != nil || !replayed.Replayed {
		t.Fatalf("receipt replay %+v: %v", replayed, err)
	}
	for dir, before := range recoveredBoards {
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatalf("replay changed board %s", dir)
		}
	}
	if commonLock != "" && !reflect.DeepEqual(recoveredCommon, policyCommonBytes(t, board)) {
		t.Fatal("replay changed common policy")
	}
}

func readCommonBytes(t *testing.T, commonLock string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(commonLock), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func readCardBytes(t *testing.T, dir string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "todo", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
