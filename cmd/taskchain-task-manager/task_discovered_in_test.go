package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestTaskNewDiscoveredIn(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"new", "issue", "--title", "Leak site"}, &out, &diag)
	if code != 1 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "discovered-in: discovered-in is required for issue cards; pass --discovered-in REF") {
		t.Fatalf("stdout did not require discovered-in: %s", out.String())
	}
	body, err := os.ReadFile("tasks/issue/001-leak-site.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "discovered-in: \"\"\n") || strings.Contains(string(body), "discovered-in: \"Leak site\"") {
		t.Fatalf("written issue:\n%s", body)
	}
	if strings.Contains(string(body), "<observable condition>") {
		t.Fatalf("issue gained a criterion placeholder:\n%s", body)
	}

	out.Reset()
	diag.Reset()
	code = runTask([]string{"new", "issue", "--title", "Known ref", "--discovered-in", "REF-9"}, &out, &diag)
	if code != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if strings.Contains(out.String(), "empty placeholder") {
		t.Fatalf("filled reference was diagnosed: %s", out.String())
	}
	body, err = os.ReadFile("tasks/issue/002-known-ref.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "discovered-in: \"REF-9\"\n") || strings.Contains(string(body), "discovered-in: \"Known ref\"") {
		t.Fatalf("written issue:\n%s", body)
	}

	out.Reset()
	diag.Reset()
	code = runTask([]string{"new", "task", "--title", "Not an issue", "--discovered-in", "REF-9"}, &out, &diag)
	if code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), "--discovered-in is only valid for an issue") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}

	out.Reset()
	diag.Reset()
	code = runTask([]string{"new", "task", "--title", "Empty ref", "--discovered-in", ""}, &out, &diag)
	if code != 2 || out.Len() != 0 || !strings.Contains(diag.String(), "--discovered-in is only valid for an issue") {
		t.Fatalf("empty flag code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
}
