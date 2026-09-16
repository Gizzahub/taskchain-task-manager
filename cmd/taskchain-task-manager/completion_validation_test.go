package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const completionRules = "schema-version: 1\ncard-dialect: {}\n"

func writeCompletionFixture(t *testing.T, cardRaw, rulesRaw []byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cardPath, rulesPath := filepath.Join(dir, "P1-card.md"), filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(cardPath, cardRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, rulesRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	return cardPath, rulesPath
}

func validCompletionCard(priority, criteria string) []byte {
	return []byte("---\nid: TASK-1\ntitle: Observe\ntype: feature\npriority: " + priority + "\n---\n## Summary\nObserve.\n## Completion Criteria\n" + criteria)
}

func TestValidateCompletionCLIObservesCriteriaAndMetadataSeparately(t *testing.T) {
	cases := []struct {
		name          string
		card          []byte
		code          int
		wantValid     bool
		wantComplete  bool
		wantCardValid bool
		wantFinding   string
	}{
		{"checked", validCompletionCard("P1", "- [x] done\n"), 0, true, true, true, ""},
		{"unchecked", validCompletionCard("P1", "- [ ] done\n"), 1, false, false, true, "completion"},
		{"unsupported marker mixed with valid", validCompletionCard("P1", "* [x] unsupported\n- [x] valid\n"), 1, false, false, true, "malformed"},
		{"metadata invalid independently", validCompletionCard("P9", "- [x] done\n"), 1, false, true, false, "priority"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cardPath, rulesPath := writeCompletionFixture(t, tc.card, []byte(completionRules))
			beforeCard, _ := os.ReadFile(cardPath)
			beforeRules, _ := os.ReadFile(rulesPath)
			var out, diagnostics bytes.Buffer
			code := run([]string{"validate-completion", cardPath, "--config", rulesPath, "--json"}, &out, &diagnostics)
			if code != tc.code {
				t.Fatalf("code=%d want=%d diagnostics=%s", code, tc.code, diagnostics.String())
			}
			var report struct {
				SchemaVersion      int    `json:"schemaVersion"`
				EvidenceValidation string `json:"evidenceValidation"`
				BoardValidation    string `json:"boardValidation"`
				Scope              string `json:"scope"`
				CardValid          bool   `json:"cardValid"`
				CriteriaComplete   bool   `json:"criteriaComplete"`
				Valid              bool   `json:"valid"`
				Findings           []struct {
					Message string `json:"message"`
				} `json:"findings"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("JSON=%q err=%v", out.String(), err)
			}
			if report.Scope != "card-completion-observation" || report.Valid != tc.wantValid || report.CriteriaComplete != tc.wantComplete || report.CardValid != tc.wantCardValid {
				t.Fatalf("report=%+v", report)
			}
			if report.SchemaVersion != 1 || report.EvidenceValidation != "not_evaluated" || report.BoardValidation != "not_evaluated" {
				t.Fatalf("unexpected validation scope: %+v", report)
			}
			if tc.wantFinding != "" {
				found := false
				for _, finding := range report.Findings {
					found = found || strings.Contains(strings.ToLower(finding.Message), strings.ToLower(tc.wantFinding))
				}
				if !found {
					t.Fatalf("finding %q absent: %+v", tc.wantFinding, report.Findings)
				}
			}
			afterCard, _ := os.ReadFile(cardPath)
			afterRules, _ := os.ReadFile(rulesPath)
			if !bytes.Equal(beforeCard, afterCard) || !bytes.Equal(beforeRules, afterRules) {
				t.Fatal("validation changed input bytes")
			}
		})
	}
}

func TestValidateCompletionCLIUsageMalformedAndOutputFailures(t *testing.T) {
	cardPath, rulesPath := writeCompletionFixture(t, validCompletionCard("P1", "- [x] done\n"), []byte(completionRules))
	for _, args := range [][]string{{"validate-completion", cardPath, "--json"}, {"validate-completion", cardPath, "--config=", "--json"}} {
		var out, diagnostics bytes.Buffer
		if code := run(args, &out, &diagnostics); code != 2 || out.Len() != 0 || diagnostics.Len() == 0 {
			t.Fatalf("usage args=%v code=%d out=%q diagnostics=%q", args, code, out.String(), diagnostics.String())
		}
	}
	var help, diagnostics bytes.Buffer
	if code := run([]string{"validate-completion", "--help"}, &help, &diagnostics); code != 0 || !strings.Contains(help.String(), "--config") || diagnostics.Len() != 0 {
		t.Fatalf("help code=%d out=%q diagnostics=%q", code, help.String(), diagnostics.String())
	}

	for _, malformed := range []struct {
		name  string
		card  []byte
		rules []byte
	}{
		{"malformed card", []byte("---\ntitle: [\n---\n"), []byte(completionRules)},
		{"malformed config", validCompletionCard("P1", "- [x] done\n"), []byte("schema-version: 1\ncard-dialect: [\n")},
	} {
		t.Run(malformed.name, func(t *testing.T) {
			path, config := writeCompletionFixture(t, malformed.card, malformed.rules)
			var out, errOut bytes.Buffer
			if code := run([]string{"validate-completion", path, "--config", config, "--json"}, &out, &errOut); code != 1 || out.Len() != 0 || errOut.Len() == 0 {
				t.Fatalf("code=%d out=%q err=%q", code, out.String(), errOut.String())
			}
		})
	}

	var diagnosticsOut bytes.Buffer
	if code := run([]string{"validate-completion", cardPath, "--config", rulesPath, "--json"}, brokenValidationOutput{}, &diagnosticsOut); code != 1 || !strings.Contains(diagnosticsOut.String(), "synthetic output failure") {
		t.Fatalf("output failure code=%d diagnostics=%q", code, diagnosticsOut.String())
	}
}
