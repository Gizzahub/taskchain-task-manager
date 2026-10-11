package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunCommandHelpAndRefusals(t *testing.T) {
	isolateRunEnv(t)
	helps := []struct {
		command string
		text    string
	}{
		{"run-doctor", runDoctorHelp},
		{"run-start", runStartHelp},
		{"run-status", runStatusHelp},
		{"run-list", runListHelp},
	}
	for _, help := range helps {
		for _, flag := range []string{"--help", "-h"} {
			stdout, stderr, code := invoke(help.command, flag)
			if code != 0 || stderr != "" || stdout != help.text {
				t.Fatalf("%s %s code=%d\nstderr=%q\nstdout=%q", help.command, flag, code, stderr, stdout)
			}
		}
	}
	refusals := []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"doctor positional", []string{"run-doctor", "extra"}, 1, "Error: unknown flag extra (valid: --json, --help, -h)\n"},
		{"doctor flag", []string{"run-doctor", "--bogus"}, 1, "Error: unknown flag --bogus (valid: --json, --help, -h)\n"},
		{"start usage", []string{"run-start"}, 1, "Error: usage: ce task run-start <task> --type <type>\n"},
		{"start no type", []string{"run-start", "sample-task"}, 1, "Error: usage: ce task run-start <task> --type <type>\n"},
		{"start no task", []string{"run-start", "--type", "feat"}, 1, "Error: usage: ce task run-start <task> --type <type>\n"},
		{"start two tasks", []string{"run-start", "one", "two", "--type", "feat"}, 1, "Error: run-start accepts one task\n"},
		{"start type value", []string{"run-start", "sample-task", "--type"}, 1, "Error: --type requires a value\n"},
		{"start flag", []string{"run-start", "sample-task", "--type", "feat", "--bogus"}, 1, "Error: unknown flag --bogus (valid: --type, --resume, --json, --help, -h)\n"},
		{"list positional", []string{"run-list", "sample-task"}, 1, "Error: unexpected argument sample-task\n"},
		{"status extra", []string{"run-status", "one", "two"}, 1, "Error: unexpected argument two\n"},
		{"status flag", []string{"run-status", "--bogus"}, 1, "Error: unknown flag --bogus (valid: --json, --help, -h)\n"},
		{"unknown command", []string{"run-missing"}, 2, "Error: unknown task command \"run-missing\" (valid: list, new, move, archive, validate, lint, preflight, gate, board, recommend, promote, issue-promote, plan-promote, backlog-promote, todo-run, doing-resume, review-run, blocked-recover, done-finalize, run-doctor, run-start, run-status, run-list)\n"},
	}
	for _, refusal := range refusals {
		t.Run(refusal.name, func(t *testing.T) {
			stdout, stderr, code := invoke(refusal.args...)
			if code != refusal.code || stderr != refusal.stderr || stdout != "" {
				t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
			}
		})
	}
	stdout, stderr, code := invoke("run-start", "sample-task", "--type", "-feat")
	if code != 1 || stderr != "Error: task and a valid --type are required\n" || !strings.Contains(stdout, "BLOCKED sample-task: task and a valid --type are required\n") || !strings.Contains(stdout, "next: use feat|fix|refactor|docs|test|chore|perf\n") {
		t.Fatalf("invalid type code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
}

func TestRunUnreadableDeclaration(t *testing.T) {
	isolateRunEnv(t)
	cases := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{name: "missing", mode: 0},
		{name: "unreadable", body: "schema-version: 2\n", mode: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)
			if tc.body != "" {
				mustWriteMode(t, filepath.Join(dir, ".ce", "task-runtime.yaml"), tc.body, 0o644)
				if err := os.Chmod(filepath.Join(dir, ".ce", "task-runtime.yaml"), tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, code := invoke("run-doctor")
			if code != 1 || !strings.Contains(stdout, "read .ce/task-runtime.yaml") || !strings.Contains(stderr, "read .ce/task-runtime.yaml") {
				t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
			}
		})
	}
}

func TestRunMalformedLedgerPreservesBytes(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	cases := []struct {
		name string
		file string
		body string
		args []string
		want string
	}{
		{name: "executions", file: "executions.json", body: "{", args: []string{"run-status"}, want: "decode task executions"},
		{name: "executions start", file: "executions.json", body: "{", args: []string{"run-start", "sample-task", "--type", "feat"}, want: "decode task executions"},
		{name: "empty receipt", file: "receipts.jsonl", body: "\n", args: []string{"run-doctor"}, want: "empty receipt record"},
		{name: "text receipt", file: "receipts.jsonl", body: "not-json\n", args: []string{"run-list"}, want: "decode receipt line"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(fx.ledger, 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(fx.ledger, tc.file)
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := invoke(tc.args...)
			if code != 1 || !strings.Contains(stdout, tc.want) || !strings.Contains(stderr, tc.want) {
				t.Fatalf("code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.body {
				t.Fatalf("ledger bytes changed from %q to %q", tc.body, got)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		})
	}
	worktree := filepath.Join(fx.wtRoot, "tester__testhost__feat__sample-task")
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("malformed start created %s", worktree)
	}
}

func TestRunHeldLockIsNotDeleted(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	body := "pid=" + strconv.Itoa(os.Getpid()) + " host=" + lockHostForTest(t) + " created=2026-01-01T00:00:00Z\n"
	if err := os.MkdirAll(fx.ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fx.ledger, "mutation.lock")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"run-doctor"}, {"run-start", "sample-task", "--type", "feat"}} {
		stdout, stderr, code := invoke(args...)
		if code != 1 || !strings.Contains(stdout, "mutation lock") || !strings.Contains(stderr, "mutation lock") {
			t.Fatalf("%v code=%d\nstderr=%q\nstdout=%q", args, code, stderr, stdout)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != body {
			t.Fatalf("%v rewrote the live lock", args)
		}
	}
}

