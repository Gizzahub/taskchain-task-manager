package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPickCommandRefusals(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		code      int
		stderr    string
		stdoutHas string
	}{
		{name: "board positional", args: []string{"board", "extra"}, code: 2, stderr: "usage: taskchain-task-manager task board"},
		{name: "board stale", args: []string{"board", "--stale-hours", "-1"}, code: 1, stderr: `invalid --stale-hours: "-1"`},
		{name: "board stale missing", args: []string{"board", "--stale-hours"}, code: 1, stderr: "flag --stale-hours needs a value"},
		{name: "board unknown", args: []string{"board", "--nope"}, code: 1, stderr: "unknown flag: --nope (valid: --json, --stale-hours, --help, -h)"},
		{name: "board help", args: []string{"board", "--help"}, code: 0, stdoutHas: "--stale-hours N"},
		{name: "recommend effort", args: []string{"recommend", "--max-effort", "huge"}, code: 1, stderr: "unknown --max-effort value: huge (valid: XS, S, M, L, XL)"},
		{name: "recommend limit", args: []string{"recommend", "--limit", "0"}, code: 1, stderr: "--limit needs a positive whole number, got: 0"},
		{name: "recommend equals", args: []string{"recommend", "--limit=2"}, code: 1, stderr: "unknown flag: --limit=2"},
		{name: "recommend help", args: []string{"recommend", "-h"}, code: 0, stdoutHas: "--max-effort E"},
		{name: "promote usage", args: []string{"promote"}, code: 1, stderr: "usage: ce task promote <issue-id|path>... | ce task promote issues"},
		{name: "promote mixed", args: []string{"promote", "issues", "ISSUE-1"}, code: 1, stderr: "`issues` selects every open issue and cannot be combined with named targets"},
		{name: "promote flag", args: []string{"promote", "--apply"}, code: 1, stderr: "unknown flag: --apply (valid: --dry-run, --json, --help, -h)"},
		{name: "promote help", args: []string{"promote", "--help"}, code: 0, stdoutHas: "--dry-run"},
		{name: "issue two", args: []string{"issue-promote", "ISSUE-1", "ISSUE-2"}, code: 1, stderr: "ce task issue-promote accepts exactly one issue; use ce task issue-promote-all for all open issues"},
		{name: "dropped sweep", args: []string{"issue-promote-all"}, code: 2, stderr: "unknown task command"},
		{name: "done review", args: []string{"done-review"}, code: 1, stderr: "ce task done-review was removed; use ce task review-run"},
		{name: "state flag", args: []string{"todo-run", "--apply"}, code: 1, stderr: "unknown flag: --apply (valid: --dry-run, --json, --help, -h)"},
		{name: "state help", args: []string{"blocked-recover", "--help"}, code: 0, stdoutHas: "Only blocked/ cards are eligible"},
		{name: "state empty json", args: []string{"review-run", "--json"}, code: 0, stdoutHas: "\"candidates\":[]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var out, diag bytes.Buffer
			code := runTask(test.args, &out, &diag)
			if code != test.code {
				t.Fatalf("code=%d want %d\nstderr=%s\nstdout=%s", code, test.code, diag.String(), out.String())
			}
			if test.stderr != "" && !strings.Contains(diag.String(), test.stderr) {
				t.Fatalf("stderr=%q", diag.String())
			}
			if test.stdoutHas != "" && !strings.Contains(out.String(), test.stdoutHas) {
				t.Fatalf("stdout=%q", out.String())
			}
			if test.code != 0 && out.Len() != 0 {
				t.Fatalf("refusal wrote stdout: %q", out.String())
			}
		})
	}
}

