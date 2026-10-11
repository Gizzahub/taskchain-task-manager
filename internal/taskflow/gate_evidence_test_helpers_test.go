package taskflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sampleChangeID = "taskchain-gate/0123456789abcdef0123456789abcdef"

func commitMsg(t *testing.T, dir, message string, env []string) {
	t.Helper()
	path := filepath.Join(dir, ".commit-msg")
	if err := os.WriteFile(path, []byte(message), 0o644); err != nil {
		t.Fatal(err)
	}
	gitContract(t, dir, env, "add", "-A")
	gitContract(t, dir, env, "commit", "-q", "-F", path, "--allow-empty")
}

func gitContract(t *testing.T, dir string, extra []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Tester",
		"GIT_AUTHOR_EMAIL=tester@example.com",
		"GIT_COMMITTER_NAME=Tester",
		"GIT_COMMITTER_EMAIL=tester@example.com",
	)
	cmd.Env = append(cmd.Env, extra...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}
