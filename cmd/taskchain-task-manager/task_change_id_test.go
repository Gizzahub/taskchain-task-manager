package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestTaskChangeIDArguments(t *testing.T) {
	var out bytes.Buffer
	if err := taskChangeID([]string{"--help"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "usage: taskchain-task-manager task change-id") {
		t.Fatalf("help=%q", out.String())
	}
	if err := taskChangeID([]string{"--bad"}, &out); err == nil || !strings.Contains(err.Error(), "unknown argument --bad") {
		t.Fatalf("err=%v", err)
	}
}

func TestTaskChangeIDDispatch(t *testing.T) {
	root := t.TempDir()
	chdirResolve(t, root)
	var out, errOut bytes.Buffer
	if code := runTask([]string{"change-id", "--unknown"}, &out, &errOut); code != 2 || out.Len() != 0 || !strings.Contains(errOut.String(), "unknown argument") {
		t.Fatalf("usage %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runTask([]string{"change-id"}, &out, &errOut); code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "source branch is not declared") {
		t.Fatalf("unresolved %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runTask([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "|change-id|") {
		t.Fatalf("help %d %q", code, out.String())
	}
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v %v %s", args, err, b)
		}
	}
	git("init", "-q", "-b", "source")
	git("config", "user.name", "Synthetic")
	git("config", "user.email", "synthetic@example.invalid")
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "base")
	git("checkout", "-qb", "task")
	const id = "taskchain-gate/0123456789abcdef0123456789abcdef"
	git("-c", "commit.gpgsign=false", "commit", "--allow-empty", "-qm", "change\n\nChange-Id: "+id+"\nModel: synthetic")
	if err := os.WriteFile(root+"/.gz-git.yaml", []byte("branch:\n  integrationBranch: source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if code := runTask([]string{"change-id"}, &out, &errOut); code != 0 || out.String() != id+"\n" || errOut.Len() != 0 {
		t.Fatalf("success %d %q %q", code, out.String(), errOut.String())
	}
}
