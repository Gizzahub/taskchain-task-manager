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

const registryIntent = validContextJSON

const registryBatch = `{"schemaVersion":1,"kind":"batch","id":"BATCH-abcdef0123456789abcdef0123456789","revision":1,"intent":{"id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"digest":"04edcffd5de255854f0bdd4f68cc24e21ee8ad1d3a39b4645382f0d9066ad508"},"gap":"Publish the reviewed release","taskIds":["TASK-1"],"constraints":[],"authorizationRefs":[]}`

func TestContextRegistryRegisterShowAndReplay(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "publish"}); err != nil {
		t.Fatal(err)
	}
	intentPath := filepath.Join(t.TempDir(), "intent.json")
	if err := os.WriteFile(intentPath, []byte(registryIntent), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"register-context", intentPath, "--dir", board, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("register intent code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	var result struct {
		Kind, ID, Status, Path, ReferenceValidation string
		Registered                                  bool `json:"registered"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "registered" || !result.Registered || result.ReferenceValidation != "not_applicable" {
		t.Fatalf("register result=%s err=%v", out.Bytes(), err)
	}
	if result.Kind != "intent" || result.ID == "" || result.Path == "" {
		t.Fatalf("unexpected result=%+v", result)
	}
	first := append([]byte(nil), out.Bytes()...)
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"register-context", intentPath, "--dir", board, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("replay code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	if !strings.Contains(out.String(), `"status":"unchanged"`) || bytes.Equal(first, out.Bytes()) {
		t.Fatalf("replay result=%s", out.Bytes())
	}
	out.Reset()
	if code := run([]string{"show-context", "--dir", board, "--kind", "intent", "--id", "INTENT-0123456789abcdef0123456789abcdef", "--revision", "1", "--json"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), `"status"`) {
		t.Fatalf("show code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}

	batchPath := filepath.Join(t.TempDir(), "batch.json")
	if err := os.WriteFile(batchPath, []byte(registryBatch), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"register-context", batchPath, "--dir", board, "--json"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), `"referenceValidation":"verified"`) {
		t.Fatalf("register batch code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
}

func TestContextRegistryConflictAndMissingReferenceAreReadOnly(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	intentPath := filepath.Join(t.TempDir(), "intent.json")
	if err := os.WriteFile(intentPath, []byte(registryIntent), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"register-context", intentPath, "--dir", board, "--json"}, &out, &diagnostics); code != 0 {
		t.Fatal(diagnostics.String())
	}
	conflict := strings.Replace(registryIntent, "Publish the release", "Changed content", 1)
	if err := os.WriteFile(intentPath, []byte(conflict), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"register-context", intentPath, "--dir", board, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("conflict code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
	missing := strings.Replace(registryBatch, `"taskIds":["TASK-1"]`, `"taskIds":["TASK-99"]`, 1)
	if err := os.WriteFile(intentPath, []byte(missing), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"register-context", intentPath, "--dir", board, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 {
		t.Fatalf("missing reference code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
}

func TestContextRegistryUsageRevisionAndOutputFailure(t *testing.T) {
	for _, args := range [][]string{{"register-context"}, {"register-context", "file", "--json"}, {"show-context", "--dir", "tasks", "--kind", "intent", "--id", "x", "--revision", "0", "--json"}, {"show-context", "--dir", "tasks", "--kind", "intent", "--id", "x", "--revision", "4294967296", "--json"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 {
			t.Fatalf("args=%v code=%d stdout=%s stderr=%s", args, code, &out, &diagnostics)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"register-context", "--help"}, &out, &diagnostics); code != 0 || out.Len() == 0 {
		t.Fatal("register help failed")
	}
	if code := run([]string{"show-context", "--help"}, &out, &diagnostics); code != 0 || out.Len() == 0 {
		t.Fatal("show help failed")
	}
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "intent.json")
	if err := os.WriteFile(path, []byte(registryIntent), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"register-context", path, "--dir", board, "--json"}, contextRegistryFailWriter{}, &diagnostics); code != 1 || !strings.Contains(diagnostics.String(), "may already be registered") {
		t.Fatalf("output failure code=%d diagnostics=%s", code, &diagnostics)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"register-context", path, "--dir", board, "--json"}, &out, &diagnostics); code != 0 || !strings.Contains(out.String(), `"status":"unchanged"`) {
		t.Fatalf("retry after output failure code=%d stdout=%s stderr=%s", code, &out, &diagnostics)
	}
}

type contextRegistryFailWriter struct{}

func (contextRegistryFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic output failure")
}
