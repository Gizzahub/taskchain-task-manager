package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const moduleAdoptionPolicy = `schema-version: 3
board-policy:
  modules: [backend]
`

func moduleAdoptionFixture(t *testing.T) (string, string) {
	t.Helper()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(filepath.Join(board, "backend", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	card := []byte("---\nid: TASK-1\ntitle: Existing\nstatus: pending\n---\n\n# Existing\n")
	if err := os.WriteFile(filepath.Join(board, "backend", "todo", "TASK-1.md"), card, 0o644); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(policy, []byte(moduleAdoptionPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	return board, policy
}

func TestPolicyActivationCLIRequiresExplicitModuleAdoption(t *testing.T) {
	board, policy := moduleAdoptionFixture(t)
	before := snapshotModuleAdoptionBoard(t, board)
	args := []string{"activate-policy", policy, "--dir", board, "--json"}
	var out, diag bytes.Buffer
	if code := runPolicyActivation(args, &out, &diag); code != 1 || out.Len() != 0 || !strings.Contains(strings.ToLower(diag.String()), "module") {
		t.Fatalf("refusal exit=%d out=%s diag=%s", code, &out, &diag)
	}
	if after := snapshotModuleAdoptionBoard(t, board); !bytes.Equal(before, after) {
		t.Fatal("module adoption refusal changed board")
	}
	out.Reset()
	diag.Reset()
	if code := runPolicyActivation(append(args, "--adopt-modules"), &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("adoption exit=%d out=%s diag=%s", code, &out, &diag)
	}
	var result taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if entries, err := taskstore.List(board); err != nil || len(entries) != 1 || entries[0].Path != "backend/todo/TASK-1.md" {
		t.Fatalf("adopted list=%v err=%v", entries, err)
	}
}

func TestPolicyRevisionCLIAdoptsModulesWithOriginalCAS(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "existing"}); err != nil {
		t.Fatal(err)
	}
	initial := filepath.Join(t.TempDir(), "initial.yaml")
	if err := os.WriteFile(initial, []byte("schema-version: 1\nboard-policy: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := runPolicyActivation([]string{"activate-policy", initial, "--dir", board, "--json"}, &out, &diag); code != 0 {
		t.Fatalf("initial activation exit=%d: %s", code, diag.String())
	}
	var active taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &active); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(board, "backend", "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(board, "backend", "todo", "TASK-2.md"), []byte("---\nid: TASK-2\ntitle: module\nstatus: pending\n---\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	revised := filepath.Join(t.TempDir(), "revised.yaml")
	if err := os.WriteFile(revised, []byte(moduleAdoptionPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"revise-policy", revised, "--dir", board, "--expected-authority", active.AuthorityID, "--expected-digest", active.Digest, "--adopt-modules", "--json"}
	out.Reset()
	diag.Reset()
	if code := runPolicyRevision(args, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("revision exit=%d out=%s diag=%s", code, &out, &diag)
	}
	var result taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "completed" {
		t.Fatalf("revision=%+v err=%v", result, err)
	}
	if entries, err := taskstore.List(board); err != nil || len(entries) != 2 {
		t.Fatalf("revised list=%v err=%v", entries, err)
	}
	out.Reset()
	if code := runPolicyRevision(args, bundleFailWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), "may already be completed") {
		t.Fatalf("output failure exit=%d diag=%s", code, diag.String())
	}
	if code := runPolicyRevision(args, &out, &diag); code != 0 {
		t.Fatalf("revision replay exit=%d: %s", code, diag.String())
	}
}

func TestPolicyRevisionCLIHelpMentionsModuleAdoption(t *testing.T) {
	var out, diag bytes.Buffer
	if code := runPolicyRevision([]string{"revise-policy", "--help"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "adopt-modules") || diag.Len() != 0 {
		t.Fatalf("help exit=%d out=%s diag=%s", code, &out, &diag)
	}
}

func snapshotModuleAdoptionBoard(t *testing.T, board string) []byte {
	t.Helper()
	var snapshot bytes.Buffer
	err := filepath.Walk(board, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(board, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(&snapshot, "%s\n%o\n%d\n", rel, info.Mode().Perm(), len(raw))
		snapshot.Write(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.Bytes()
}
