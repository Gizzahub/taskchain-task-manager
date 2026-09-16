package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnableSharedCLIAndImport(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "fixture", repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init %v %s", err, out)
	}
	board := filepath.Join(repo, "tasks")
	call := func(args ...string) []byte {
		t.Helper()
		var out, diag bytes.Buffer
		if code := run(args, &out, &diag); code != 0 {
			t.Fatalf("%v exit%d %s", args, code, &diag)
		}
		return out.Bytes()
	}
	call("init", "--dir", board, "--json")
	var out, diag bytes.Buffer
	if code := run([]string{"enable-shared", "--dir", board, "--json"}, &out, &diag); code != 2 || out.Len() != 0 {
		t.Fatalf("missing acknowledgement: %d %s", code, &out)
	}
	call("enable-shared", "--dir", board, "--all-worktrees", "--json")
	call("import-ids", "--repo", repo, "--dir", board, "--json")
	call("reserve-ids", "--dir", board, "--id", "TASK-90", "--json")
	raw := call("create", "--dir", board, "--title", "after shared import", "--json")
	if !bytes.Contains(raw, []byte(`"id":"TASK-91"`)) {
		t.Fatalf("create: %s", raw)
	}
	stored, err := os.ReadFile(filepath.Join(board, ".task-manager-ids.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ledger map[string]any
	if err := json.Unmarshal(stored, &ledger); err != nil || ledger["schemaVersion"] != float64(3) || ledger["namespace"] == nil {
		t.Fatalf("binding %s %v", stored, err)
	}
	call("enable-shared", "--dir", board, "--all-worktrees", "--resume", "--json")
}
