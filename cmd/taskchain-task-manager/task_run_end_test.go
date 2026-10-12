package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTerminationFlagRefusals(t *testing.T) {
	isolateRunEnv(t)
	cases := []struct {
		name   string
		args   []string
		stderr string
	}{
		{"finish two", []string{"run-finish", "one", "two"}, "Error: run-finish accepts at most one task\n"},
		{"finish flag", []string{"run-finish", "--bogus"}, "Error: unknown flag: --bogus (valid: --json, --help, -h)\n"},
		{"recover missing", []string{"run-recover"}, "Error: usage: ce task run-recover <task> [--claim-dead] [--json]\n"},
		{"recover two", []string{"run-recover", "one", "two"}, "Error: run-recover accepts one task\n"},
		{"recover flag", []string{"run-recover", "sample-task", "--bogus"}, "Error: unknown flag --bogus (valid: --claim-dead, --json, --help, -h)\n"},
		{"abort missing", []string{"run-abort"}, "Error: usage: ce task run-abort <task> [--reason R] [--json]\n"},
		{"abort two", []string{"run-abort", "one", "two"}, "Error: run-abort accepts one task\n"},
		{"abort reason", []string{"run-abort", "sample-task", "--reason"}, "Error: --reason requires a value\n"},
		{"abort flag", []string{"run-abort", "sample-task", "--bogus"}, "Error: unknown flag --bogus (valid: --reason, --json, --help, -h)\n"},
		{"settle missing", []string{"run-settle"}, "Error: usage: ce task run-settle <task> [--json]\n"},
		{"settle blank", []string{"run-settle", "   "}, "Error: run-settle accepts exactly one nonblank task\n"},
		{"settle two", []string{"run-settle", "one", "two"}, "Error: run-settle accepts exactly one nonblank task\n"},
		{"settle flag", []string{"run-settle", "--bogus"}, "Error: unknown flag --bogus (valid: --json, --help, -h)\n"},
		{"close missing", []string{"run-close"}, "Error: usage: ce task run-close <task> --reason <reason> [--json]\n"},
		{"close blank task", []string{"run-close", "  ", "--reason", "gone"}, "Error: run-close accepts exactly one nonblank task\n"},
		{"close blank reason", []string{"run-close", "sample-task", "--reason", "   "}, "Error: usage: ce task run-close <task> --reason <reason> [--json]\n"},
		{"close reason prefix", []string{"run-close", "sample-task", "--reason", "--gone"}, "Error: --reason requires exactly one nonblank value\n"},
		{"close duplicate reason", []string{"run-close", "sample-task", "--reason", "one", "--reason", "two"}, "Error: --reason requires exactly one nonblank value\n"},
		{"close flag", []string{"run-close", "sample-task", "--reason", "gone", "--bogus"}, "Error: unknown flag --bogus (valid: --reason, --json, --help, -h)\n"},
		{"discard missing", []string{"run-discard"}, "Error: usage: ce task run-discard <task> --reason R [--take-over-from actor/host] [--json]\n"},
		{"discard no reason", []string{"run-discard", "sample-task"}, "Error: usage: ce task run-discard <task> --reason R [--take-over-from actor/host] [--json]\n"},
		{"discard two", []string{"run-discard", "one", "two", "--reason", "gone"}, "Error: run-discard accepts one task\n"},
		{"discard reason value", []string{"run-discard", "sample-task", "--reason", "--gone"}, "Error: --reason requires a value\n"},
		{"discard takeover value", []string{"run-discard", "sample-task", "--reason", "gone", "--take-over-from"}, "Error: --take-over-from requires a value\n"},
		{"discard flag", []string{"run-discard", "sample-task", "--bogus"}, "Error: unknown flag --bogus (valid: --reason, --take-over-from, --json, --help, -h)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := invoke(tc.args...)
			if code != 1 || stderr != tc.stderr || stdout != "" {
				t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
			}
		})
	}
}

