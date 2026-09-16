package taskstore

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func statusRepairFixture(t *testing.T) (string, RepairRequest, []byte, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{Title: "repair"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "todo", "TASK-1.md")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	baseline := append(append([]byte(nil), original...), []byte("\n| **Status** | [ ] Pending |\n")...)
	mutated := bytes.Replace(baseline, []byte("| **Status** | [ ] Pending |"), []byte("| **Status** | [x] Done |"), 1)
	if bytes.Equal(mutated, baseline) {
		t.Fatal("repair fixture did not contain a status cell")
	}
	if err := os.WriteFile(path, mutated, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	req := RepairRequest{ID: "TASK-1", Owner: "tester", RequestID: strings.Repeat("a", 32), Path: "todo/TASK-1.md", ExpectedSHA256: bytesDigest(mutated)}
	return dir, req, mutated, baseline
}

func repairStopAt(phase string) func(string) error {
	return func(at string) error {
		if at == phase {
			return fmt.Errorf("stop at %s", phase)
		}
		return nil
	}
}

func assertRepairPendingGates(t *testing.T, dir string, req RepairRequest) {
	t.Helper()
	before := boardBytes(t, dir)
	claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: testToken}
	checks := map[string]func() error{
		"init":    func() error { return Init(dir) },
		"list":    func() error { _, err := List(dir); return err },
		"ready":   func() error { _, err := Ready(dir); return err },
		"create":  func() error { _, err := Create(dir, CreateRequest{Title: "blocked"}); return err },
		"claim":   func() error { _, err := Claim(dir, claim); return err },
		"release": func() error { _, err := Release(dir, claim); return err },
		"transition": func() error {
			_, err := Transition(dir, TransitionRequest{ID: req.ID, Owner: req.Owner, Token: testToken, RequestID: strings.Repeat("b", 32), From: "todo", To: "doing"})
			return err
		},
		"reserve": func() error { _, err := ReserveIDs(dir, []string{"TASK-9"}, false); return err },
	}
	for name, check := range checks {
		err := check()
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "pending") {
			t.Errorf("%s bypassed pending repair: %v", name, err)
		}
		if !reflectEqualBoard(before, boardBytes(t, dir)) {
			t.Errorf("%s modified board while repair pending", name)
		}
	}
}

func reflectEqualBoard(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func TestRepairRecoveryPhasesAndPendingGates(t *testing.T) {
	phases := []string{"after-empty-journal", "after-common-protocol", "after-local-protocol", "after-common-pending", "after-journal", "after-stage", "after-replacement", "after-receipt"}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			dir, req, _, want := statusRepairFixture(t)
			_, err := repairStatusWithStep(dir, req, true, false, repairStopAt(phase))
			if err == nil || !strings.Contains(err.Error(), "stop at "+phase) {
				t.Fatalf("phase %s not reached: %v", phase, err)
			}
			// A standalone board has no common reservation at after-common-pending.
			if phase == "after-journal" || phase == "after-stage" || phase == "after-replacement" {
				assertRepairPendingGates(t, dir, req)
			}
			result, err := RepairStatus(dir, req, true)
			if err != nil || result.Status != "completed" || result.Path != req.Path {
				t.Fatalf("resume=%+v err=%v", result, err)
			}
			got, err := os.ReadFile(filepath.Join(dir, req.Path))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("repaired bytes=%q err=%v", got, err)
			}
			if info, err := os.Stat(filepath.Join(dir, req.Path)); err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("repair lost original permissions: %v %v", info, err)
			}
			if _, err := List(dir); err != nil {
				t.Fatalf("board remained gated: %v", err)
			}
		})
	}
}

func TestRepairRecoveryConflictsPreserveBoard(t *testing.T) {
	for _, kind := range []string{"content", "mode", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			dir, req, _, _ := statusRepairFixture(t)
			stop := repairStopAt("after-journal")
			if _, err := repairStatusWithStep(dir, req, true, false, stop); err == nil {
				t.Fatal("repair did not stop")
			}
			path := filepath.Join(dir, req.Path)
			switch kind {
			case "content":
				if err := os.WriteFile(path, []byte("external edit"), 0o640); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(path)+".backup", path); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			if _, err := RecoverStatusRepair(dir, req); err == nil {
				t.Fatal("conflicting recovery accepted")
			}
			if !reflectEqualBoard(before, boardBytes(t, dir)) {
				t.Fatal("conflicting recovery modified board")
			}
		})
	}
}

