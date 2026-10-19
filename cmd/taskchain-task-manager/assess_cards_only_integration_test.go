package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func assessCLIGit(t *testing.T, root string, args ...string) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = root
	c.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	if b, err := c.CombinedOutput(); err != nil {
		t.Fatalf("fixture git: %s %v", b, err)
	}
}

func TestAssessCardsOnlyEndToEnd(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("candidate safe file reader unavailable")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assessCLIGit(t, root, "init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "tasks/todo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks/todo/a.md"), []byte("candidate, not card-content validation"), 0600); err != nil {
		t.Fatal(err)
	}
	assessCLIGit(t, root, "add", "tasks")
	assessCLIGit(t, root, "commit", "-qm", "fixture")
	policy, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "prospective.json")
	if err := os.WriteFile(file, policy, 0600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"ref", "index", "worktree"} {
		args := []string{"assess-cards-only", "--repo", root, "--board", "tasks", "--candidate-policy", file, "--source", source, "--json"}
		if source == "ref" {
			args = append(args, "--ref", "HEAD")
		}
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
			t.Fatalf("%s code=%d stdout=%s stderr=%s", source, code, out.String(), diagnostics.String())
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if len(result) != 9 || string(result["outputVersion"]) != "1" || string(result["authorityChecked"]) != "false" || string(result["status"]) != `"clean"` || string(result["findings"]) != "[]" || string(result["error"]) != "null" || !strings.HasSuffix(out.String(), "\n") {
			t.Fatalf("wire: %s", out.String())
		}
		if strings.Contains(out.String(), root) || strings.Contains(out.String(), "candidate, not") {
			t.Fatalf("private input reflected: %s", out.String())
		}
	}
	args := []string{"assess-cards-only", "--repo", root, "--board", "tasks", "--candidate-policy", file, "--source", "worktree", "--json"}
	if err := os.WriteFile(filepath.Join(root, "tasks/todo/INDEX.md"), []byte("untracked residue"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 1 || diagnostics.Len() != 0 || !strings.Contains(out.String(), `"severity":"residue"`) || !strings.Contains(out.String(), `"status":"findings"`) {
		t.Fatalf("residue wire: code=%d %s %s", code, out.String(), diagnostics.String())
	}
	if err := os.WriteFile(file, []byte("private-secret-raw: [broken"), 0600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run(args, &out, &diagnostics); code != 2 || diagnostics.Len() != 0 || !strings.Contains(out.String(), `"code":"candidate-invalid"`) || strings.Contains(out.String(), "private-secret") || strings.Contains(out.String(), root) {
		t.Fatalf("invalid wire: code=%d %s %s", code, out.String(), diagnostics.String())
	}
}
