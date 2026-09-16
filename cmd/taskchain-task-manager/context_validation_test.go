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

const validContextJSON = `{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":"Publish the release","outcome":"A reviewed release is available","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"published","text":"The release is published."}]}`

func TestContextValidationIsReadOnlyAndReportsCanonicalDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "intent.json")
	raw := []byte(validContextJSON)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-context", path, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	var result struct {
		SchemaVersion        int             `json:"schemaVersion"`
		Scope                string          `json:"scope"`
		Valid                bool            `json:"valid"`
		Kind                 string          `json:"kind"`
		ID                   string          `json:"id"`
		Revision             uint32          `json:"revision"`
		Canonical            json.RawMessage `json:"canonical"`
		Digest               string          `json:"digest"`
		Registered           bool            `json:"registered"`
		ReferenceValidation  string          `json:"referenceValidation"`
		EvaluationValidation string          `json:"evaluationValidation"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != 1 || result.Scope != "intent-batch-document" || !result.Valid || result.Kind != "intent" || result.ID == "" || result.Revision != 1 || result.Registered || result.ReferenceValidation != "not_evaluated" || result.EvaluationValidation != "not_evaluated" {
		t.Fatalf("result=%+v", result)
	}
	sum := sha256.Sum256(result.Canonical)
	if result.Digest != hex.EncodeToString(sum[:]) {
		t.Fatalf("digest=%s canonical=%s", result.Digest, result.Canonical)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, got) {
		t.Fatalf("input changed: %v", err)
	}
}

func TestContextValidationRejectsMalformedAndBoundedInput(t *testing.T) {
	dir := t.TempDir()
	bad := []string{
		strings.Replace(validContextJSON, `}`, `,"extra":1}`, 1),
		strings.Replace(validContextJSON, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		strings.Replace(validContextJSON, `"kind":"intent"`, `"kind":null`, 1),
		strings.Replace(validContextJSON, `"kind":"intent"`, `"Kind":"intent"`, 1),
		validContextJSON + " trailing",
	}
	for i, content := range bad {
		path := filepath.Join(dir, "bad-"+string(rune('a'+i))+".json")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		if code := run([]string{"validate-context", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("case %d code=%d stdout=%s stderr=%s", i, code, &out, &diagnostics)
		}
	}
	path := filepath.Join(dir, "large.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 256<<10+1), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-context", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("large code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
}

func TestContextValidationAcceptsBatchAndRejectsTaskAliases(t *testing.T) {
	dir := t.TempDir()
	batch := `{"schemaVersion":1,"kind":"batch","id":"BATCH-abcdef0123456789abcdef0123456789","revision":1,"intent":{"id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"digest":"04edcffd5de255854f0bdd4f68cc24e21ee8ad1d3a39b4645382f0d9066ad508"},"gap":"Publish the reviewed release","taskIds":["TASK-1"],"constraints":[],"authorizationRefs":[]}`
	path := filepath.Join(dir, "batch.json")
	if err := os.WriteFile(path, []byte(batch), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-context", path, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("batch code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	var result struct{ Kind, ID string }
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Kind != "batch" || result.ID == "" {
		t.Fatalf("batch result=%s err=%v", out.Bytes(), err)
	}
	alias := strings.Replace(batch, `"taskIds":["TASK-1"]`, `"taskIds":["TASK-1","TASK-001"]`, 1)
	if err := os.WriteFile(path, []byte(alias), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"validate-context", path, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("alias code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
}

func TestContextValidationRejectsSymlinkAndUsageErrors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte(validContextJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"validate-context", link, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("symlink code=%d stdout=%s", code, &out)
	}
	for _, args := range [][]string{{"validate-context"}, {"validate-context", "file"}, {"validate-context", "file", "--json", "extra"}, {"validate-context", "file", "--json=false"}} {
		out.Reset()
		diagnostics.Reset()
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%s", args, code, &out)
		}
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"validate-context", "--help"}, &out, &diagnostics); code != 0 || out.Len() == 0 {
		t.Fatal("help failed")
	}
}

func TestContextValidationOutputFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "intent.json")
	if err := os.WriteFile(path, []byte(validContextJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	if code := run([]string{"validate-context", path, "--json"}, contextFailWriter{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "write context result") {
		t.Fatalf("output failure code=%d diagnostics=%s", code, &diagnostics)
	}
}

type contextFailWriter struct{}

func (contextFailWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic output failure") }
