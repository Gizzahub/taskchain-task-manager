package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestConfiguredCreateCLI(t *testing.T) {
	dir := t.TempDir()
	board, config := filepath.Join(dir, "tasks"), filepath.Join(dir, "rules.yaml")
	policy := []byte("schema-version: 1\ncard-dialect:\n  id-required: false\n  criteria-heading: Acceptance Criteria\n  priority-values: [P4]\n  task-types: [audit]\n")
	if err := os.WriteFile(config, policy, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	args := []string{"create", "--dir", board, "--title", "Review sample", "--config", config, "--type", "audit", "--priority", "P4", "--summary", "Inspect synthetic inputs", "--criterion", "First requirement", "--criterion", "Second requirement", "--json"}
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("create=%d %s %s", code, &out, &diagnostics)
	}
	var entry taskstore.Entry
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Card.ID != "TASK-1" || entry.Card.Priority != "P4" {
		t.Fatalf("entry=%+v", entry)
	}
	path := filepath.Join(board, filepath.FromSlash(entry.Path))
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"validate", path, "--config", config, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatalf("validate=%d %s %s", code, &out, &diagnostics)
	}
	var report card.ValidationReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil || !report.Valid || len(report.Criteria) != 2 || report.Criteria[0].Checked || report.Criteria[1].Checked {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	got, err := os.ReadFile(config)
	if err != nil || !bytes.Equal(policy, got) {
		t.Fatal("configuration changed")
	}
}

func TestCreateProfileFlagsCannotBeSilentlyIgnored(t *testing.T) {
	dir := t.TempDir()
	board := filepath.Join(dir, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"--type", "bug"}, {"--criterion", "x"}, {"--config="}, {"--summary", ""}} {
		args := append([]string{"create", "--dir", board, "--title", "Rejected", "--json"}, extra...)
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("args=%v code=%d %s %s", args, code, &out, &diagnostics)
		}
	}
	config := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(config, []byte("schema-version: 1\ncard-dialect: {zones: [manual]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"create", "--dir", board, "--title", "Rejected", "--config", config, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("config code=%d %s %s", code, &out, &diagnostics)
	}
	entry, err := taskstore.Create(board, taskstore.CreateRequest{Title: "Default still works"})
	if err != nil || entry.Card.ID != "TASK-1" {
		t.Fatalf("default=%+v %v", entry, err)
	}
}
