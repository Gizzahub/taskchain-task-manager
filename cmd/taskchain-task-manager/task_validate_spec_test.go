package main

import (
	"os"
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

func TestValidateAdmissionGatesExecution(t *testing.T) {
	card := "tasks/todo/001-probe.md"
	side := "probe-side-effect.txt"
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
			name: "refused find -fls is not executed",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("find . -fls "+side))
			},
			args:       []string{"validate", card},
			code:       0,
			stdout:     []string{"unsupported", "0 examined"},
			stdoutMiss: []string{"Criterion already passes"},
		},
		{
			name: "refused negated find -fprint is not executed",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("! find . -fprint "+side))
			},
			args:   []string{"validate", card},
			code:   0,
			stdout: []string{"unsupported", "0 examined"},
		},
		{
			name: "base does not treat a worktree-only file as vacuous",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, "marker.txt", "only in the work tree\n")
				writeCardFile(t, card, cardWithVerify("test -f marker.txt"))
			},
			args:       []string{"validate", "--base", "HEAD", card},
			code:       0,
			stdout:     []string{"tracked snapshot", "1 eligible, 1 examined, 0 already passing"},
			stdoutMiss: []string{"Criterion already passes", "current tree"},
		},
		{
			name: "empty base fails before a probe",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("find . -fls "+side))
			},
			args:       []string{"validate", "--base", "", card},
			code:       2,
			stdout:     []string{"Validation tool:"},
			stdoutMiss: []string{"Criterion tree:", "Criterion admission:"},
			stderr:     "criterion base unavailable",
		},
		{
			name: "blank base fails before a probe",
			setup: func(t *testing.T) {
				initGitRepo(t)
				writeCardFile(t, card, cardWithVerify("find . -fprint "+side))
			},
			args:       []string{"validate", "--base", "   ", card},
			code:       2,
			stdout:     []string{"Validation tool:"},
			stdoutMiss: []string{"Criterion tree:", "Criterion admission:"},
			stderr:     "criterion base unavailable",
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
			if _, err := os.Stat(side); !os.IsNotExist(err) {
				t.Fatalf("verify command created %s: %v", side, err)
			}
		})
	}
}

func TestValidateStagedDoesNotRunVerify(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	card := "tasks/todo/001-probe.md"
	side := "probe-side-effect.txt"
	writeCardFile(t, card, cardWithVerify("find . -fls "+side))
	gitRun(t, nil, "add", card)
	code, stdout, stderr := runTaskOut(t, "validate", "--staged")
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(side); !os.IsNotExist(err) {
		t.Fatalf("staged validate created %s: %v", side, err)
	}
}

func TestValidateBaseSymlinkRefusalIsAdmissionUnavailable(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	parent, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	outside := parent + ".outside"
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, "escape"); err != nil {
		t.Fatal(err)
	}
	gitRun(t, nil, "config", "core.symlinks", "true")
	gitRun(t, nil, "add", "escape")
	gitRun(t, nil, "commit", "-qm", "link")
	card := "tasks/todo/001-probe.md"
	side := "probe-side-effect.txt"
	writeCardFile(t, "marker.txt", "only in the work tree\n")
	writeCardFile(t, card, cardWithVerify("test -f marker.txt"))
	code, stdout, stderr := runTaskOut(t, "validate", "--base", "HEAD", card)
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"Criterion admission: unavailable", "absolute, empty, cyclic, or escaping symlink", "0 examined", "0 already passing"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Criterion already passes") || strings.Contains(stderr, "criterion base unavailable") {
		t.Fatalf("stdout=%s stderr=%s", stdout, stderr)
	}
	if _, statErr := os.Stat(side); !os.IsNotExist(statErr) {
		t.Fatalf("symlink refusal still probed: %v", statErr)
	}
}
