package taskstore

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// This helper is deliberately a test entry point: it lets the parent test
// prove that the namespace lock is observed across processes, not only by
// two calls sharing one Go address space.
func TestBoardSessionSubprocessHelper(t *testing.T) {
	t.Parallel()
	if os.Getenv("TASKCHAIN_SESSION_HELPER") != "1" {
		return
	}
	dir := os.Getenv("TASKCHAIN_SESSION_DIR")
	if dir == "" {
		t.Fatal("missing helper board")
	}
	_, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "subprocess", Token: testToken})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("claim did not observe namespace lock: %v", err)
	}
}

func TestSharedNamespaceLockObservedBySubprocess(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	_, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBoardSessionSubprocessHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "TASKCHAIN_SESSION_HELPER=1", "TASKCHAIN_SESSION_DIR="+b)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subprocess lock check: %v\n%s", err, raw)
	}
}