func TestRunDeadLockIsReclaimedOnlyByStart(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	body := "pid=" + strconv.Itoa(deadPID(t)) + " host=" + lockHostForTest(t) + " created=2026-01-01T00:00:00Z\n"
	if err := os.MkdirAll(fx.ledger, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fx.ledger, "mutation.lock")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := invoke("run-doctor")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "names dead pid") {
		t.Fatalf("doctor code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatal("doctor rewrote a dead-owner lock")
	}
	stdout, stderr, code = invoke("run-start", "sample-task", "--type", "feat")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ACTIVE sample-task: task execution started\n") {
		t.Fatalf("start code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("start left the lock in place: %v", err)
	}
}

func TestRunDoctorStartStatusAndList(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	stdout, stderr, code := invoke("run-doctor")
	if code != 0 || stderr != "" || !strings.HasPrefix(stdout, "ACTIVE: task runtime dependencies are ready\n") {
		t.Fatalf("doctor code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	if _, err := os.Stat(fx.ledger); !os.IsNotExist(err) {
		t.Fatalf("doctor created the state directory: %v", err)
	}
	stdout, stderr, code = invoke("run-list")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "executions: none\n") {
		t.Fatalf("list code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	stdout, stderr, code = invoke("run-status", "sample-missing")
	if code != 1 || !strings.Contains(stdout, "UNKNOWN sample-missing: task execution was not found\n") || !strings.Contains(stderr, "Error: task execution was not found\n") {
		t.Fatalf("missing code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}

	stdout, stderr, code = invoke("run-doctor", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("doctor json code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	var doctor struct {
		Status   string `json:"status"`
		Warnings []string
		Provider *struct {
			Present      bool
			Capabilities map[string]bool
		}
		Diagnostics []struct{ Command string }
	}
	if err := json.Unmarshal([]byte(stdout), &doctor); err != nil {
		t.Fatal(err)
	}
	if doctor.Status != "ACTIVE" || doctor.Provider == nil || !doctor.Provider.Present || len(doctor.Diagnostics) != 8 || len(doctor.Warnings) != 0 {
		t.Fatalf("doctor json: %+v", doctor)
	}
	for _, capability := range []string{"integrate-check", "integrate-run", "integrate-no-fetch", "integrate-release"} {
		if !doctor.Provider.Capabilities[capability] {
			t.Fatalf("capability %s is false: %+v", capability, doctor.Provider.Capabilities)
		}
	}

	stdout, stderr, code = invoke("run-start", "sample-task", "--type", "feat", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"task execution started"`) {
		t.Fatalf("start code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	worktree := filepath.Join(fx.wtRoot, "tester__testhost__feat__sample-task")
	stdout, stderr, code = invoke("run-status", "sample-task")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ACTIVE sample-task: task branch has no upstream\n") {
		t.Fatalf("status code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	upstream := exec.Command("git", "branch", "--set-upstream-to=origin/master")
	upstream.Dir = worktree
	if out, err := upstream.CombinedOutput(); err != nil {
		t.Fatalf("set upstream: %v\n%s", err, out)
	}
	stdout, stderr, code = invoke("run-status", "sample-task", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("ready code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	var status struct {
		Status      string
		FinishReady bool
		Reason      string
	}
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "ACTIVE" || !status.FinishReady || status.Reason != "task execution is ready to finish" {
		t.Fatalf("status json: %+v", status)
	}
	stdout, stderr, code = invoke("run-list")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ACTIVE sample-task: branch=dev/tester/testhost/feat/sample-task worktree="+worktree+"\n") || !strings.Contains(stdout, "next: select a task\n") {
		t.Fatalf("list code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}

	mustWrite(t, filepath.Join(worktree, ".ce", "task-runtime.yaml"), mustRead(t, filepath.Join(fx.primary, ".ce", "task-runtime.yaml")))
	t.Chdir(worktree)
	stdout, stderr, code = invoke("run-status", "sample-task", "--json")
	if code != 0 || stderr != "" || !strings.Contains(stdout, `"finishReady": true`) {
		t.Fatalf("worktree status code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	t.Chdir(fx.primary)
	stdout, stderr, code = invoke("run-start", "sample-task", "--type", "feat")
	if code != 1 || !strings.Contains(stdout, "BLOCKED sample-task: task already has an unfinished execution") || !strings.Contains(stderr, "Error: task already has an unfinished execution") {
		t.Fatalf("resume required code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	stdout, stderr, code = invoke("run-start", "sample-task", "--type", "feat", "--resume")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "ACTIVE sample-task: existing execution returned\n") {
		t.Fatalf("resume code=%d\nstderr=%q\nstdout=%q", code, stderr, stdout)
	}
	records := mustRead(t, filepath.Join(fx.ledger, "executions.json"))
	if strings.Count(records, `"task": "sample-task"`) != 1 {
		t.Fatalf("resume duplicated the execution:\n%s", records)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
