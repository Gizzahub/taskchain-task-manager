package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndEvidenceCLI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeCardFile(t, "tasks/issue/001-broken.md", "---\nid: ISSUE-1\ntitle: Broken widget\ntype: bug\nstatus: open\ndisposition: blocked\n---\n\n## Summary\n\nKeep this body.\n")
	writeCardFile(t, "tasks/todo/004-fix.md", "---\nid: TASK-4\ntitle: Fix\ntype: chore\n---\n\nbody\n")
	issue := filepath.Join(dir, "tasks/issue/001-broken.md")
	before, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runTaskOut(t, "resolve", "--help")
	if code != 0 || !strings.Contains(stdout, "usage: ce task resolve") || stderr != "" {
		t.Fatalf("help code=%d out=%q err=%q", code, stdout, stderr)
	}
	code, _, stderr = runTaskOut(t, "resolve", "missing.md", "nope")
	if code != 1 || !strings.Contains(stderr, `invalid resolution "nope"`) {
		t.Fatalf("bad resolution code=%d err=%q", code, stderr)
	}
	code, _, stderr = runTaskOut(t, "resolve", "tasks/issue/001-broken.md", "wontfix", "--by", "TASK-4")
	if code != 1 || !strings.Contains(stderr, "--by is not accepted with wontfix") {
		t.Fatalf("wontfix code=%d err=%q", code, stderr)
	}
	after, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("CLI rejection mutated the issue")
	}
	code, _, stderr = runTaskOut(t, "resolve", "tasks/issue/001-broken.md", "fixed", "--bogus")
	if code != 1 || !strings.Contains(stderr, "unknown flag: --bogus (valid: --by, --help, -h)") {
		t.Fatalf("flag code=%d err=%q", code, stderr)
	}
	code, stdout, stderr = runTaskOut(t, "resolve", "tasks/issue/001-broken.md", "fixed", "--by", "TASK-4")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "resolution: fixed") || !strings.Contains(stdout, "resolution-summary: Resolved as fixed by TASK-4.") {
		t.Fatalf("resolve code=%d out=%q err=%q", code, stdout, stderr)
	}
	text, err := os.ReadFile(issue)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(text), "Keep this body.") || !strings.Contains(string(text), "disposition: blocked") || strings.Contains(string(text), "title: Resolved:") {
		t.Fatalf("preserved:\n%s", text)
	}

	code, _, stderr = runTaskOut(t, "change-id", "--help")
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