func TestPickCommandOutputs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/001-bug.md", "---\nid: TASK-1\ntitle: Bug one\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n")
	writeCardFile(t, "tasks/issue/002-leak.md", "---\nid: ISSUE-2\ntitle: Leak\ntype: bug\n---\n\n## Summary\n\nKeep.\n")
	writeCardFile(t, "tasks/done/003-finished.md", "---\nid: TASK-3\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nDone.\n")
	writeCardFile(t, "tasks/issue/005-open.md", "---\nid: ISSUE-5\ntitle: Still open\ntype: bug\n---\n\n## Summary\n\nKeep.\n")

	var out, diag bytes.Buffer
	if code := runTask([]string{"board", "--json"}, &out, &diag); code != 0 {
		t.Fatalf("board code=%d %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "\"zones\": [") || strings.Contains(out.String(), "\"outputVersion\"") {
		t.Fatalf("board json=%s", out.String())
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"recommend", "--json"}, &out, &diag); code != 0 {
		t.Fatalf("recommend code=%d %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "\"score\": 53") {
		t.Fatalf("recommend json=%s", out.String())
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"todo-run", "--json"}, &out, &diag); code != 0 {
		t.Fatalf("todo code=%d %s", code, diag.String())
	}
	for _, want := range []string{`"outputVersion":1`, `"candidates":[`, `"acceptanceCriteria":[]`, `"next_action":`} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %s in %s", want, out.String())
		}
	}
	if strings.Contains(out.String(), `"skill"`) {
		t.Fatalf("skill key was emitted: %s", out.String())
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"done-finalize"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "done-finalize: selection-required (zone=done)") {
		t.Fatalf("finalize select code=%d out=%s err=%s", code, out.String(), diag.String())
	}
	finished := mustRead(t, "tasks/done/003-finished.md")
	out.Reset()
	diag.Reset()
	if code := runTask([]string{"done-finalize", "--dry-run", "TASK-3"}, &out, &diag); code != 1 {
		t.Fatalf("finalize write code=%d %s %s", code, diag.String(), out.String())
	}
	if !strings.Contains(diag.String(), "done-finalize refuses to write: cleanup is not available, so a named card is not deleted") {
		t.Fatalf("stderr=%s", diag.String())
	}
	if mustRead(t, "tasks/done/003-finished.md") != finished {
		t.Fatal("done-finalize changed the card")
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"issue-promote", "--dry-run", "ISSUE-2"}, &out, &diag); code != 0 {
		t.Fatalf("issue dry-run code=%d %s", code, diag.String())
	}
	if strings.Contains(out.String(), "would be promoted") || strings.Contains(mustRead(t, "tasks/issue/002-leak.md"), "promoted-to:") {
		t.Fatal("dry-run issue-promote previewed or wrote a promotion")
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"promote", "--dry-run", "--json", "ISSUE-2"}, &out, &diag); code != 0 {
		t.Fatalf("promote dry-run code=%d %s", code, diag.String())
	}
	if !strings.Contains(out.String(), "\"dry_run\": true") || !strings.Contains(out.String(), "\"skipped\": false") {
		t.Fatalf("promote json=%s", out.String())
	}
	if _, err := os.Stat("tasks/todo/010-leak.md"); !os.IsNotExist(err) {
		t.Fatal("dry-run created a task")
	}

	out.Reset()
	diag.Reset()
	if code := runTask([]string{"issue-promote", "ISSUE-2"}, &out, &diag); code != 0 {
		t.Fatalf("issue apply code=%d %s %s", code, diag.String(), out.String())
	}
	if !strings.Contains(out.String(), "✅ ISSUE-2 → TASK-004") {
		t.Fatalf("apply out=%s", out.String())
	}
	if !strings.Contains(mustRead(t, "tasks/issue/002-leak.md"), "promoted-to: [TASK-004]") {
		t.Fatal("issue was not stamped")
	}

	cmd := exec.Command("git", "checkout", "--detach")
	if stderr, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("detach: %v %s", err, stderr)
	}
	out.Reset()
	diag.Reset()
	if code := runTask([]string{"issue-promote", "ISSUE-5"}, &out, &diag); code != 1 || !strings.Contains(diag.String(), "issue-promote refuses detached HEAD") {
		t.Fatalf("detach code=%d %s", code, diag.String())
	}
}

func TestPromoteOutsideAndMissingExitTwo(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/001-task.md", "---\nid: TASK-1\ntitle: Task\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n")
	cases := []struct {
		arg  string
		want string
	}{
		{arg: "TASK-1", want: "is not under issue/"},
		{arg: "nope", want: `no card in this corpus answers to "nope"`},
	}
	for _, test := range cases {
		var out, diag bytes.Buffer
		code := runTask([]string{"promote", test.arg}, &out, &diag)
		if code != 2 || !strings.Contains(diag.String(), test.want) {
			t.Fatalf("%s code=%d stderr=%s", test.arg, code, diag.String())
		}
	}
}
