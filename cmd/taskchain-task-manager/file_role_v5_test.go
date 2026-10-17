package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV5PolicyValidationKeepsExistingEnvelopeAndExactBytes(t *testing.T) {
	fixture := filepath.Join("..", "..", "internal", "boardpolicy", "testdata", "file-roles-v5")
	raw, err := os.ReadFile(filepath.Join(fixture, "policy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join(fixture, "expected-canonical.json"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "policy.yaml")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"validate-policy", file, "--json"}, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diag)
	}
	var envelope struct {
		OutputVersion int             `json:"outputVersion"`
		Activated     bool            `json:"activated"`
		Validation    string          `json:"boardValidation"`
		Canonical     json.RawMessage `json:"canonical"`
	}
	if err := json.Unmarshal(out.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, envelope.Canonical); err != nil {
		t.Fatal(err)
	}
	if envelope.OutputVersion != 1 || envelope.Activated || envelope.Validation != "not_evaluated" || !bytes.Equal(compact.Bytes(), want) {
		t.Fatalf("envelope=%s", &out)
	}
	got, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("policy bytes changed: %v", err)
	}
}

func TestV5TrackedPolicyCannotBypassReadinessAuthority(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema-version":5,"board-policy":{"modules":[],"file-roles":{"enabled":true,"projections":[],"guidance":[],"templates":[]}}}`)
	if err := os.WriteFile(filepath.Join(root, "tasks", ".task-manager-policy.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, verb := range []string{"lint", "preflight", "gate"} {
		var out, diag bytes.Buffer
		code := runTask([]string{verb, "--json"}, &out, &diag)
		if code != 2 || diag.Len() == 0 {
			t.Fatalf("%s code=%d stdout=%s stderr=%s", verb, code, &out, &diag)
		}
		if strings.Contains(out.String(), `"status":"ready"`) {
			t.Fatalf("orphan policy became authority: %s", &out)
		}
		out.Reset()
		diag.Reset()
		if code := runTask([]string{verb, "--policy-file", "untrusted.yaml", "--json"}, &out, &diag); code != 2 || out.Len() != 0 {
			t.Fatalf("%s accepted policy override: code=%d stdout=%s", verb, code, &out)
		}
	}
}
