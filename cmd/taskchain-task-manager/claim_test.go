package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failClaimOutput struct{}

func (failClaimOutput) Write(p []byte) (int, error) { return 0, errors.New("synthetic output failure") }

func TestClaimCLIReplayAndRelease(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	call := func(want int, args ...string) string {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != want {
			t.Fatalf("%v code=%d stderr=%s", args, code, diagnostics.String())
		}
		if want != 0 && out.Len() != 0 {
			t.Fatalf("error stdout=%s", out.String())
		}
		return out.String()
	}
	call(0, "init", "--dir", dir, "--json")
	call(0, "create", "--dir", dir, "--title", "synthetic", "--json")
	path := filepath.Join(dir, "todo", "TASK-1.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"claim", "--dir", dir, "--id", "TASK-1", "--owner", "tester", "--token", strings.Repeat("a", 32), "--json"}
	var diagnostics bytes.Buffer
	if code := run(args, failClaimOutput{}, &diagnostics); code != 1 {
		t.Fatalf("output failure code=%d", code)
	}
	result := call(0, args...)
	var record map[string]string
	if err := json.Unmarshal([]byte(result), &record); err != nil || record["status"] != "held" {
		t.Fatalf("record=%s err=%v", result, err)
	}
	if got := call(0, args...); got != result {
		t.Fatalf("replay differs: %s vs %s", got, result)
	}
	if got := call(0, "ready", "--dir", dir, "--json"); got != "[]\n" {
		t.Fatalf("held task ready: %s", got)
	}
	wrong := append([]string(nil), args...)
	wrong[0], wrong[6] = "release", "other"
	call(1, wrong...)
	args[0] = "release"
	diagnostics.Reset()
	if code := run(args, failClaimOutput{}, &diagnostics); code != 1 {
		t.Fatalf("release output failure code=%d", code)
	}
	released := call(0, args...)
	if got := call(0, args...); got != released {
		t.Fatalf("release replay differs: %s", got)
	}
	args[0] = "claim"
	call(1, args...)
	if got := call(0, "ready", "--dir", dir, "--json"); !strings.Contains(got, "TASK-1") {
		t.Fatalf("released task missing: %s", got)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("card bytes changed: %v", err)
	}
}

func TestClaimUsageDoesNotCreateBoard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	var out, diagnostics bytes.Buffer
	if code := run([]string{"claim", "--dir", dir, "--id", "TASK-1", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 {
		t.Fatalf("code=%d out=%s", code, out.String())
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("board created: %v", err)
	}
}
