package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/candidatediagnostic"
)

func TestAssessCardsOnlyParsesOnlyExactGrammar(t *testing.T) {
	valid := []string{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "ref", "--ref", "HEAD", "--json"}
	input, ok := parseAssessCardsOnlyInput(valid)
	if !ok || input.Repo != "/repo" || input.Board != "tasks" || input.CandidatePolicy != "/candidate.yaml" || input.Source != "ref" || input.Ref != "HEAD" {
		t.Fatalf("valid input parsed as %#v, %v", input, ok)
	}

	for _, args := range [][]string{
		{"assess-cards-only"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree"},
		{"assess-cards-only", "--repo", "/repo", "--repo", "/again", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--ref", "HEAD", "--json"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "ref", "--json"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "staged", "--json"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json=false"},
		{"assess-cards-only", "--repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json", "extra"},
	} {
		if _, ok := parseAssessCardsOnlyInput(args); ok {
			t.Fatalf("accepted invalid arguments: %#v", args)
		}
	}
}

func TestAssessCardsOnlyHelpAndUsageDoNotReflectArguments(t *testing.T) {
	var out, diagnostics bytes.Buffer
	if code := run([]string{"assess-cards-only", "--help"}, &out, &diagnostics); code != 0 || out.String() != assessCardsOnlyUsage+"\n" || diagnostics.Len() != 0 {
		t.Fatalf("help code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}

	for _, args := range [][]string{
		{"assess-cards-only", "--help", "x"},
		{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json", "secret-positional"},
	} {
		out.Reset()
		diagnostics.Reset()
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 || strings.Contains(diagnostics.String(), "secret-positional") {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, out.String(), diagnostics.String())
		}
	}
	for _, writer := range []io.Writer{shortOnlyWriter{}, failedAssessWriter{}} {
		out.Reset()
		diagnostics.Reset()
		if code := run([]string{"assess-cards-only", "--help"}, writer, &diagnostics); code != 2 || diagnostics.String() != "assess-cards-only: write help failed\n" {
			t.Fatalf("help writer=%T code=%d stderr=%q", writer, code, diagnostics.String())
		}
	}
}

func TestAssessCardsOnlyRejectsDuplicateAndPrivateFlags(t *testing.T) {
	base := []string{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "ref", "--ref", "HEAD", "--json"}
	cases := [][]string{
		append(append([]string{}, base...), "--repo", "/second"),
		append(append([]string{}, base...), "--board", "other"),
		append(append([]string{}, base...), "--candidate-policy", "/second.yaml"),
		append(append([]string{}, base...), "--source", "ref"),
		append(append([]string{}, base...), "--ref", "other"),
		append(append([]string{}, base...), "--json"),
		append(append([]string{}, base...), "--private=/do-not-reflect"),
		append(append([]string{}, base...), "--help=/do-not-reflect"),
	}
	for _, args := range cases {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || strings.Contains(diagnostics.String(), "/do-not-reflect") {
			t.Fatalf("args=%q code=%d stdout=%q stderr=%q", args, code, out.String(), diagnostics.String())
		}
	}
}

func TestAssessCardsOnlyUnsafeBoardIsRejectedBeforeAssessment(t *testing.T) {
	args := []string{"assess-cards-only", "--repo", "/untrusted-root", "--board", ".GiT/private", "--candidate-policy", "/untrusted-policy", "--source", "worktree", "--json"}
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.String() != "assess-cards-only: invalid input\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "untrusted") || strings.Contains(diagnostics.String(), ".GiT") {
		t.Fatalf("unsafe values reflected: %q", diagnostics.String())
	}
}

func TestAssessCardsOnlyShortAndFailedWritesAreErrors(t *testing.T) {
	old := assessCardsOnly
	t.Cleanup(func() { assessCardsOnly = old })
	assessCardsOnly = func(context.Context, candidatediagnostic.Input) candidatediagnostic.Result {
		return cleanAssessResult()
	}
	for _, writer := range []io.Writer{shortOnlyWriter{}, failedAssessWriter{}} {
		var diagnostics bytes.Buffer
		if code := runAssessCardsOnly(validAssessArgs(), writer, &diagnostics); code != 2 || diagnostics.String() != "assess-cards-only: write result failed\n" {
			t.Fatalf("writer=%T code=%d stderr=%q", writer, code, diagnostics.String())
		}
	}
}

func TestAssessCardsOnlyContextSeamAndJSONEnvelope(t *testing.T) {
	old := assessCardsOnly
	t.Cleanup(func() { assessCardsOnly = old })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	assessCardsOnly = func(got context.Context, in candidatediagnostic.Input) candidatediagnostic.Result {
		called = true
		if got != ctx || in.Source != "worktree" {
			t.Fatalf("context/input not forwarded: %v %#v", got, in)
		}
		return cleanAssessResult()
	}
	var out, diagnostics bytes.Buffer
	if code := runAssessCardsOnlyWithContext(ctx, validAssessArgs(), &out, &diagnostics); code != 0 || !called || diagnostics.Len() != 0 {
		t.Fatalf("code=%d called=%v stdout=%q stderr=%q", code, called, out.String(), diagnostics.String())
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"outputVersion", "operation", "mode", "authorityChecked", "status", "candidate", "snapshot", "findings", "error"} {
		if _, ok := document[key]; !ok {
			t.Fatalf("missing JSON key %q: %s", key, out.String())
		}
	}
}

func TestAssessCardsOnlyStageResultWritesJSONWithoutDiagnostics(t *testing.T) {
	old := assessCardsOnly
	t.Cleanup(func() { assessCardsOnly = old })
	assessCardsOnly = func(context.Context, candidatediagnostic.Input) candidatediagnostic.Result {
		return candidatediagnostic.Result{
			Operation: "assess-cards-only", Mode: "candidate-diagnostic", Status: "unavailable", Findings: []candidatediagnostic.Finding{},
			Error: &candidatediagnostic.DiagnosticError{Code: "candidate-read", Detail: "candidate file could not be read as a stable bounded regular file"},
		}
	}
	var out, diagnostics bytes.Buffer
	if code := runAssessCardsOnly(validAssessArgs(), &out, &diagnostics); code != 2 || diagnostics.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	var document struct {
		Status string                               `json:"status"`
		Error  *candidatediagnostic.DiagnosticError `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &document); err != nil || document.Status != "unavailable" || document.Error == nil || document.Error.Code != "candidate-read" {
		t.Fatalf("document=%s err=%v", out.String(), err)
	}
}

func validAssessArgs() []string {
	return []string{"assess-cards-only", "--repo", "/repo", "--board", "tasks", "--candidate-policy", "/candidate.yaml", "--source", "worktree", "--json"}
}

func cleanAssessResult() candidatediagnostic.Result {
	return candidatediagnostic.Result{
		Operation: "assess-cards-only", Mode: "candidate-diagnostic", Status: "clean", Findings: []candidatediagnostic.Finding{},
	}
}

type shortOnlyWriter struct{}

func (shortOnlyWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type failedAssessWriter struct{}

func (failedAssessWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic writer failure")
}