func TestRunAbortReasonConsumesNextToken(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	stdout, stderr, code := invoke("run-start", "sample-task", "--type", "feat")
	if code != 0 || stderr != "" {
		t.Fatalf("start code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	stdout, stderr, code = invoke("run-abort", "sample-task", "--reason", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ABORTED sample-task:") || !strings.Contains(stdout, "--json") || strings.Contains(stdout, "{") {
		t.Fatalf("abort code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	receipts := mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl"))
	if strings.Count(receipts, `"operation"`) != 2 {
		t.Fatalf("receipts:\n%s", receipts)
	}
	before := mustRead(t, filepath.Join(fx.ledger, "executions.json"))
	stdout, stderr, code = invoke("run-abort", "sample-task", "--reason", "again")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "already ABORTED") {
		t.Fatalf("second abort code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if got := mustRead(t, filepath.Join(fx.ledger, "executions.json")); got != before {
		t.Fatal("no-op abort rewrote executions.json")
	}
	if got := mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl")); got != receipts {
		t.Fatal("no-op abort appended a receipt")
	}
}

func TestRunAbortRefusesAnotherOwner(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	before := mustRead(t, filepath.Join(fx.ledger, "executions.json"))
	t.Setenv("CE_TASK_ACTOR", "other")
	stdout, stderr, code := invoke("run-abort", "sample-task")
	if code != 1 || !strings.Contains(stderr, "only tester/testhost may abort") || !strings.Contains(stdout, "only tester/testhost may abort") {
		t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if got := mustRead(t, filepath.Join(fx.ledger, "executions.json")); got != before {
		t.Fatal("refused abort rewrote executions.json")
	}
}

func TestRunDiscardRefusalsOnALiveWorktree(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	worktree := filepath.Join(fx.wtRoot, "tester__testhost__feat__sample-task")
	mustWrite(t, filepath.Join(worktree, "dirty.txt"), "local\n")
	stdout, stderr, code := invoke("run-discard", "sample-task", "--reason", "scratch")
	if code != 1 || !strings.Contains(stderr, "task worktree is dirty") || !strings.Contains(stdout, "task worktree is dirty") {
		t.Fatalf("dirty code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if err := os.Remove(filepath.Join(worktree, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_WORK_HOST", "otherhost")
	stdout, stderr, code = invoke("run-discard", "sample-task", "--reason", "scratch", "--take-over-from", "tester/testhost")
	if code != 1 || !strings.Contains(stderr, "on host testhost") {
		t.Fatalf("host code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
}

func TestRunSettleRefusesATipOutsideTheSource(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	worktree := filepath.Join(fx.wtRoot, "tester__testhost__feat__sample-task")
	mustWrite(t, filepath.Join(worktree, "note.txt"), "ahead\n")
	gitIn(t, worktree, "add", "note.txt")
	gitIn(t, worktree, "commit", "-qm", "move the tip")
	gitIn(t, fx.primary, "worktree", "remove", "--force", worktree)
	stdout, stderr, code := invoke("run-settle", "sample-task")
	if code != 1 || !strings.Contains(stderr, "recorded task tip is not contained in named source ref") {
		t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
}

func TestRunSettleAndCloseOnAMissingWorktree(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	worktree := filepath.Join(fx.wtRoot, "tester__testhost__feat__sample-task")
	gitIn(t, fx.primary, "worktree", "remove", "--force", worktree)
	stdout, stderr, code := invoke("run-close", "sample-task", "--reason", "abandoned")
	if code != 1 || !strings.Contains(stderr, "caller is not a repository-designated execution administrator") {
		t.Fatalf("close code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	stdout, stderr, code = invoke("run-settle", "sample-task")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ABORTED sample-task:") || !strings.Contains(stdout, "CE recorded settlement") {
		t.Fatalf("settle code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	before := mustRead(t, filepath.Join(fx.ledger, "executions.json"))
	receipts := mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl"))
	stdout, stderr, code = invoke("run-settle", "sample-task")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "already ABORTED") {
		t.Fatalf("second settle code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if mustRead(t, filepath.Join(fx.ledger, "executions.json")) != before || mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl")) != receipts {
		t.Fatal("no-op settle rewrote the ledger")
	}
}

func TestRunDiscardRefusesAPushedBranch(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, fx.primary, "init", "--bare", "-q", bare)
	gitIn(t, fx.primary, "remote", "set-url", "origin", bare)
	gitIn(t, fx.primary, "push", "-q", "origin", "dev/tester/testhost/feat/sample-task")
	stdout, stderr, code := invoke("run-discard", "sample-task", "--reason", "scratch")
	if code != 1 || !strings.Contains(stderr, "task branch still exists on origin") {
		t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
