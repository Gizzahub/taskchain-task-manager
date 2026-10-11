package taskflow

import (
	"context"
	"strings"
	"testing"
)

func TestCreateIssueDiscoveredInDoesNotDefaultFromTitle(t *testing.T) {
	root := t.TempDir()
	absent, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "Leak site", Created: "2026-10-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(absent.Content, "discovered-in: \"\"\n") {
		t.Fatalf("absent value was not an empty placeholder:\n%s", absent.Content)
	}
	if strings.Contains(absent.Content, "discovered-in: \"Leak site\"") {
		t.Fatalf("title was copied into discovered-in:\n%s", absent.Content)
	}
	if strings.Contains(absent.Content, "<observable condition>") || strings.Contains(absent.Content, "## Completion Criteria") {
		t.Fatalf("issue scaffold changed criterion behavior:\n%s", absent.Content)
	}

	quoted, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "Leak site", Created: "2026-10-12", DiscoveredIn: `say "hi"`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(quoted.Content, "discovered-in: \"say \\\"hi\\\"\"\n") {
		t.Fatalf("reference not preserved:\n%s", quoted.Content)
	}
	if strings.Contains(quoted.Content, "Leak site\"\ndiscovered") || strings.Contains(quoted.Content, "discovered-in: \"Leak site\"") {
		t.Fatalf("title leaked into discovered-in:\n%s", quoted.Content)
	}

	if _, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "Keep criteria", Created: "2026-10-12", DiscoveredIn: "REF-1",
	}); err == nil || !strings.Contains(err.Error(), "--discovered-in is only valid for an issue") {
		t.Fatalf("task accepted discovered-in: %v", err)
	}
	if _, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "Broken ref", Created: "2026-10-12", DiscoveredIn: "a\nb",
	}); err == nil || !strings.Contains(err.Error(), "single-line") {
		t.Fatalf("newline reference accepted: %v", err)
	}
}

func TestCreateTaskCriterionPlaceholderUnchanged(t *testing.T) {
	root := t.TempDir()
	result, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "bare", Created: "2026-10-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "<observable condition>") || !strings.Contains(result.Content, "| verify:") {
		t.Fatalf("criterion placeholder changed:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "discovered-in:") {
		t.Fatalf("task scaffold grew discovered-in:\n%s", result.Content)
	}
	bound, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "bound card", Created: "2026-10-12",
		Criteria: []string{"bound | verify: `test -f absent-new-marker.txt`"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bound.Content, "<observable condition>") {
		t.Fatal("explicit criteria no longer suppress the placeholder")
	}
	if !strings.Contains(bound.Content, "- [ ] bound | verify: `test -f absent-new-marker.txt`\n") {
		t.Fatalf("criterion line changed:\n%s", bound.Content)
	}
}

func TestValidateIssueDiscoveredInPlaceholder(t *testing.T) {
	root := t.TempDir()
	empty := "---\nid: ISSUE-1\ntitle: Leak site\ntype: bug\ndiscovered-in: \"\"\n---\n\n## Summary\n\nbody\n"
	if err := writeFileForTest(root, "tasks/issue/001-leak.md", []byte(empty)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Valid() {
		t.Fatalf("empty placeholder should fail validation: %+v", results)
	}
	if len(results[0].Errors) != 1 || results[0].Errors[0].Field != "discovered-in" || results[0].Errors[0].Message != "discovered-in is required for issue cards; pass --discovered-in REF" {
		t.Fatalf("errors=%+v", results[0].Errors)
	}
	for _, finding := range append(results[0].Errors, results[0].Warnings...) {
		if finding.Field == "criterion_placeholder" || strings.Contains(finding.Message, "Completion Criteria") {
			t.Fatalf("criterion diagnosis leaked onto an issue: %+v", finding)
		}
	}

	filled := "---\nid: ISSUE-1\ntitle: Leak site\ntype: bug\ndiscovered-in: REF-9\n---\n\n## Summary\n\nbody\n"
	if err := writeFileForTest(root, "tasks/issue/001-leak.md", []byte(filled)); err != nil {
		t.Fatal(err)
	}
	results, _, err = ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Valid() || len(results[0].Warnings) != 0 {
		t.Fatalf("filled reference diagnosed: %+v", results)
	}

	missing := "---\nid: ISSUE-1\ntitle: Leak site\ntype: bug\n---\n\n## Summary\n\nbody\n"
	if err := writeFileForTest(root, "tasks/issue/001-leak.md", []byte(missing)); err != nil {
		t.Fatal(err)
	}
	results, _, err = ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Valid() || len(results[0].Errors) != 1 || results[0].Errors[0].Message != "discovered-in is required for issue cards; pass --discovered-in REF" {
		t.Fatalf("missing field not diagnosed: %+v", results)
	}
}

func TestValidateTaskCriterionPlaceholderUnchanged(t *testing.T) {
	root := t.TempDir()
	raw := "---\nid: TASK-1\ntitle: bare\ntype: feature\npriority: P2\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] <observable condition>\n"
	if err := writeFileForTest(root, "tasks/todo/001-bare.md", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Valid() {
		t.Fatalf("task validity changed: %+v", results[0])
	}
	var messages []string
	for _, warning := range results[0].Warnings {
		if warning.Field == "discovered-in" {
			t.Fatalf("task received discovered-in diagnosis: %+v", warning)
		}
		messages = append(messages, warning.Field+": "+warning.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "criterion_placeholder: criterion is an unfilled <...> placeholder: <observable condition>") {
		t.Fatalf("criterion warning changed: %s", joined)
	}
}
