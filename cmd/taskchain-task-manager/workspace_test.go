package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestWorkspaceQueryCLIRequiresExplicitScopeForCollision(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		board := filepath.Join(root, name, "tasks")
		if err := os.Mkdir(filepath.Dir(board), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := taskstore.Init(board); err != nil {
			t.Fatal(err)
		}
		if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: name}); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "workspace.json")
	data := []byte(`{"schemaVersion":1,"repositories":[{"name":"beta","path":"beta","board":"tasks"},{"name":"alpha","path":"alpha","board":"tasks"}]}`)
	if err := os.WriteFile(manifest, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	args := []string{"query-workspace", "--manifest", manifest, "--card-id", "TASK-0001", "--json"}
	if code := run(args, &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "alpha, beta") {
		t.Fatalf("collision: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	out.Reset()
	diagnostics.Reset()
	args = append(args, "--repository", "beta")
	if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("scoped: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	var result struct {
		Repository string `json:"repository"`
		Entry      struct {
			Path string `json:"path"`
			Card struct {
				ID string `json:"id"`
			} `json:"card"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Repository != "beta" || result.Entry.Path != "todo/TASK-1.md" || result.Entry.Card.ID != "TASK-1" {
		t.Fatalf("result: %+v", result)
	}
}

func TestWorkspaceQueryCLIMissingAndAmbiguousEmitNoJSON(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"alpha", "beta"} {
		board := filepath.Join(root, name, "tasks")
		if err := os.Mkdir(filepath.Dir(board), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := taskstore.Init(board); err != nil {
			t.Fatal(err)
		}
		if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: name}); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(root, "workspace.json")
	if err := os.WriteFile(manifest, []byte(`{"schemaVersion":1,"repositories":[{"name":"beta","path":"beta","board":"tasks"},{"name":"alpha","path":"alpha","board":"tasks"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cardID := range []string{"TASK-2", "TASK-1"} {
		var out, diagnostics bytes.Buffer
		code := run([]string{"query-workspace", "--manifest", manifest, "--card-id", cardID, "--json"}, &out, &diagnostics)
		if code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("card %s: code=%d stdout=%q stderr=%q", cardID, code, out.String(), diagnostics.String())
		}
	}
}

func TestWorkspaceQueryCLIUsageHasNoOutput(t *testing.T) {
	for _, args := range [][]string{
		{"query-workspace", "--json"},
		{"query-workspace", "--manifest", "missing.json", "--card-id", "TASK-1", "--kind", "intent", "--json"},
		{"query-workspace", "--manifest", "missing.json", "--kind", "intent", "--context-id", "ID", "--revision", "0", "--json"},
	} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("usage %v: code=%d out=%q stderr=%q", args, code, out.String(), diagnostics.String())
		}
	}
}
