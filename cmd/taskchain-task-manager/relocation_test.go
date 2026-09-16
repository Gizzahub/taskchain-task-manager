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

func TestRelocationCLIAdoptReplayAndOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(dir); err != nil {
		t.Fatal(err)
	}
	policy := []byte("schema-version: 2\nboard-policy:\n  relocations:\n    - from: todo\n      to: [plan]\n")
	if _, err := taskstore.ActivatePolicy(dir, policy, taskstore.PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(dir, taskstore.CreateRequest{Title: "relocate"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "todo/TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"relocate", "--dir", dir, "--id", "TASK-1", "--source", "todo/TASK-1.md", "--target", "plan/TASK-1.md", "--owner", "worker", "--request-id", strings.Repeat("a", 32), "--expected-sha256", repairDigest(raw), "--json"}
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("missing adoption code=%d out=%s err=%s", code, out.String(), diagnostics.String())
	}
	diagnostics.Reset()
	if code := run(append(append([]string{}, args...), "--adopt"), failClaimOutput{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "retry the identical request") {
		t.Fatalf("output failure code=%d err=%s", code, diagnostics.String())
	}
	stored, err := os.ReadFile(filepath.Join(dir, ".task-manager-relocations.json"))
	if err != nil {
		t.Fatal(err)
	}
	moved, err := os.ReadFile(filepath.Join(dir, "plan/TASK-1.md"))
	if err != nil || !bytes.Equal(moved, raw) {
		t.Fatalf("output failure lost move: %s %v", moved, err)
	}
	for _, option := range []string{"--adopt", "--resume"} {
		out.Reset()
		diagnostics.Reset()
		if code := run(append(append([]string{}, args...), option), &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
			t.Fatalf("%s code=%d out=%s err=%s", option, code, out.String(), diagnostics.String())
		}
		var result taskstore.RelocationResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "completed" || result.Target != "plan/TASK-1.md" || result.SchemaVersion != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		after, err := os.ReadFile(filepath.Join(dir, ".task-manager-relocations.json"))
		if err != nil || !bytes.Equal(stored, after) {
			t.Fatalf("replay changed receipt: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "todo/TASK-1.md")); !os.IsNotExist(err) {
		t.Fatalf("source exists: %v", err)
	}
}

func TestRelocationCLIUsageAndHelp(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := run([]string{"relocate", "--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "Usage: relocate") || diagnostics.Len() != 0 {
		t.Fatalf("help code=%d out=%s err=%s", code, out.String(), diagnostics.String())
	}
	out.Reset()
	if code := run([]string{"relocate", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("usage code=%d out=%s err=%s", code, out.String(), diagnostics.String())
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"--help"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), relocationUsage) {
		t.Fatalf("top-level help missing relocate: %d %s %s", code, out.String(), diagnostics.String())
	}
}
