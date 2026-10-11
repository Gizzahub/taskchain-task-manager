package main

import (
	"strings"
	"testing"
)

func TestValidateSpecAdmissionBaseAndDialect(t *testing.T) {
	card := "tasks/todo/001-probe.md"
	cases := []struct {
		name       string
		setup      func(t *testing.T)
		args       []string
		code       int
		stdout     []string
		stdoutMiss []string
		stderr     string
	}{
		{
			name: "staged dialect prints the stamp",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, "ce-tasks.yaml", "gate: [\n")
				gitRun(t, nil, "add", "ce-tasks.yaml")
			},
			args:       []string{"validate", "--staged"},
			code:       2,
			stdout:     []string{"Validation tool:"},
			stdoutMiss: []string{"Staged task snapshot"},
			stderr:     "decode ce-tasks.yaml:",
		},
		{
			name: "named dialect prints the stamp",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("test -d ."))
				writeCardFile(t, "ce-tasks.yaml", "gate: [\n")
			},
			args:       []string{"validate", card},
			code:       2,
			stdout:     []string{"Validation tool:"},
			stdoutMiss: []string{"Criterion tree:"},
			stderr:     "decode ce-tasks.yaml:",
		},
		{
			name: "vacuous criterion",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("test -d ."))
			},
			args:   []string{"validate", card},
			code:   1,
			stdout: []string{"Criterion already passes on the current tree"},
			stderr: "validation failed with 1 error(s)",
		},
		{
			name: "negated probe is examined",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("! test -d ."))
			},
			args:       []string{"validate", card},
			code:       0,
			stdout:     []string{"1 eligible, 1 examined, 0 already passing"},
			stdoutMiss: []string{"Criterion already passes"},
		},
		{
			name: "quoted separator is not compound",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify(`test -f "a;b"`))
			},
			args:   []string{"validate", card},
			code:   0,
			stdout: []string{"1 eligible, 1 examined, 0 already passing, 0 compound unexecuted"},
		},
		{
			name: "unquoted metacharacter is compound",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("ls *.md"))
			},
			args:   []string{"validate", card},
			code:   0,
			stdout: []string{"shell syntax", "1 eligible, 0 examined, 0 already passing, 1 compound unexecuted"},
		},
		{
			name: "probe exit other than 0 or 1",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("grep -q needle missing-grep-target.txt"))
			},
			args:   []string{"validate", card},
			code:   0,
			stdout: []string{"1 eligible, 0 examined, 0 already passing, 0 compound unexecuted, 0 unsupported, 1 unavailable"},
		},
		{
			name: "base snapshot vacuous names the snapshot",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, "marker.txt", "marker\n")
				gitAddCommit(t, "marker")
				writeCardFile(t, card, cardWithVerify("test -f marker.txt"))
			},
			args:       []string{"validate", "--base", "HEAD", card},
			code:       1,
			stdout:     []string{"selected tree (commit "},
			stdoutMiss: []string{"current tree"},
			stderr:     "validation failed with 1 error(s)",
		},
		{
			name: "base absolute operand is unavailable",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("test -f /etc/passwd"))
			},
			args:       []string{"validate", "--base", "HEAD", card},
			code:       0,
			stdout:     []string{"operands are not contained by the selected tree", "1 eligible, 0 examined, 0 already passing, 0 compound unexecuted, 0 unsupported, 1 unavailable"},
			stdoutMiss: []string{"current tree"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			tc.setup(t)
			code, stdout, stderr := runTaskOut(t, tc.args...)
			if code != tc.code {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			for _, want := range tc.stdout {
				if !strings.Contains(stdout, want) {
					t.Fatalf("stdout missing %q:\n%s\nstderr:\n%s", want, stdout, stderr)
				}
			}
			for _, miss := range tc.stdoutMiss {
				if strings.Contains(stdout, miss) {
					t.Fatalf("stdout contains %q:\n%s", miss, stdout)
				}
			}
			if tc.stderr != "" && !strings.Contains(stderr, tc.stderr) {
				t.Fatalf("stderr=%s", stderr)
			}
		})
	}
}

func cardWithVerify(command string) string {
	return strings.Replace(probeCard("P2"), "test -f missing-marker.txt", command, 1)
}
