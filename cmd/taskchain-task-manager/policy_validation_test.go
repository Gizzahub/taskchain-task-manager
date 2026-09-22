package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyValidationIsExplicitAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "policy.yaml")
	raw := []byte("schema-version: 1\nboard-policy:\n  zones: [manual]\n  zone-status: {manual: done}\n  transitions:\n    - from: manual\n      to: [todo]\n")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-policy", file, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	var result struct {
		OutputVersion int             `json:"outputVersion"`
		Scope         string          `json:"scope"`
		Valid         bool            `json:"valid"`
		Activated     bool            `json:"activated"`
		Board         string          `json:"boardValidation"`
		Digest        string          `json:"digest"`
		Canonical     json.RawMessage `json:"canonical"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	// The contract is the encoded bytes, not the Go field: assert the stdout
	// document's own top-level keys spell the output-format version
	// "outputVersion" and never "schemaVersion", which names this repository's
	// on-disk journal axis. Only the top level is ours: the embedded canonical
	// document carries whatever version key its own author wrote.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["outputVersion"]; !ok {
		t.Fatalf("no top-level outputVersion key: %s", out.Bytes())
	}
	if _, ok := top["schemaVersion"]; ok {
		t.Fatalf("top-level schemaVersion key regained: %s", out.Bytes())
	}
	if result.OutputVersion != 1 || result.Scope != "policy-document" || !result.Valid || result.Activated || result.Board != "not_evaluated" {
		t.Fatalf("result=%+v", result)
	}
	sum := sha256.Sum256(result.Canonical)
	if result.Digest != hex.EncodeToString(sum[:]) {
		t.Fatal("digest does not bind canonical policy bytes")
	}
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("input changed: %v", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("validation created state: %v %v", files, err)
	}
}

func TestPolicyValidationFailuresDoNotPrintSuccessJSON(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "policy.yaml")
	for _, raw := range []string{"", "schema-version: 3\nboard-policy: {}\n", "schema-version: 1\nboard-policy: {command: 'touch should-not-exist'}\n", strings.Repeat(" ", 64<<10+1)} {
		if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		if code := run([]string{"validate-policy", file, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, dir, filepath.Join(dir, "missing")} {
		var out, diagnostics bytes.Buffer
		if code := run([]string{"validate-policy", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
			t.Fatalf("path=%s code=%d stdout=%s", path, code, &out)
		}
	}
}

func TestPolicyValidationUsageAndOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"validate-policy"}, {"validate-policy", "file"}, {"validate-policy", "file", "--json", "extra"}, {"validate-policy", "file", "--apply"}, {"validate-policy", "file", "--json=false"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-policy", "--help"}, &out, &diagnostics); code != 0 || out.Len() == 0 {
		t.Fatal("help failed")
	}
	file := filepath.Join(t.TempDir(), "policy.yaml")
	if err := os.WriteFile(file, []byte("schema-version: 1\nboard-policy: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"validate-policy", file, "--json"}, policyFailWriter{}, &diagnostics); code != 1 {
		t.Fatal("output failure ignored")
	}
}

type policyFailWriter struct{}

func (policyFailWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic output failure") }
