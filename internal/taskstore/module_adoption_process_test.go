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

type moduleAdoptionProcessRequest struct {
	Raw             []byte                  `json:"raw"`
	Revision        bool                    `json:"revision"`
	Options         PolicyActivationOptions `json:"options"`
	RevisionOptions PolicyRevisionOptions   `json:"revisionOptions"`
}

func TestModuleAdoptionProcessHelper(t *testing.T) {
	mode := os.Getenv("TASKCHAIN_MODULE_ADOPTION_HELPER")
	if mode == "" {
		return
	}
	board := os.Getenv("TASKCHAIN_MODULE_ADOPTION_BOARD")
	if mode == "contend" {
		if _, err := Create(board, CreateRequest{Title: "contender"}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "lock") {
			t.Fatalf("contender bypassed adoption lock: %v", err)
		}
		return
	}
	var req moduleAdoptionProcessRequest
	if err := json.Unmarshal([]byte(os.Getenv("TASKCHAIN_MODULE_ADOPTION_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	ready := os.NewFile(3, "module-adoption-ready")
	control := os.NewFile(4, "module-adoption-control")
	defer ready.Close()
	defer control.Close()
	point := os.Getenv("TASKCHAIN_MODULE_ADOPTION_POINT")
	step := func(at string) error {
		if at != point {
			return nil
		}
		if _, err := io.WriteString(ready, "ready\n"); err != nil {
			return err
		}
		var signal [1]byte
		_, err := io.ReadFull(control, signal[:])
		return errors.Join(errors.New("helper was not killed at adoption boundary"), err)
	}
	if req.Revision {
		if _, err := revisePolicyWithStep(board, req.Raw, req.RevisionOptions, step); err != nil {
			t.Fatal(err)
		}
	} else if _, err := activatePolicyWithStep(board, req.Raw, req.Options, step); err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper did not reach adoption boundary")
}

func TestModuleAdoptionProcessKillAndRecovery(t *testing.T) {
	cases := []struct {
		name     string
		shared   bool
		revision bool
		point    string
	}{
		{"local-initial-ids", false, false, "after-policy-ids"},
		{"local-revision-journal", false, true, "after-policy-journal"},
		{"shared-initial-common", true, false, "after-common-policy-pending"},
		{"shared-initial-partial", true, false, "board-0/after-policy-ids"},
		{"shared-revision-common", true, true, "after-common-policy-pending"},
		{"shared-revision-partial", true, true, "board-0/after-policy-journal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			board, other, commonLock, req := moduleAdoptionProcessFixture(t, tc.shared, tc.revision)
			runModuleAdoptionCrash(t, board, other, commonLock, tc.point, req)
		})
	}
}
func moduleAdoptionProcessFixture(t *testing.T, shared, revision bool) (string, string, string, moduleAdoptionProcessRequest) {
	t.Helper()
	if !shared {
		if !revision {
			board := moduleAdoptionBoard(t)
			return board, board, "", moduleAdoptionProcessRequest{Raw: moduleAdoptionRaw(t), Options: PolicyActivationOptions{AdoptModules: true}}
		}
		board, options := localRevisionFixture(t)
		if err := os.MkdirAll(filepath.Join(board, "backend", "todo"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeAdoptionCard(t, board, "backend/todo/TASK-7.md", "TASK-7")
		if err := os.Chmod(filepath.Join(board, "backend/todo/TASK-7.md"), 0o640); err != nil {
			t.Fatal(err)
		}
		return board, board, "", moduleAdoptionProcessRequest{Raw: moduleAdoptionRaw(t), Revision: true, RevisionOptions: PolicyRevisionOptions{ExpectedAuthorityID: options.ExpectedAuthorityID, ExpectedDigest: options.ExpectedDigest, AdoptModules: true}}
	}
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	var request moduleAdoptionProcessRequest
	if revision {
		active, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true})
		if err != nil {
			t.Fatal(err)
		}
		raw = moduleAdoptionRaw(t)
		request = moduleAdoptionProcessRequest{Raw: raw, Revision: true, RevisionOptions: PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest, AllWorktrees: true, AdoptModules: true}}
	} else {
		raw = moduleAdoptionRaw(t)
		request = moduleAdoptionProcessRequest{Raw: raw, Options: PolicyActivationOptions{AllWorktrees: true, AdoptModules: true}}
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	commonLock := filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, ".task-manager.lock")
	if err := release(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{board, other} {
		if err := os.MkdirAll(filepath.Join(dir, "backend", "todo"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeAdoptionCard(t, dir, "backend/todo/TASK-7.md", "TASK-7")
	}
	return board, other, commonLock, request
}

func runModuleAdoptionCrash(t *testing.T, board, other, commonLock, point string, req moduleAdoptionProcessRequest) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	beforeCards := adoptionProcessCards(t, board)
	otherBeforeCards := adoptionProcessCards(t, other)
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestModuleAdoptionProcessHelper$")
	cmd.Env = append(os.Environ(), "TASKCHAIN_MODULE_ADOPTION_HELPER=hold", "TASKCHAIN_MODULE_ADOPTION_BOARD="+board, "TASKCHAIN_MODULE_ADOPTION_POINT="+point, "TASKCHAIN_MODULE_ADOPTION_REQUEST="+string(payload))
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
		t.Fatalf("readiness=%q err=%v", ready, err)
	}
	beforeDuring := boardBytes(t, board)
	otherDuring := boardBytes(t, other)
	commonDuring := adoptionProcessCommon(t, commonLock)
	for _, dir := range []string{board, other} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		contender := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestModuleAdoptionProcessHelper$")
		contender.Env = append(os.Environ(), "TASKCHAIN_MODULE_ADOPTION_HELPER=contend", "TASKCHAIN_MODULE_ADOPTION_BOARD="+dir)
		if out, err := contender.CombinedOutput(); err != nil {
			cancel()
			t.Fatalf("contention %s: %s %v", dir, out, err)
		}
		cancel()
	}
	if !reflect.DeepEqual(beforeDuring, boardBytes(t, board)) || !reflect.DeepEqual(otherDuring, boardBytes(t, other)) || (commonLock != "" && !reflect.DeepEqual(commonDuring, adoptionProcessCommon(t, commonLock))) {
		t.Fatal("contender changed protected state")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || cmd.ProcessState == nil {
		t.Fatalf("helper termination=%v state=%v", err, cmd.ProcessState)
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("helper termination=%v state=%v output=%s", err, cmd.ProcessState, output.String())
	}
	locks := []string{filepath.Join(board, ".task-manager.lock"), filepath.Join(other, ".task-manager.lock"), commonLock}
	seen := map[string]bool{}
	for _, lockPath := range locks {
		if lockPath == "" || seen[lockPath] {
			continue
		}
		seen[lockPath] = true
		info, err := os.Lstat(lockPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("unexpected synthetic lock %s: %v", lockPath, err)
		}
		if err := os.Remove(lockPath); err != nil {
			t.Fatal(err)
		}
	}
	var result PolicyActivationResult
	if req.Revision {
		req.RevisionOptions.Resume = true
		result, err = RevisePolicy(board, req.Raw, req.RevisionOptions)
	} else {
		req.Options.Resume = true
		result, err = ActivatePolicy(board, req.Raw, req.Options)
	}
	if err != nil || result.Status != "completed" {
		t.Fatalf("recovery=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(beforeCards, adoptionProcessCards(t, board)) || !reflect.DeepEqual(otherBeforeCards, adoptionProcessCards(t, other)) {
		t.Fatal("recovery changed pre-existing cards")
	}
	if _, err := List(board); err != nil {
		t.Fatal(err)
	}
	if _, err := List(other); err != nil {
		t.Fatal(err)
	}
	if req.Revision {
		recovered := boardBytes(t, board)
		recoveredOther := boardBytes(t, other)
		recoveredCommon := adoptionProcessCommon(t, commonLock)
		replay, err := RevisePolicy(board, req.Raw, req.RevisionOptions)
		if err != nil || !replay.Replayed {
			t.Fatalf("revision replay=%+v err=%v", replay, err)
		}
		if !reflect.DeepEqual(recovered, boardBytes(t, board)) || !reflect.DeepEqual(recoveredOther, boardBytes(t, other)) || (commonLock != "" && !reflect.DeepEqual(recoveredCommon, adoptionProcessCommon(t, commonLock))) {
			t.Fatal("revision replay changed state")
		}
	} else {
		recovered := boardBytes(t, board)
		recoveredOther := boardBytes(t, other)
		recoveredCommon := adoptionProcessCommon(t, commonLock)
		replay, err := ActivatePolicy(board, req.Raw, req.Options)
		if err != nil || !replay.Replayed {
			t.Fatalf("activation replay=%+v err=%v", replay, err)
		}
		if !reflect.DeepEqual(recovered, boardBytes(t, board)) || !reflect.DeepEqual(recoveredOther, boardBytes(t, other)) || (commonLock != "" && !reflect.DeepEqual(recoveredCommon, adoptionProcessCommon(t, commonLock))) {
			t.Fatal("activation replay changed state")
		}
	}
}

func adoptionProcessCards(t *testing.T, board string) map[string][]byte {
	paths := []string{"todo/TASK-1.md", "backend/todo/TASK-7.md"}
	result := map[string][]byte{}
	for _, path := range paths {
		full := filepath.Join(board, filepath.FromSlash(path))
		raw, err := os.ReadFile(full)
		if err == nil {
			info, statErr := os.Stat(full)
			if statErr != nil {
				t.Fatal(statErr)
			}
			result[fmt.Sprintf("%s:%o", path, info.Mode().Perm())] = raw
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(board, "backend", "todo", "TASK-7.md")); err != nil {
		t.Fatalf("expected module card missing: %v", err)
	}
	return result
}
func adoptionProcessCommon(t *testing.T, lockPath string) []byte {
	if lockPath == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(lockPath), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
