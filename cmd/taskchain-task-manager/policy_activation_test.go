package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestActivatePolicyCLISharedScope(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-b", "fixture")
	board := filepath.Join(repo, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	git("add", "tasks")
	git("-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	if _, err := taskstore.EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte("schema-version: 1\nboard-policy: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"activate-policy", path, "--dir", board, "--json"}
	var out, diag bytes.Buffer
	if code := run(args, &out, &diag); code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "all-worktrees") {
		t.Fatalf("missing shared scope exit=%d out=%s diag=%s", code, &out, &diag)
	}
	diag.Reset()
	if code := run(append(args, "--all-worktrees"), &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("shared activation exit=%d out=%s diag=%s", code, &out, &diag)
	}
	var result struct {
		taskstore.PolicyActivationResult
		OutputVersion int `json:"outputVersion"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Scope != "shared" || result.Status != "completed" || result.OutputVersion != outputformat.Version {
		t.Fatalf("shared result=%+v %v", result, err)
	}
}

func TestActivatePolicyCLI(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(path, []byte("schema-version: 1\nboard-policy: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"activate-policy", path, "--dir", board, "--json"}
	var out, diag bytes.Buffer
	if code := run(args, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("activate exit=%d out=%s diag=%s", code, &out, &diag)
	}
	var first struct {
		taskstore.PolicyActivationResult
		OutputVersion int `json:"outputVersion"`
	}
	if err := json.Unmarshal(out.Bytes(), &first); err != nil || first.OutputVersion != 1 || first.Status != "completed" || first.Replayed || first.Scope != "local" || first.Boards != 1 || len(first.AuthorityID) != 32 || len(first.Digest) != 64 {
		t.Fatalf("result=%+v %v", first, err)
	}
	out.Reset()
	if code := run(append(args, "--resume"), &out, &diag); code != 0 {
		t.Fatalf("completed resume exit=%d %s", code, &diag)
	}
	var replay taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &replay); err != nil || !replay.Replayed || replay.AuthorityID != first.AuthorityID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	diag.Reset()
	if code := run(args, bundleFailWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), "may already be completed") {
		t.Fatalf("output failure exit=%d %s", code, &diag)
	}
	if _, err := taskstore.List(board); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("schema-version: 1\nboard-policy:\n  zones: [manual]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diag.Reset()
	if code := run(args, &out, &diag); code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "different policy") {
		t.Fatalf("different policy exit=%d out=%s diag=%s", code, &out, &diag)
	}
}

func TestActivatePolicyCLIInput(t *testing.T) {
	for _, args := range [][]string{{"activate-policy"}, {"activate-policy", "file", "--json"}, {"activate-policy", "file", "--dir", "board"}, {"activate-policy", "file", "--dir", "board", "--json", "extra"}} {
		var out, diag bytes.Buffer
		if code := run(args, &out, &diag); code != 2 || out.Len() != 0 || diag.Len() == 0 {
			t.Fatalf("usage %v exit=%d out=%s diag=%s", args, code, &out, &diag)
		}
	}
	var out, diag bytes.Buffer
	if code := run([]string{"activate-policy", "--help"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "all-worktrees") || diag.Len() != 0 {
		t.Fatalf("help exit=%d out=%s diag=%s", code, &out, &diag)
	}
	path := filepath.Join(t.TempDir(), "policy")
	for _, raw := range []string{"{}", strings.Repeat("x", (64<<10)+1)} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		out.Reset()
		diag.Reset()
		if code := run([]string{"activate-policy", path, "--dir", "absent", "--json"}, &out, &diag); code != 1 || out.Len() != 0 || diag.Len() == 0 {
			t.Fatalf("bad input exit=%d out=%s diag=%s", code, &out, &diag)
		}
	}
}
