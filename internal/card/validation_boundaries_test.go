package card

import (
	"strings"
	"testing"
)

func TestValidationConfigShapeBoundaries(t *testing.T) {
	for name, raw := range map[string]string{
		"missing version":       "card-dialect: {}",
		"unknown version":       "schema-version: 2\ncard-dialect: {}",
		"missing dialect":       "schema-version: 1",
		"null dialect":          "schema-version: 1\ncard-dialect: null",
		"top unknown":           "schema-version: 1\ncard-dialect: {}\nother: yes",
		"duplicate version":     "schema-version: 1\nschema-version: 1\ncard-dialect: {}",
		"multiple documents":    "schema-version: 1\ncard-dialect: {}\n---\nschema-version: 1\ncard-dialect: {}",
		"empty second document": "schema-version: 1\ncard-dialect: {}\n---\n",
		"alias":                 "schema-version: 1\ncard-dialect:\n  name: &name team\n  summary-heading: *name",
		"merge":                 "schema-version: 1\ncard-dialect:\n  <<: {id-required: false}",
		"nonstring key":         "schema-version: 1\ncard-dialect:\n  12: x",
		"wrong bool":            "schema-version: 1\ncard-dialect:\n  id-required: 'false'",
		"empty priorities":      "schema-version: 1\ncard-dialect:\n  priority-values: []",
		"empty prefixes":        "schema-version: 1\ncard-dialect:\n  filename-prefixes: []",
		"numeric name":          "schema-version: 1\ncard-dialect:\n  name: 12",
		"multiline heading":     "schema-version: 1\ncard-dialect:\n  summary-heading: \"Summary\\nAnother\"",
		"zone status":           "schema-version: 1\ncard-dialect:\n  zone-status: {manual: blocked}",
		"transitions":           "schema-version: 1\ncard-dialect:\n  transitions: []",
		"oversize":              strings.Repeat(" ", validationConfigLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseValidationConfig([]byte(raw)); err == nil {
				t.Fatalf("accepted %q", raw)
			}
		})
	}
}

func TestValidationFindingsIndividually(t *testing.T) {
	rules, err := ParseValidationConfig([]byte("schema-version: 1\ncard-dialect: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	valid := "---\nid: TASK-1\ntitle: T\ntype: bug\npriority: P1\n---\n## Summary\nx\n## Completion Criteria\n- [ ] verify me\n"
	for _, change := range []struct{ before, after, field string }{
		{"id: TASK-1\n", "", "id"},
		{"title: T", "title: 12", "title"},
		{"type: bug", "type: unknown", "type"},
		{"priority: P1", "priority: P9", "priority"},
		{"## Summary", "## Context", "summary-heading"},
		{"## Completion Criteria", "## Examples", "criteria-heading"},
		{"- [ ] verify me", "```\n- [ ] fake\n```", "criteria"},
		{"- [ ] verify me", "- [ ]", "criteria"},
	} {
		t.Run(change.field+change.after, func(t *testing.T) {
			doc, err := Parse([]byte(strings.Replace(valid, change.before, change.after, 1)))
			if err != nil {
				t.Fatal(err)
			}
			report, err := doc.ValidateCard("P1-example.md", rules)
			if err != nil || report.Valid || len(report.Findings) != 1 || report.Findings[0].Field != change.field {
				// An empty checkbox can also carry the precise malformed-shape finding.
				if change.after != "- [ ]" || err != nil || report.Valid || len(report.Findings) != 2 || report.Findings[0].Field != "criteria" || report.Findings[1].Field != "criteria" {
					t.Fatalf("report=%+v err=%v", report, err)
				}
			}
		})
	}
}

func TestCriteriaIndentationAndSectionBoundaries(t *testing.T) {
	rules := defaultValidationRules()
	prefix := "---\nid: TASK-1\ntitle: T\ntype: bug\npriority: P1\n---\n"
	for indent := 0; indent <= 3; indent++ {
		padding := strings.Repeat(" ", indent)
		body := padding + "## Summary\nx\n" + padding + "## Completion Criteria\n" + padding + "- [>] pending\n" +
			"    - [ ] code\n\t- [ ] code\n" + padding + "##\tOther\n- [ ] outside\n"
		doc, err := Parse([]byte(prefix + body))
		if err != nil {
			t.Fatal(err)
		}
		report, err := doc.ValidateCard("P1-example.md", rules)
		if err != nil || !report.Valid || len(report.Criteria) != 1 || report.Criteria[0].Checked {
			t.Fatalf("indent=%d report=%+v err=%v", indent, report, err)
		}
	}
	for _, boundary := range []string{"#", "##", "#\tOther", "##\tOther"} {
		doc, err := Parse([]byte(prefix + "## Summary\nx\n## Completion Criteria\n" + boundary + "\n- [ ] outside\n"))
		if err != nil {
			t.Fatal(err)
		}
		report, err := doc.ValidateCard("P1-example.md", rules)
		if err != nil || report.Valid || len(report.Criteria) != 0 {
			t.Fatalf("boundary=%q report=%+v err=%v", boundary, report, err)
		}
	}
	doc, err := Parse([]byte(prefix + "## Summary\nx\n## Completion Criteria\n```\n    ```\n- [ ] still code\n```\n"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := doc.ValidateCard("P1-example.md", rules)
	if err != nil || report.Valid || len(report.Criteria) != 0 {
		t.Fatalf("indented fence: %+v %v", report, err)
	}
}
