package taskruntime

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMalformedLedger(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		read func(stateStore) error
		want string
	}{
		{name: "executions object", file: "executions.json", body: "{}", read: func(s stateStore) error { _, err := s.load(); return err }, want: "decode task executions"},
		{name: "executions truncated", file: "executions.json", body: "{", read: func(s stateStore) error { _, err := s.load(); return err }, want: "decode task executions"},
		{name: "executions empty", file: "executions.json", body: "", read: func(s stateStore) error { _, err := s.load(); return err }, want: "decode task executions"},
		{name: "receipts empty line", file: "receipts.jsonl", body: "\n", read: func(s stateStore) error { _, err := s.latestReceipts(); return err }, want: "decode receipt line 1: empty receipt record"},
		{name: "receipts blank after record", file: "receipts.jsonl", body: "{\"task\":\"alpha\"}\n\n", read: func(s stateStore) error { _, err := s.latestReceipts(); return err }, want: "decode receipt line 2: empty receipt record"},
		{name: "receipts truncated", file: "receipts.jsonl", body: "{\"task\":", read: func(s stateStore) error { _, err := s.latestReceipts(); return err }, want: "decode receipt line 1:"},
		{name: "receipts not json", file: "receipts.jsonl", body: "not-json\n", read: func(s stateStore) error { _, err := s.latestReceipts(); return err }, want: "decode receipt line 1:"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			store := newStateStore(t.TempDir())
			if err := store.init(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.dir, test.file)
			if err := os.WriteFile(path, []byte(test.body), 0o644); err != nil {
				t.Fatal(err)
			}
			err := test.read(store)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want substring %q", err, test.want)
			}
			got, readErr := os.ReadFile(path)
			if readErr != nil || string(got) != test.body {
				t.Fatalf("read rewrote ledger: %q err=%v", got, readErr)
			}
		})
	}
}

func TestLoadPreservesBytes(t *testing.T) {
	store := newStateStore(t.TempDir())
	if err := store.init(); err != nil {
		t.Fatal(err)
	}
	body := []byte("[]\n")
	if err := os.WriteFile(store.executionsPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.load()
	if err != nil || len(loaded) != 0 {
		t.Fatalf("load %+v err=%v", loaded, err)
	}
	got, err := os.ReadFile(store.executionsPath())
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("bytes %q err=%v", got, err)
	}
	if _, err := os.Stat(store.receiptsPath()); !os.IsNotExist(err) {
		t.Fatalf("load created receipts: %v", err)
	}
}

func TestHeldLockIsNotDeleted(t *testing.T) {
	store := newStateStore(t.TempDir())
	if err := store.init(); err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf("pid=%d host=%s created=%s\n", os.Getpid(), lockHost(), time.Now().UTC().Format(time.RFC3339)))
	if err := os.WriteFile(store.lockPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.lock()
	if err == nil || unlock != nil || !strings.Contains(err.Error(), "task runtime mutation lock exists") {
		t.Fatalf("lock err=%v unlock=%v", err, unlock != nil)
	}
	got, readErr := os.ReadFile(store.lockPath())
	if readErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("live lock changed: %q err=%v", got, readErr)
	}
	if store.lockBlocks(time.Now()) != true {
		t.Fatal("live lock did not block readers")
	}
}

func TestDeadOwnerLockIsReclaimedByMutationOnly(t *testing.T) {
	orig := pidRunning
	t.Cleanup(func() { pidRunning = orig })
	pidRunning = func(int) bool { return false }
	store := newStateStore(t.TempDir())
	if err := store.init(); err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf("pid=42 host=%s created=%s\n", lockHost(), time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)))
	if err := os.WriteFile(store.lockPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if store.lockBlocks(time.Now()) {
		t.Fatal("dead-owner lock blocked a reader")
	}
	got, err := os.ReadFile(store.lockPath())
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("reader rewrote dead lock: %q err=%v", got, err)
	}
	unlock, err := store.lock()
	if err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(store.lockPath())
	if err != nil || !bytes.Contains(got, []byte(fmt.Sprintf("pid=%d ", os.Getpid()))) {
		t.Fatalf("reclaimed lock = %q err=%v", got, err)
	}
	unlock()
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("unlock left the lock: %v", err)
	}
}

func TestInterruptedReclaimLeavesBothFiles(t *testing.T) {
	orig := pidRunning
	t.Cleanup(func() { pidRunning = orig })
	pidRunning = func(int) bool { return false }
	store := newStateStore(t.TempDir())
	if err := store.init(); err != nil {
		t.Fatal(err)
	}
	body := []byte(fmt.Sprintf("pid=42 host=%s created=%s\n", lockHost(), time.Now().UTC().Format(time.RFC3339)))
	if err := os.WriteFile(store.lockPath(), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.guardPath(), []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock, err := store.lock()
	if err == nil || unlock != nil || !strings.Contains(err.Error(), "inspect and remove "+store.guardPath()) {
		t.Fatalf("err=%v", err)
	}
	got, readErr := os.ReadFile(store.lockPath())
	if readErr != nil || !bytes.Equal(got, body) {
		t.Fatalf("lock changed under a held guard: %q err=%v", got, readErr)
	}
	if _, err := os.Stat(store.guardPath()); err != nil {
		t.Fatalf("guard removed: %v", err)
	}
}

func TestRealDeadProcessIsNotRunning(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if !pidRunning(pid) {
		t.Fatal("live sleep was reported dead")
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if pidRunning(pid) {
		t.Fatal("dead sleep was reported running")
	}
}
