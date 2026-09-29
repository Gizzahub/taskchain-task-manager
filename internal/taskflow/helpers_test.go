package taskflow

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFileForTest creates parent directories as needed and writes content,
// for tests that assemble a board from scratch.
func writeFileForTest(root, rel string, content []byte) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, content, 0o644)
}

// gitInitForTest makes root a committed repository: the reservation ledger and
// the ref scan only run where git answers, and a ref scan over a repository
// with no commits aborts by design (the pinned scanner folds nothing).
func gitInitForTest(t *testing.T, root string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("add", "-A")
	run("commit", "-qm", "board", "--allow-empty")
}
