package card

import (
	"bytes"
	"strings"
	"testing"
)

const completionCard = "---\nid: TASK-1\ntitle: Observe\ntype: test\npriority: P1\n---\n## Summary\nSynthetic example\n## Completion Criteria\n"

func TestCompletionObservation(t *testing.T) {
	rules := defaultValidationRules()
	for _, tc := range []struct {
		name, body          string
		cardValid, complete bool
	}{
		{"checked", "- [x] one\n- [X] two\n", true, true},
		{"unchecked", "- [x] one\n- [ ] two\n", true, false},
		{"deferred", "- [>] later\n", true, false},
		{"empty", "No checkboxes\n", false, false},
		{"malformed", "- [x] one\n- [?] bad\n", false, false},
		{"star-hidden", "- [x] one\n* [ ] two\n", true, false},
		{"plus-hidden", "- [x] one\n+ [ ] two\n", true, false},
		{"ordered-hidden", "- [x] one\n1. [ ] two\n", true, false},
		{"bare-hidden", "- [x] one\n[ ] two\n", true, false},
		{"fenced", "- [x] one\n```md\n* [ ] ignored\n```\n", true, true},
		{"nested", "- [x] one\n    * [ ] child\n", true, false},
		{"loose-nested", "- [x] one\n\n    - [ ] child\n", true, false},
		{"group-nested", "- [x] one\n- group\n    - [ ] child\n", true, false},
		{"number-group-nested", "- [x] one\n1. group\n    - [ ] child\n", true, false},
		{"tab-nested", "- [x] one\n\t- [ ] child\n", true, false},
		{"lazy-nested", "- [x] one\ncontinued prose\n    - [ ] child\n", true, false},
		{"loose-paragraph-lazy", "- [x] parent\n\n    continued paragraph\nlazy continuation\n    - [ ] child\n", true, false},
		{"fenced-nested", "- [x] one\n```\n    - [ ] ignored\n```\n", true, true},
		{"indented-code", "- [x] one\n\nExample paragraph:\n\n    * [ ] ignored\n", true, true},
		{"outside", "- [x] one\n## Later\n* [ ] ignored\n", true, true},
		{"fence-only", "```\n- [x] fake\n```\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(completionCard + tc.body)
			doc, err := Parse(raw)
			if err != nil {
				t.Fatal(err)
			}
			report, err := doc.ValidateCompletion("P1-observe.md", rules)
			if err != nil {
				t.Fatal(err)
			}
			if report.CardValid != tc.cardValid || report.CriteriaComplete != tc.complete || report.Valid != (tc.cardValid && tc.complete) {
				t.Fatalf("unexpected report: %+v", report)
			}
			if report.Scope != "card-completion-observation" || report.EvidenceValidation != "not_evaluated" || report.BoardValidation != "not_evaluated" {
				t.Fatalf("scope overstated: %+v", report)
			}
			if !bytes.Equal(raw, doc.Bytes()) {
				t.Fatal("observation changed bytes")
			}
		})
	}
}

func TestCompletionMetadataIndependentAndCustomRules(t *testing.T) {
	raw := []byte(strings.Replace(completionCard, "priority: P1", "priority: invalid", 1) + "- [x] checked\n")
	doc, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	r, err := doc.ValidateCompletion("P1-observe.md", defaultValidationRules())
	if err != nil || r.CardValid || !r.CriteriaComplete || r.Valid {
		t.Fatalf("metadata and checkbox observations conflated: %+v %v", r, err)
	}
	rules := defaultValidationRules()
	rules.CriteriaHeading = "Acceptance Criteria"
	doc, err = Parse([]byte(strings.Replace(completionCard, "Completion Criteria", "Acceptance Criteria", 1) + "- [x] checked\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err = doc.ValidateCompletion("P1-observe.md", rules)
	if err != nil || !r.Valid {
		t.Fatalf("custom rules: %+v %v", r, err)
	}
}
