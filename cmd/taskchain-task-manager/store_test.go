package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestStoreCLI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	call := func(args ...string) []byte {
		t.Helper()
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
			t.Fatalf("%v: code %d, %s", args, code, diagnostics.String())
		}
		if !json.Valid(out.Bytes()) {
			t.Fatalf("invalid JSON: %q", out.String())
		}
		return out.Bytes()
	}
	call("init", "--dir", dir, "--json")
	if got := call("list", "--dir", dir, "--json"); string(got) != "[]\n" {
		t.Fatalf("empty: %s", got)
	}
	call("create", "--dir", dir, "--title", "Synthetic task", "--json")
	var entries []struct {
		Path string
		Card struct{ ID, Title, Status string }
	}
	if err := json.Unmarshal(call("list", "--dir", dir, "--json"), &entries); err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Card.ID != "TASK-1" || entries[0].Card.Status != "pending" {
		t.Fatalf("entries: %+v", entries)
	}
	call("show", filepath.Join(dir, filepath.FromSlash(entries[0].Path)), "--json")
	path := filepath.Join(dir, "todo", "TASK-1.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"create", "--dir", dir, "--title", "Overwrite", "--id", "TASK-1", "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("duplicate: %d %s", code, out.String())
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("duplicate changed existing task")
	}
}

func TestStoreUsageDoesNotCreate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	for _, args := range [][]string{
		{"init", "--dir", dir},
		{"create", "--dir", dir, "--json"},
		{"list", "--dir", dir, "--json", "unexpected"},
	} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("usage %v: %d", args, code)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path was created: %v", err)
	}
}

func TestReadyCLIAndRepeatedDependencies(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	call := func(args ...string) []byte {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
			t.Fatalf("%v: code=%d stderr=%s", args, code, diagnostics.String())
		}
		return out.Bytes()
	}
	call("init", "--dir", dir, "--json")
	call("create", "--dir", dir, "--id", "TASK-1", "--title", "Prereq", "--json")
	call("create", "--dir", dir, "--id", "TASK-2", "--title", "Dependent", "--depends-on", "TASK-1", "--json")
	created := call("create", "--dir", dir, "--id", "TASK-3", "--title", "Two prerequisites", "--depends-on", "TASK-1", "--depends-on", "TASK-2", "--json")
	var third taskstore.Entry
	if err := json.Unmarshal(created, &third); err != nil {
		t.Fatal(err)
	}
	if len(third.Card.DependsOn) != 2 || third.Card.DependsOn[0] != "TASK-1" || third.Card.DependsOn[1] != "TASK-2" {
		t.Fatalf("repeated dependencies lost: %#v", third.Card.DependsOn)
	}
	var ready []taskstore.Entry
	if err := json.Unmarshal(call("ready", "--dir", dir, "--json"), &ready); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Card.ID != "TASK-1" {
		t.Fatalf("ready before move: %#v", ready)
	}
	if err := os.Mkdir(filepath.Join(dir, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "done/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(call("ready", "--dir", dir, "--json"), &ready); err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("ready after move: %#v", ready)
	}
	empty := filepath.Join(t.TempDir(), "empty")
	call("init", "--dir", empty, "--json")
	if got := call("ready", "--dir", empty, "--json"); string(got) != "[]\n" {
		t.Fatalf("unexpected output: %s", got)
	}
}

func TestCreateMissingDependencyLeavesBoardUnchanged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	var out, diagnostics bytes.Buffer
	if code := run([]string{"init", "--dir", dir, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatal(diagnostics.String())
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"create", "--dir", dir, "--title", "bad", "--depends-on", "TASK-9", "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("missing dependency: code=%d out=%s", code, out.String())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("board changed: %v", entries)
	}
}

func TestReadyRejectsMalformedDependencyInsteadOfSelectingCard(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := os.MkdirAll(filepath.Join(dir, "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, deps := range []string{"123", "{task: TASK-2}", "[TASK-2, null]"} {
		raw := []byte("---\nid: TASK-1\ndepends-on: " + deps + "\n---\n")
		if err := os.WriteFile(filepath.Join(dir, "todo/TASK-1.md"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		if code := run([]string{"ready", "--dir", dir, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("malformed dependencies %s: code=%d out=%s stderr=%s", deps, code, out.String(), diagnostics.String())
		}
	}
}
