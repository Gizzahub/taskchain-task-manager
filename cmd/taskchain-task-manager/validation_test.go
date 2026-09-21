package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestConfiguredCardValidation(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "validation.yaml")
	path := filepath.Join(dir, "P4-example.md")
	policy := []byte("schema-version: 1\ncard-dialect:\n  id-required: false\n  criteria-heading: Acceptance Criteria\n  priority-values: [P4]\n  filename-prefixes: [P4]\n  task-types: [audit]\n")
	raw := []byte("---\ntitle: Example\ntype: audit\npriority: P4\ncustom: keep\n---\n## Summary\nExample.\n## Acceptance Criteria\n- [ ] inspect | verify: echo not-executed\n")
	for file, data := range map[string][]byte{config: policy, path: raw} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate", path, "--config", config, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("code=%d diagnostics=%s output=%s", code, &diagnostics, &out)
	}
	var result struct {
		Scope    string `json:"scope"`
		Board    string `json:"boardValidation"`
		Valid    bool   `json:"valid"`
		Criteria []struct {
			Text string `json:"text"`
		} `json:"criteria"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Valid || result.Scope != "card" || result.Board != "not_evaluated" || len(result.Criteria) != 1 || diagnostics.Len() != 0 {
		t.Fatalf("result=%+v err=%v diagnostics=%s", result, err, &diagnostics)
	}
	for file, want := range map[string][]byte{config: policy, path: raw} {
		got, err := os.ReadFile(file)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("input changed: %s (%v)", file, err)
		}
	}
	// Card validation does not silently give an anonymous card lifecycle identity.
	board := filepath.Join(dir, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(board, "todo", "P4-example.md"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Ready(board); err == nil {
		t.Fatal("anonymous lifecycle card accepted")
	}

	if err := os.WriteFile(path, bytes.Replace(raw, []byte("priority: P4"), []byte("priority: P3"), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"validate", path, "--config", config, "--json"}, &out, &diagnostics); code != 3 || !json.Valid(out.Bytes()) || !strings.Contains(out.String(), `"valid":false`) || diagnostics.Len() == 0 {
		t.Fatalf("invalid card: %d %s %s", code, &out, &diagnostics)
	}
}

type brokenValidationOutput struct{}

func (brokenValidationOutput) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}

func TestValidationReadLimitsAndOutputFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Example\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	if code := run([]string{"validate", path, "--json"}, brokenValidationOutput{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "synthetic output failure") {
		t.Fatalf("output failure=%d %s", code, &diagnostics)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{dir, link} {
		var out bytes.Buffer
		diagnostics.Reset()
		if code := run([]string{"validate", file, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
			t.Fatalf("nonregular=%s code=%d stdout=%s", file, code, &out)
		}
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	diagnostics.Reset()
	if code := run([]string{"validate", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || !strings.Contains(diagnostics.String(), "1048576") {
		t.Fatalf("oversize=%d %s %s", code, &out, &diagnostics)
	}
}

func TestExitCodeDistinguishesRuleViolationFromInputError(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "validation.yaml")
	path := filepath.Join(dir, "P4-example.md")
	policy := []byte("schema-version: 1\ncard-dialect:\n  id-required: false\n  criteria-heading: Acceptance Criteria\n  priority-values: [P4]\n  filename-prefixes: [P4]\n  task-types: [audit]\n")
	// priority P9 is not in priority-values, so the check runs and finds a
	// rule violation: the result document is still valid JSON.
	raw := []byte("---\ntitle: Example\ntype: audit\npriority: P9\n---\n## Summary\nExample.\n## Acceptance Criteria\n- [ ] inspect | verify: echo not-executed\n")
	for file, data := range map[string][]byte{config: policy, path: raw} {
		if err := os.WriteFile(file, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate", path, "--config", config, "--json"}, &out, &diagnostics); code != 3 {
		t.Fatalf("rule violation: code=%d diagnostics=%s output=%s", code, &diagnostics, &out)
	}
	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Valid {
		t.Fatalf("rule violation: expected parseable invalid result, got err=%v out=%s", err, &out)
	}

	// A missing input file is an input error, not a rule violation: no
	// result document is ever encoded to stdout.
	missing := filepath.Join(dir, "does-not-exist.md")
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"validate", missing, "--config", config, "--json"}, &out, &diagnostics); code != 1 {
		t.Fatalf("input error: code=%d diagnostics=%s output=%s", code, &diagnostics, &out)
	}
	if err := json.Unmarshal(out.Bytes(), &result); err == nil {
		t.Fatalf("input error: expected no parseable result document, got out=%s", &out)
	}
}

func TestValidationHelp(t *testing.T) {
	for _, option := range []string{"--help", "-h"} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{"validate", option}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), "--config") || diagnostics.Len() != 0 {
			t.Fatalf("help=%d %s %s", code, &out, &diagnostics)
		}
	}
}

func TestValidationConfigErrorsHaveNoSuccessJSON(t *testing.T) {
	dir := t.TempDir()
	config, path := filepath.Join(dir, "rules.yaml"), filepath.Join(dir, "card.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Example\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		"schema-version: 1\ncard-dialect:\n  zones: [manual]\n",
		"schema-version: 1\ncard-dialect:\n  priority-value: [P4]\n",
		strings.Repeat("x", (64<<10)+1),
	} {
		if err := os.WriteFile(config, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		if code := run([]string{"validate", path, "--config", config, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("config error: %d %s %s", code, &out, &diagnostics)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate", path, "--config=", "--json"}, &out, &diagnostics); code != 2 || out.Len() != 0 {
		t.Fatalf("empty config: %d %s", code, &out)
	}
}
