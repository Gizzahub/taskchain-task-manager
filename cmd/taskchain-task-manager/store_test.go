package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
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
