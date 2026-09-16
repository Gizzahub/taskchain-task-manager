package card

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestParseValidationConfigDefaultsAndEnvelope(t *testing.T) {
	r, err := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IDRequired || r.CriteriaHeading != "Completion Criteria" || len(r.PriorityValues) != 4 {
		t.Fatalf("defaults: %+v", r)
	}
}

func TestParseValidationConfigCustomAndRejectsUnsupported(t *testing.T) {
	raw := []byte("schema-version: 1\ncard-dialect:\n  name: downstream\n  id-required: false\n  summary-heading: Context\n  criteria-heading: Acceptance Criteria\n  priority-values: [P0, P4]\n  task-types: [bug, audit]\n  filename-prefixes: [P0, P4]\n")
	r, err := ParseValidationConfig(raw)
	if err != nil || r.IDRequired || r.SummaryHeading != "Context" || !contains(r.PriorityValues, "P4") {
		t.Fatalf("custom=%+v err=%v", r, err)
	}
	for _, bad := range []string{
		"schema-version: 1\ncard-dialect:\n  zones: [manual]\n",
		"schema-version: 1\ncard-dialect:\n  criteria-heading: null\n",
		"schema-version: 1\ncard-dialect:\n  name: ''\n",
		"schema-version: 1\ncard-dialect:\n  priority-values: [P0, P0]\n",
		"schema-version: 1\ncard-dialect:\n  criteria-heading: Exit Conditions\n",
		"schema-version: 1\ncard-dialect:\n  filename-prefixes: [P0-x]\n",
		"schema-version: 1\ncard-dialect:\n  priority-values: [1]\n",
		"schema-version: 1\ncard-dialect:\n  name: x\n  name: y\n",
	} {
		if _, err := ParseValidationConfig([]byte(bad)); err == nil {
			t.Errorf("accepted invalid config %q", bad)
		}
	}
}

func TestValidateCardDefaultAndPreservesBytes(t *testing.T) {
	rules, err := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-007\ntitle: Example\ntype: bug\npriority: P1\nx-unknown: keep\n---\n\n# Example\n\n## Summary\ntext\n\n## Completion Criteria\n- [ ] first\n- [x] second\n\n## Examples\n```\n## Completion Criteria\n- [ ] ignored\n```\n")
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	report, err := d.ValidateCard("tasks/todo/P1-example.md", rules)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Valid || len(report.Criteria) != 2 || !report.Criteria[1].Checked {
		t.Fatalf("report=%+v", report)
	}
	if !bytes.Equal(raw, d.Bytes()) {
		t.Fatal("validation changed source bytes")
	}
	encoded, err := json.Marshal(report)
	if err != nil || !strings.Contains(string(encoded), `"boardValidation":"not_evaluated"`) {
		t.Fatalf("json=%s err=%v", encoded, err)
	}
}

func TestValidateCardCustomRulesAndFilenameWarning(t *testing.T) {
	rules, err := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect:\n  id-required: false\n  summary-heading: Context\n  criteria-heading: Acceptance Criteria\n  priority-values: [P4]\n  task-types: [audit]\n  filename-prefixes: [P4]\n"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse([]byte("---\ntitle: T\ntype: audit\npriority: P4\n---\n## context\nbody\n## acceptance criteria\n- [ ] check\n"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := d.ValidateCard("tasks/todo/bad.md", rules)
	if err != nil || !report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Severity != "warning" {
		t.Fatalf("findings=%+v", report.Findings)
	}
}

func TestValidateCardFindingsAndFenceCriteria(t *testing.T) {
	rules, _ := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	d, err := Parse([]byte("---\nid: TASK-1\ntitle: T\ntype: unknown\npriority: P9\n---\n## Summary\n## Completion Criteria\n```md\n- [ ] fake\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := d.ValidateCard("tasks/todo/1.md", rules)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Criteria) != 0 {
		t.Fatalf("report=%+v", report)
	}
	if !strings.Contains(report.Findings[0].Message, "allowed") {
		t.Fatalf("findings=%+v", report.Findings)
	}
}

func TestValidateCardCriteriaBoundariesAndCheckboxShape(t *testing.T) {
	rules, _ := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	raw := "---\nid: TASK-1\ntitle: T\ntype: bug\npriority: P1\n---\n## Summary\nx\n## Completion Criteria\n- [ ] good\n- [x]bad\n    - [ ] indented code\n#\tOther\n- [ ] outside\n"
	d, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	report, err := d.ValidateCard("tasks/todo/1.md", rules)
	if err != nil {
		t.Fatal(err)
	}
	if report.Valid || len(report.Criteria) != 1 || !strings.Contains(report.Findings[0].Message, "checkbox") {
		t.Fatalf("report=%+v", report)
	}
}

func TestValidateCardRejectsInvalidPresentID(t *testing.T) {
	rules, _ := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	for _, id := range []string{"PLAN-1", "TASK-x", "TASK-18446744073709551616"} {
		d, err := Parse([]byte("---\nid: " + id + "\ntitle: T\ntype: bug\npriority: P1\n---\n## Summary\nx\n## Completion Criteria\n- [ ] y\n"))
		if err != nil {
			t.Fatal(err)
		}
		report, err := d.ValidateCard("tasks/todo/1.md", rules)
		if err != nil || report.Valid {
			t.Errorf("id=%s report=%+v err=%v", id, report, err)
		}
	}
}

func TestValidateCardOptionalIDStillRejectsPresentBadValues(t *testing.T) {
	rules, _ := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect:\n  id-required: false\n"))
	for _, id := range []string{"", "123", "null"} {
		field := "id: " + id
		if id == "null" {
			field = "id: null"
		}
		d, err := Parse([]byte("---\n" + field + "\ntitle: T\ntype: bug\npriority: P1\n---\n## Summary\nx\n## Completion Criteria\n- [ ] y\n"))
		if err != nil {
			t.Fatal(err)
		}
		report, err := d.ValidateCard("tasks/todo/1.md", rules)
		if err != nil || report.Valid {
			t.Errorf("id=%s report=%+v err=%v", id, report, err)
		}
	}
}
