package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceContextCLI(t *testing.T) {
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	board := filepath.Join(root, "tasks", "todo")
	if err := os.MkdirAll(board, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(board, "one.md"), []byte("---\nid: TASK-1\ntitle: One\n---\nBODY_ONLY_SENTINEL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "manifest.json")
	raw := []byte(`{"schemaVersion":1,"repositories":[{"repositoryId":"product","root":"` + root + `","board":"tasks"}],"cardIds":["TASK-1"]}`)
	if err := os.WriteFile(manifest, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"workspace-context", manifest, "--json"}, &out, &diag); code != 0 || diag.Len() != 0 || !bytes.Contains(out.Bytes(), []byte(`"status":"found"`)) || bytes.Contains(out.Bytes(), []byte(root)) || bytes.Contains(out.Bytes(), []byte("BODY_ONLY_SENTINEL")) {
		t.Fatalf("code=%d diag=%s out=%s", code, diag.String(), out.String())
	}
}
