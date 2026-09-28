package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"github.com/Gizzahub/taskchain-task-manager/internal/workspace"
)

func TestWorkspaceContextCLIProducesBoundedBatchWithoutWriting(t *testing.T) {
	root := t.TempDir()
	alpha := createContextCLIRepo(t, root, "alpha", map[string]string{"TASK-1": "shared", "TASK-2": "only alpha"})
	beta := createContextCLIRepo(t, root, "beta", map[string]string{"TASK-1": "shared"})
	privateSentinel := "private-frontmatter-sentinel-5f831d"
	bodySentinel := "body-only-sentinel-9ad71c"
	cardPath := filepath.Join(alpha, "tasks", "todo", "TASK-1.md")
	cardBytes, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	cardBytes = bytes.Replace(cardBytes, []byte("\n---\n"), []byte("\nprivate-context: "+privateSentinel+"\n---\n"), 1)
	cardBytes = append(cardBytes, []byte("\n"+bodySentinel+"\n")...)
	if err := os.WriteFile(cardPath, cardBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "workspace.json")
	raw := []byte(`{"schemaVersion":1,"repositories":[{"name":"beta","path":"repos/beta","board":"tasks"},{"name":"alpha","path":"repos/alpha","board":"tasks"}]}`)
	if err := os.WriteFile(manifest, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestBefore, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	args := []string{"workspace-context", "--manifest", manifest, "--card-id", "TASK-0001", "--card-id", "TASK-2", "--card-id", "TASK-3", "--json"}
	if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("workspace context: code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	var result workspace.SnapshotOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 3 {
		t.Fatalf("output = %#v", result)
	}
	if got := result.Results[0]; got.RequestedID != "TASK-0001" || got.CardID != "TASK-1" || got.Status != "ambiguous" || len(got.Matches) != 2 || got.Matches[0].Repository != "alpha" || got.Matches[1].Repository != "beta" {
		t.Fatalf("collision result = %#v", got)
	}
	if got := result.Results[1]; got.RequestedID != "TASK-2" || got.Status != "found" || len(got.Matches) != 1 || got.Matches[0].Repository != "alpha" {
		t.Fatalf("found result = %#v", got)
	}
	if got := result.Results[2]; got.RequestedID != "TASK-3" || got.Status != "missing" || len(got.Matches) != 0 {
		t.Fatalf("missing result = %#v", got)
	}
	if strings.Contains(out.String(), filepath.Dir(manifest)) {
		t.Fatalf("output leaked an absolute root: %s", out.String())
	}
	if strings.Contains(out.String(), privateSentinel) || strings.Contains(out.String(), bodySentinel) {
		t.Fatalf("output leaked unknown metadata or card body: %s", out.String())
	}
	if after, err := os.ReadFile(manifest); err != nil || !bytes.Equal(after, manifestBefore) {
		t.Fatalf("manifest changed: %v", err)
	}
	for _, board := range []string{filepath.Join(alpha, "tasks"), filepath.Join(beta, "tasks")} {
		if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
			t.Fatalf("workspace-context left lock in %s: %v", board, err)
		}
	}
}

func TestWorkspaceContextCLIEmitsNoPartialOutputOnBoardError(t *testing.T) {
	root := t.TempDir()
	valid := createContextCLIRepo(t, root, "valid", map[string]string{"TASK-1": "valid"})
	broken := createContextCLIRepo(t, root, "broken", nil)
	if err := os.WriteFile(filepath.Join(broken, "tasks", "todo", "broken.md"), []byte("---\nid: [\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "workspace.json")
	data := []byte(`{"schemaVersion":1,"repositories":[{"name":"valid","path":"repos/valid","board":"tasks"},{"name":"broken","path":"repos/broken","board":"tasks"}]}`)
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	args := []string{"workspace-context", "--manifest", manifest, "--card-id", "TASK-1", "--json"}
	if code := run(args, &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "broken") {
		t.Fatalf("board error: code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	for _, board := range []string{filepath.Join(valid, "tasks"), filepath.Join(broken, "tasks")} {
		if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
			t.Fatalf("failed workspace-context left lock in %s: %v", board, err)
		}
	}
}

func TestWorkspaceContextCLIUsageHasNoOutput(t *testing.T) {
	for _, args := range [][]string{
		{"workspace-context", "--json"},
		{"workspace-context", "--manifest", "missing.json", "--json"},
		{"workspace-context", "--manifest", "missing.json", "--card-id", "TASK-1"},
	} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("usage %v: code=%d stdout=%q stderr=%q", args, code, out.String(), diagnostics.String())
		}
	}
}

func createContextCLIRepo(t *testing.T, root, name string, cards map[string]string) string {
	t.Helper()
	repository := filepath.Join(root, "repos", name)
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", repository).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	board := filepath.Join(repository, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	for id, title := range cards {
		if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: id, Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	return repository
}
