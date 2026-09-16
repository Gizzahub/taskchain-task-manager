package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "card.md")
	if err := os.WriteFile(path, []byte("---\ntitle: Example\npriority: P1\n---\n# Example\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"show", "validate"} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{command, path, "--json"}, &out, &diagnostics); code != 0 {
			t.Fatalf("%s: %d %s", command, code, diagnostics.String())
		}
		if !json.Valid(out.Bytes()) || diagnostics.Len() != 0 {
			t.Fatalf("impure output: %q %q", out.String(), diagnostics.String())
		}
	}
	for _, args := range [][]string{{}, {"close", path, "--json"}, {"show", path}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("usage: %v %d", args, code)
		}
	}
}

func TestInputErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.md")
	for _, data := range [][]byte{nil, []byte("---\ntitle: [\n---\n")} {
		if data != nil {
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
		}
		var out, diagnostics bytes.Buffer
		if code := run([]string{"show", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("error handling: %d %q %q", code, out.String(), diagnostics.String())
		}
	}
}

func TestCardRelativePath(t *testing.T) {
	for input, want := range map[string]string{
		"/work/done/project/tasks/todo/card.md": "todo/card.md",
		"/work/done/project/card.md":            "card.md",
		"tasks/review/card.md":                  "review/card.md",
		"tasks/todo/../done/card.md":            "done/card.md",
	} {
		if got := cardRelativePath(input); got != want {
			t.Errorf("%s: %s != %s", input, got, want)
		}
	}
}
