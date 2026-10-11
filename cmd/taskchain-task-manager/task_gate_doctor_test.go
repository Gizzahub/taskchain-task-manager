package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDoctorDeclarationAndIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, ".ce/task-runtime.yaml", "repository-id: example.repo\n")
	code, stdout, stderr := runTaskOut(t, "doctor")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "repository identity is unknown") {
		t.Fatalf("missing declaration code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runTaskOut(t, "doctor", "--json")
	if code != 2 || !strings.Contains(stdout, `"capabilityId": "ce.task.gate-doctor/v2"`) || !strings.Contains(stdout, `"repositoryId": "example.repo"`) || !strings.Contains(stderr, "repository identity is unknown") {
		t.Fatalf("json doctor code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	decl := "task-gate:\n  schema-version: 1\n  repository-id: example.repo\n  gates:\n    - event: pre-commit\n      id: probe\n      hook-path: .githooks/pre-commit\n      payload-version: 1\n      payload-digest: sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\n"
	writeCardFile(t, "ce-tasks.yaml", decl)
	code, stdout, stderr = runTaskOut(t, "doctor")
	if code != 1 || !strings.Contains(stdout, "probe: not-adopted (configure-project-hook)") || !strings.Contains(stderr, "task gate is not-adopted") {
		t.Fatalf("not adopted code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	writeCardFile(t, ".ce/task-runtime.yaml", "repository-id: other.repo\n")
	code, stdout, stderr = runTaskOut(t, "doctor")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "declared repository-id does not match runtime repository identity") {
		t.Fatalf("mismatch code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}

	writeCardFile(t, "ce-tasks.yaml", "task-gate: [\n")
	writeCardFile(t, ".ce/task-runtime.yaml", "repository-id: example.repo\n")
	code, stdout, stderr = runTaskOut(t, "doctor")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "decode task-gate:") {
		t.Fatalf("malformed declaration code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
}

func gitRun(t *testing.T, extra []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if extra != nil {
		cmd.Env = append(os.Environ(), extra...)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
