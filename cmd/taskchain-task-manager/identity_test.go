package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCLICardKindsAndAliasReservations(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	call := func(args ...string) map[string]any {
		t.Helper()
		var out, diag bytes.Buffer
		args = append(args, "--dir", dir, "--json")
		if code := run(args, &out, &diag); code != 0 {
			t.Fatalf("%v code=%d stderr=%s", args, code, &diag)
		}
		var result map[string]any
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	call("init")
	got := call("create", "--kind", "plan", "--title", "Synthetic")
	if got["path"] != "plan/PLAN-1.md" {
		t.Fatalf("kind path=%v", got)
	}
	got = call("create", "--id", "ISSUE-007", "--title", "Explicit")
	if got["path"] != "issue/ISSUE-007.md" {
		t.Fatalf("explicit path=%v", got)
	}
	got = call("reserve-ids", "--id", "ISSUE-0007", "--id", "TASK-000")
	if got["reservedCount"] != float64(3) || got["maxId"] != "TASK-0" {
		t.Fatalf("reservations=%v", got)
	}
	got = call("create", "--title", "Next")
	if got["path"] != "todo/TASK-1.md" {
		t.Fatalf("task floor=%v", got)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"create", "--kind", "task", "--id", "PLAN-2", "--title", "Wrong", "--dir", dir, "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("mismatch code=%d out=%s", code, &out)
	}
}