func TestRepairRecoveryRequestConflictClaimAndNoop(t *testing.T) {
	dir, req, current, _ := statusRepairFixture(t)
	if _, err := repairStatusWithStep(dir, req, true, false, repairStopAt("after-journal")); err == nil {
		t.Fatal("repair did not stop")
	}
	conflict := req
	conflict.Owner = "other"
	if _, err := RecoverStatusRepair(dir, conflict); err == nil {
		t.Fatal("different request owner recovered")
	}
	if err := os.WriteFile(filepath.Join(dir, req.Path), current, 0o640); err != nil {
		t.Fatal(err)
	}

	noOpDir, noOpReq, _, noOpBaseline := statusRepairFixture(t)
	noOpRaw := bytes.Replace(noOpBaseline, []byte("\n| **Status** | [ ] Pending |\n"), nil, 1)
	if err := os.WriteFile(filepath.Join(noOpDir, noOpReq.Path), noOpRaw, 0o640); err != nil {
		t.Fatal(err)
	}
	noOpReq.ExpectedSHA256 = bytesDigest(noOpRaw)
	result, err := RepairStatus(noOpDir, noOpReq, true)
	if err != nil || result.Changed {
		t.Fatalf("missing-cell repair result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(noOpDir, noOpReq.Path)); err != nil || !bytes.Equal(got, noOpRaw) {
		t.Fatalf("no-op changed bytes: %v", err)
	}
}

func TestRepairRecoveryProcessKill(t *testing.T) {
	if os.Getenv("TASKCHAIN_REPAIR_HELPER") == "1" {
		dir := os.Getenv("TASKCHAIN_REPAIR_DIR")
		req := RepairRequest{ID: os.Getenv("TASKCHAIN_REPAIR_ID"), Owner: os.Getenv("TASKCHAIN_REPAIR_OWNER"), RequestID: os.Getenv("TASKCHAIN_REPAIR_REQUEST"), Path: os.Getenv("TASKCHAIN_REPAIR_PATH"), ExpectedSHA256: os.Getenv("TASKCHAIN_REPAIR_SHA256")}
		fd := os.NewFile(uintptr(3), "repair-ready")
		_, _ = repairStatusWithStep(dir, req, true, false, func(at string) error {
			if at == "after-journal" {
				if _, err := io.WriteString(fd, "ready\n"); err != nil {
					return err
				}
				time.Sleep(time.Minute)
				return fmt.Errorf("parent did not terminate repair helper")
			}
			return nil
		})
		return
	}
	dir, req, _, want := statusRepairFixture(t)
	readyR, readyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { readyR.Close(); readyW.Close() })
	cmd := exec.Command(os.Args[0], "-test.run=^TestRepairRecoveryProcessKill$", "-test.v")
	cmd.ExtraFiles = []*os.File{readyW}
	cmd.Env = append(os.Environ(), "TASKCHAIN_REPAIR_HELPER=1", "TASKCHAIN_REPAIR_DIR="+dir, "TASKCHAIN_REPAIR_ID="+req.ID, "TASKCHAIN_REPAIR_OWNER="+req.Owner, "TASKCHAIN_REPAIR_REQUEST="+req.RequestID, "TASKCHAIN_REPAIR_PATH="+req.Path, "TASKCHAIN_REPAIR_SHA256="+req.ExpectedSHA256)
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
	if err := readyR.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 6)
	if _, err := io.ReadFull(readyR, line); err != nil || string(line) != "ready\n" {
		t.Fatalf("helper readiness=%q err=%v", line, err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitErr := cmd.Wait()
	waited = true
	if waitErr == nil {
		t.Fatal("killed helper exited successfully")
	}
	raw, err := os.ReadFile(filepath.Join(dir, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeRepairJournal(raw)
	if err != nil || len(j.Records) != 1 || j.Records[0].Kind != "pending" {
		t.Fatalf("helper did not persist pending repair: %+v, %v", j, err)
	}
	// The helper is terminal. Remove only its empty fixture lock directory.
	lockPath := filepath.Join(dir, ".task-manager.lock")
	if info, err := os.Lstat(lockPath); err != nil || !info.IsDir() {
		t.Fatalf("unexpected fixture lock: %v, %v", info, err)
	}
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	result, err := RecoverStatusRepair(dir, req)
	if err != nil || result.Status != "completed" {
		t.Fatalf("post-kill recovery=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, req.Path)); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("post-kill repaired bytes=%q err=%v", got, err)
	}
}
