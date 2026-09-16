package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestIterationContextCLIRoundTrip(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	rawIntent := `{"schemaVersion":2,"kind":"intent","id":"INTENT-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","revision":1,"title":"Keep healthy","outcome":"Verified health","mode":"maintenance","constraints":[],"nonGoals":[],"successCriteria":[{"key":"healthy","text":"Checks pass"}],"maintenance":{"triggers":[{"key":"manual","kind":"manual"}],"budget":{"maxIterations":3,"maxTasks":5,"maxElapsedSeconds":100,"noProgressLimit":2}}}`
	path := filepath.Join(t.TempDir(), "document.json")
	call := func(args ...string) []byte {
		t.Helper()
		var out, diag bytes.Buffer
		if code := run(args, &out, &diag); code != 0 || diag.Len() != 0 {
			t.Fatalf("%v exit=%d %s", args, code, &diag)
		}
		return out.Bytes()
	}
	if err := os.WriteFile(path, []byte(rawIntent), 0o600); err != nil {
		t.Fatal(err)
	}
	var registered taskstore.ContextResult
	if err := json.Unmarshal(call("register-context", path, "--dir", board, "--json"), &registered); err != nil {
		t.Fatal(err)
	}
	iteration := intentdoc.Iteration{
		SchemaVersion: 2, Kind: "iteration", ID: "ITERATION-" + strings.Repeat("b", 32), Revision: 1,
		Intent: intentdoc.IntentRef{ID: registered.ID, Revision: 1, Digest: registered.Digest}, Ordinal: 1,
		Trigger:    intentdoc.IterationTrigger{Key: "manual", EvidenceRefs: []string{"manual-observation"}},
		Usage:      intentdoc.IterationUsage{},
		Evaluation: intentdoc.IterationEvaluation{Actor: "observer", Criteria: []intentdoc.CriterionEvaluation{{Key: "healthy", Result: "met", EvidenceRefs: []string{"synthetic-check"}}}, Decision: "idle", StopReason: "none", Reason: "No task needed", RemainingGaps: []string{}, EvidenceRefs: []string{}},
	}
	raw, err := json.Marshal(iteration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var validation map[string]any
	if err := json.Unmarshal(call("validate-context", path, "--json"), &validation); err != nil {
		t.Fatal(err)
	}
	if validation["scope"] != "iteration-document" || validation["registered"] != false || validation["referenceValidation"] != "not_evaluated" {
		t.Fatalf("validation=%v", validation)
	}
	var first taskstore.ContextResult
	if err := json.Unmarshal(call("register-context", path, "--dir", board, "--json"), &first); err != nil {
		t.Fatal(err)
	}
	if first.ReferenceValidation != "verified" || first.Kind != "iteration" {
		t.Fatalf("first=%+v", first)
	}
	var shown taskstore.ContextResult
	if err := json.Unmarshal(call("show-context", "--dir", board, "--kind", "iteration", "--id", iteration.ID, "--revision", "1", "--json"), &shown); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Canonical, shown.Canonical) || shown.ReferenceValidation != "not_rechecked" {
		t.Fatal("show changed historical record")
	}
	var replay taskstore.ContextResult
	if err := json.Unmarshal(call("register-context", path, "--dir", board, "--json"), &replay); err != nil {
		t.Fatal(err)
	}
	if replay.Status != "unchanged" || replay.Digest != first.Digest {
		t.Fatal("replay changed record")
	}
	entries, err := taskstore.List(board)
	if err != nil || len(entries) != 0 {
		t.Fatalf("idle registration created tasks: %v %v", entries, err)
	}
}

func TestMaintenancePublishedExamples(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"maintenance-intent.json", "iteration-idle.json"} {
		path := filepath.Join("..", "..", "examples", "context", name)
		var out, diag bytes.Buffer
		if code := run([]string{"register-context", path, "--dir", board, "--json"}, &out, &diag); code != 0 {
			t.Fatalf("published example %s failed: exit=%d %s", name, code, &diag)
		}
		var result taskstore.ContextResult
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Status != "registered" || (name == "iteration-idle.json" && result.ReferenceValidation != "verified") {
			t.Fatalf("example registration=%+v", result)
		}
	}
	entries, err := taskstore.List(board)
	if err != nil || len(entries) != 0 {
		t.Fatalf("published idle example generated tasks: %v %v", entries, err)
	}
}
