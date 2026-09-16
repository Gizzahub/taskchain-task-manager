package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

func TestInspectWorktreesCLI(t *testing.T) {
	repo := t.TempDir()
	cmd := exec.Command("git", "init", "-b", "fixture", repo)
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture init: %v %s", err, raw)
	}
	if err := os.Mkdir(filepath.Join(repo, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	args := []string{"inspect-worktrees", "--repo", repo, "--json"}
	if code := run(args, &out, &diag); code != 0 {
		t.Fatalf("exit=%d diagnostic=%s", code, &diag)
	}
	var report githistory.WorktreeReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.SharedReadiness != "not_evaluated" || report.Board != "tasks" || len(report.Worktrees) != 1 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	entries, err := os.ReadDir(filepath.Join(repo, "tasks"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("inspection wrote board: %v %v", entries, err)
	}
	for _, args := range [][]string{{"inspect-worktrees", "--json"}, {"inspect-worktrees", "--repo", repo}, {"inspect-worktrees", "--repo", repo, "--json", "extra"}} {
		out.Reset()
		if code := run(args, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatalf("usage exit=%d stdout=%s", code, &out)
		}
	}
	out.Reset()
	if code := run([]string{"inspect-worktrees", "--repo", repo, "--board", "missing", "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("missing board exit=%d stdout=%s", code, &out)
	}
	if code := run([]string{"inspect-worktrees", "--help"}, &out, &diag); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
}
