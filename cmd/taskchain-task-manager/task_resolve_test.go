package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskResolveDirect(t *testing.T) {
	root := t.TempDir()
	chdirResolve(t, root)
	writeTaskResolveIssue(t, root)
	var out bytes.Buffer
	err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "wontfix", "--by", "TASK-1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "not accepted with wontfix") {
		t.Fatalf("got %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("refusal wrote stdout")
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "invalid"}, &out); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "resolution: invalid\n") || !strings.Contains(text, "resolution-summary: Resolved as invalid.\n") || strings.Contains(text, "\"Resolved as invalid.\"") {
		t.Fatalf("stdout:\n%s", text)
	}
	if _, err := time.Parse(time.RFC3339, resolveStamp(t, text)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "tasks/issue/001-bug.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "resolution-summary: \"Resolved as invalid.\"\n") || !strings.Contains(string(body), "body\n") {
		t.Fatalf("file:\n%s", body)
	}
}

func TestTaskResolveEmptyByIsRejected(t *testing.T) {
	root := t.TempDir()
	chdirResolve(t, root)
	writeTaskResolveIssue(t, root)
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "invalid", "--by", ""}, &out); err == nil || !strings.Contains(err.Error(), "not accepted with invalid") {
		t.Fatalf("empty invalid --by: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "fixed", "--by", ""}, &out); err == nil || !strings.Contains(err.Error(), "--by needs a TASK id") {
		t.Fatalf("empty fixed --by: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "duplicate", "--by", ""}, &out); err == nil || !strings.Contains(err.Error(), "--by needs an ISSUE id") {
		t.Fatalf("empty duplicate --by: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("empty --by wrote stdout")
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(before) {
		t.Fatal("empty --by wrote the card")
	}
}

func TestTaskResolveUsageAndFlags(t *testing.T) {
	root := t.TempDir()
	chdirResolve(t, root)
	writeTaskResolveIssue(t, root)
	var out bytes.Buffer
	if err := taskResolve(context.Background(), []string{"--help"}, &out); err != nil || !strings.Contains(out.String(), "fixed [--by TASK-N] | duplicate [--by ISSUE-N] | wontfix | invalid") {
		t.Fatalf("help: %v\n%s", err, out.String())
	}
	out.Reset()
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md"}, &out); err == nil || !strings.Contains(err.Error(), "usage: taskchain-task-manager task resolve") {
		t.Fatalf("usage: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "fixed", "extra"}, &out); err == nil || !strings.Contains(err.Error(), "unexpected argument: extra (ce task resolve takes two arguments)") {
		t.Fatalf("extra: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"--by=TASK-1", "tasks/issue/001-bug.md", "fixed"}, &out); err == nil || !strings.Contains(err.Error(), "unknown flag: --by=TASK-1 (valid: --by, --help, -h)") {
		t.Fatalf("flag: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/001-bug.md", "fixed", "--by"}, &out); err == nil || !strings.Contains(err.Error(), "flag --by needs a value") {
		t.Fatalf("missing value: %v", err)
	}
	if err := taskResolve(context.Background(), []string{"tasks/issue/missing.md", "nope"}, &out); err == nil || !strings.Contains(err.Error(), "invalid resolution \"nope\"") {
		t.Fatalf("resolution: %v", err)
	}
	if out.Len() != 0 {
		t.Fatal("help was not reset and refusals wrote stdout")
	}
}

func chdirResolve(t *testing.T, root string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func writeTaskResolveIssue(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "tasks/issue"), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nid: ISSUE-1\ntitle: Bug\ntype: bug\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "tasks/issue/001-bug.md"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func resolveStamp(t *testing.T, text string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "   resolved-at: ") {
			return strings.TrimPrefix(line, "   resolved-at: ")
		}
	}
	t.Fatalf("no resolved-at in %q", text)
	return ""
}

func TestTaskResolveDispatch(t *testing.T) {
	root := t.TempDir()
	chdirResolve(t, root)
	writeTaskResolveIssue(t, root)
	var out, errOut bytes.Buffer
	if code := runTask([]string{"resolve", "tasks/issue/001-bug.md", "invalid"}, &out, &errOut); code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "resolution: invalid") {
		t.Fatalf("dispatch code/stdout/stderr: %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runTask([]string{"resolve", "tasks/issue/001-bug.md", "invalid", "--by", ""}, &out, &errOut); code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "not accepted with invalid") {
		t.Fatalf("refusal code/stdout/stderr: %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := runTask([]string{"--help"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "|resolve|") {
		t.Fatalf("help: %d %q", code, out.String())
	}
}
