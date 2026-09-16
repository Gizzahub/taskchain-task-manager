package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransitionCLIWorkflowReplayAndResume(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	call := func(want int, args ...string) string {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != want {
			t.Fatalf("%v: code=%d stderr=%s", args, code, diagnostics.String())
		}
		if want != 0 && out.Len() != 0 {
			t.Fatalf("error stdout=%s", out.String())
		}
		return out.String()
	}
	call(0, "init", "--dir", dir, "--json")
	call(0, "create", "--dir", dir, "--title", "workflow", "--json")
	token := strings.Repeat("a", 32)
	call(0, "claim", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json")
	args := func(request, from, to string) []string {
		return []string{"transition", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--request-id", request, "--from", from, "--to", to, "--json"}
	}
	start := args(strings.Repeat("1", 32), "todo", "doing")
	var diagnostics bytes.Buffer
	if code := run(start, failClaimOutput{}, &diagnostics); code != 1 {
		t.Fatalf("output failure code=%d", code)
	}
	first := call(0, start...)
	var receipt map[string]string
	if err := json.Unmarshal([]byte(first), &receipt); err != nil || receipt["path"] != "doing/TASK-1.md" || receipt["status"] != "completed" {
		t.Fatalf("receipt=%s err=%v", first, err)
	}
	call(0, args(strings.Repeat("2", 32), "doing", "review")...)
	call(0, args(strings.Repeat("3", 32), "review", "done")...)
	call(0, "release", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json")
	if got := call(0, start...); got != first {
		t.Fatalf("historical receipt changed: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "done", "TASK-1.md")); err != nil {
		t.Fatalf("historical replay moved card: %v", err)
	}
	call(1, args(strings.Repeat("1", 32), "done", "todo")...)
	token = strings.Repeat("b", 32)
	call(1, "claim", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json")
	call(0, "claim", "--resume", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json")
	reopened := args(strings.Repeat("4", 32), "done", "todo")
	call(0, reopened...)
	reopened[0] = "recover"
	call(0, reopened...)
	call(0, "release", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", token, "--json")
	if got := call(0, "ready", "--dir", dir, "--json"); !strings.Contains(got, "TASK-1") {
		t.Fatalf("reopened task missing: %s", got)
	}
}

func TestTransitionCLIRejectsUsageWithoutCreatingFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	for _, command := range []string{"transition", "recover"} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{command, "--dir", dir, "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("code=%d stdout=%s", code, out.String())
		}
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("usage created files: %v", err)
	}
}
