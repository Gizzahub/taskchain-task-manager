package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeIDAndEvidenceCLI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	code, _, stderr := runTaskOut(t, "change-id", "--help")
	if code != 0 || stderr != "" {
		t.Fatalf("change-id help code=%d err=%q", code, stderr)
	}
	cmd := exec.Command("git", "init", "-q", "-b", "main")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	code, _, stderr = runTaskOut(t, "gate-evidence", "--log", filepath.Join(dir, "missing.log"))
	if code != 1 || !strings.Contains(stderr, "does not declare gate.name and gate.log") {
		t.Fatalf("evidence code=%d err=%q", code, stderr)
	}
}
