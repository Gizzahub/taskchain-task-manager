package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func policyRevisionFixture(t *testing.T) (string, string, taskstore.PolicyActivationResult, string) {
	t.Helper()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	initial := filepath.Join(t.TempDir(), "initial.yaml")
	initialPolicy := []byte("schema-version: 1\nboard-policy: {}\n")
	if err := os.WriteFile(initial, initialPolicy, 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"activate-policy", initial, "--dir", board, "--json"}, &out, &diag); code != 0 {
		t.Fatalf("activate exit=%d: %s", code, diag.String())
	}
	var activation taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &activation); err != nil {
		t.Fatal(err)
	}
	revised := filepath.Join(t.TempDir(), "revised.yaml")
	if err := os.WriteFile(revised, []byte("schema-version: 1\nboard-policy:\n  transitions:\n    - from: todo\n      to: [done]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return board, revised, activation, initial
}

func TestRevisePolicyCLI(t *testing.T) {
	board, revised, activation, _ := policyRevisionFixture(t)
	args := []string{"revise-policy", revised, "--dir", board, "--expected-authority", activation.AuthorityID, "--expected-digest", activation.Digest, "--json"}
	var out, diag bytes.Buffer
	if code := run(args, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("revision exit=%d out=%s diag=%s", code, &out, &diag)
	}
	var result taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Status != "completed" || result.Digest == activation.Digest || result.Replayed || result.Scope != "local" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := taskstore.List(board); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if code := run(args, &out, &diag); code != 0 {
		t.Fatalf("revision replay exit=%d: %s", code, diag.String())
	}
	var replay taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &replay); err != nil || !replay.Replayed || replay.AuthorityID != result.AuthorityID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}

	if code := run(args, bundleFailWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), "may already be completed") {
		t.Fatalf("output failure exit=%d diag=%s", code, diag.String())
	}
}

func TestRevisePolicyCLIRejectsSamePolicy(t *testing.T) {
	board, _, activation, initial := policyRevisionFixture(t)
	args := []string{"revise-policy", initial, "--dir", board, "--expected-authority", activation.AuthorityID, "--expected-digest", activation.Digest, "--json"}
	var out, diag bytes.Buffer
	if code := run(args, &out, &diag); code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "must change") {
		t.Fatalf("same policy exit=%d out=%s diag=%s", code, &out, &diag)
	}
}

func TestRevisePolicyCLIFirstPublicationOutputFailure(t *testing.T) {
	board, revised, activation, _ := policyRevisionFixture(t)
	args := []string{"revise-policy", revised, "--dir", board, "--expected-authority", activation.AuthorityID, "--expected-digest", activation.Digest, "--json"}
	var diag bytes.Buffer
	if code := run(args, bundleFailWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), "may already be completed") {
		t.Fatalf("output failure exit=%d diag=%s", code, &diag)
	}
	var out bytes.Buffer
	diag.Reset()
	if code := run(args, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("confirm after output failure exit=%d diag=%s", code, &diag)
	}
	var result taskstore.PolicyActivationResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Replayed {
		t.Fatalf("first output failure was not completed: %+v %v", result, err)
	}
}

func TestRevisePolicyCLIInput(t *testing.T) {
	for _, args := range [][]string{
		{"revise-policy"},
		{"revise-policy", "file", "--json"},
		{"revise-policy", "file", "--dir", "board", "--expected-authority", "a", "--expected-digest", "b", "--json", "extra"},
	} {
		var out, diag bytes.Buffer
		if code := run(args, &out, &diag); code != 2 || out.Len() != 0 || diag.Len() == 0 {
			t.Fatalf("usage %v exit=%d out=%s diag=%s", args, code, &out, &diag)
		}
	}
	var out, diag bytes.Buffer
	if code := run([]string{"revise-policy", "--help"}, &out, &diag); code != 0 || !strings.Contains(out.String(), "expected-authority") || diag.Len() != 0 {
		t.Fatalf("help exit=%d out=%s diag=%s", code, &out, &diag)
	}
}
