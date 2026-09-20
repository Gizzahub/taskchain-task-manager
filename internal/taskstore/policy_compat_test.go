package taskstore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// This opt-in test executes an actual pre-policy executable. Without the
// binary it is intentionally skipped; the in-process tests do not prove that
// an old writer observes the new policy barriers.
func TestPolicyLegacyBinaryBarrier(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_POLICY_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_POLICY_LEGACY_BINARY to a pre-policy executable")
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("legacy binary: %v", err)
	}

	// Establish that the executable and command surface work on an ordinary
	// board before testing policy-specific rejection.
	baseline := configuredFixture(t)
	if _, err := runLegacyPolicyCommand(binary, "list", "--dir", baseline, "--json"); err != nil {
		t.Fatalf("legacy baseline list: %v", err)
	}

	t.Run("local-active", func(t *testing.T) {
		dir := configuredFixture(t)
		if _, err := Create(dir, CreateRequest{Title: "policy"}); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
			t.Fatal("valid local activation:", err)
		}
		assertLegacyPolicyBarrier(t, binary, dir, "")
	})

	t.Run("shared-active-markerless", func(t *testing.T) {
		repo, owner, _ := sharedFixture(t)
		if _, err := EnableShared(owner, false); err != nil {
			t.Fatal(err)
		}
		if _, err := ActivatePolicy(owner, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err != nil {
			t.Fatal("valid shared activation:", err)
		}
		assertLegacyPolicyBarrier(t, binary, owner, owner)
		newRoot := filepath.Join(t.TempDir(), "markerless")
		sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
		target := filepath.Join(newRoot, "tasks")
		assertLegacyPolicyBarrier(t, binary, target, owner)
	})

	t.Run("shared-pending-before-local-receipt", func(t *testing.T) {
		_, owner, _ := sharedFixture(t)
		if _, err := EnableShared(owner, false); err != nil {
			t.Fatal(err)
		}
		stop := errCompatStop
		if _, err := activatePolicyWithStep(owner, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
			if at == "after-common-policy-pending" {
				return stop
			}
			return nil
		}); !errors.Is(err, stop) {
			t.Fatalf("valid shared pending baseline not reached: %v", err)
		}
		assertLegacyPolicyBarrier(t, binary, owner, owner)
	})

	t.Run("local-early-window-rejects-replanning", func(t *testing.T) {
		dir := configuredFixture(t)
		raw := defaultPolicyBytes(t)
		if _, err := activatePolicyWithStep(dir, raw, PolicyActivationOptions{}, func(at string) error {
			if at == "after-local-pending" {
				return errCompatStop
			}
			return nil
		}); !errors.Is(err, errCompatStop) {
			t.Fatalf("early local window not reached: %v", err)
		}
		// A pre-policy writer cannot know the new pending receipt. Upgrade all
		// writers before activation; recovery detects its edits, not erases them.
		for _, args := range [][]string{
			{"list", "--dir", dir, "--json"},
			{"create", "--dir", dir, "--title", "early legacy write", "--json"},
		} {
			if out, err := runLegacyPolicyCommand(binary, args...); err != nil {
				t.Fatalf("documented early window changed: %v %s", err, out)
			}
		}
		before := boardBytes(t, dir)
		if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{Resume: true}); err == nil || !strings.Contains(err.Error(), "snapshot") {
			t.Fatalf("legacy edit silently replanned: %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatal("failed resume overwrote old writer data")
		}
	})
}

var errCompatStop = &compatStopError{}

type compatStopError struct{}

func (*compatStopError) Error() string { return "compatibility test interruption" }

func runLegacyPolicyCommand(binary string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	if err != nil {
		return out, err
	}
	return out, nil
}

func assertLegacyPolicyBarrier(t *testing.T, binary, target, commonBoard string) {
	t.Helper()
	before := boardBytes(t, target)
	var commonBefore []byte
	if commonBoard != "" {
		commonBefore = policyCommonBytes(t, commonBoard)
	}
	for _, args := range [][]string{
		{"list", "--dir", target, "--json"},
		{"ready", "--dir", target, "--json"},
		{"create", "--dir", target, "--title", "legacy writer", "--json"},
		{"reserve-ids", "--dir", target, "--id", "TASK-99", "--json"},
	} {
		output, err := runLegacyPolicyCommand(binary, args...)
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			t.Fatalf("legacy command unexpectedly succeeded: %v (%s)", args, output)
		}
		if !strings.Contains(string(output), "journal") && !strings.Contains(string(output), "shared state") && !strings.Contains(string(output), "policy") && !strings.Contains(string(output), "activation") {
			t.Fatalf("legacy command returned unrelated error: %v %s", args, output)
		}
		if !reflect.DeepEqual(before, boardBytes(t, target)) {
			t.Fatalf("legacy operation modified board: %v", args)
		}
		if commonBoard != "" && !reflect.DeepEqual(commonBefore, policyCommonBytes(t, commonBoard)) {
			t.Fatalf("legacy operation modified common state: %v", args)
		}
	}
}
