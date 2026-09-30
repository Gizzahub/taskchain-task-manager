package taskflow

import (
	"context"
	"strings"
	"testing"
)

const validChoreCard = "---\nid: TASK-1\ntype: chore\ntitle: \"x\"\npriority: P3\nstatus: todo\ncreated: 2026-09-29\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f absent-marker.txt`\n"

func validateOneCard(t *testing.T, tasksRel string, raw string) ValidationResult {
	t.Helper()
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/"+tasksRel, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	return results[0]
}

func messages(result ValidationResult, wantErrors bool) string {
	var b strings.Builder
	if wantErrors {
		for _, e := range result.Errors {
			b.WriteString(e.Message + "\n")
		}
		return b.String()
	}
	for _, w := range result.Warnings {
		b.WriteString(w.Message + "\n")
	}
	return b.String()
}

func TestValidatePlaceholderCriterionIsWarningNotUnboundError(t *testing.T) {
	raw := strings.Replace(validChoreCard, "- [ ] bound | verify: `test -f absent-marker.txt`",
		"- [ ] <observable condition>", 1)
	result := validateOneCard(t, "todo/001-placeholder.md", raw)
	if len(result.Errors) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("verdict = %#v, want one warning, no errors", result)
	}
	if !strings.Contains(result.Warnings[0].Message, "criterion is an unfilled <...> placeholder: <observable condition>") {
		t.Fatalf("warning = %q", result.Warnings[0].Message)
	}
}

func TestValidateUnboundCheckboxIsError(t *testing.T) {
	raw := strings.Replace(validChoreCard, "- [ ] bound | verify: `test -f absent-marker.txt`",
		"- [ ] manually confirm the output looks right", 1)
	result := validateOneCard(t, "todo/001-unbound.md", raw)
	if len(result.Errors) != 1 ||
		!strings.Contains(result.Errors[0].Message, "checkbox has no verify binding: manually confirm the output looks right") {
		t.Fatalf("verdict = %#v, want unbound checkbox error", result)
	}
}

func TestValidateCriteriaHeadingAliasWarning(t *testing.T) {
	raw := strings.Replace(validChoreCard, "## Completion Criteria", "## Acceptance Criteria", 1)
	result := validateOneCard(t, "todo/001-alias.md", raw)
	if len(result.Errors) != 0 || len(result.Warnings) != 1 {
		t.Fatalf("verdict = %#v, want alias warning only", result)
	}
	if !strings.Contains(result.Warnings[0].Message,
		`Criteria heading "Acceptance Criteria" is an accepted alias; the canonical heading is "Completion Criteria"`) {
		t.Fatalf("warning = %q", result.Warnings[0].Message)
	}
}

func TestSeveredCommandSubstitutionShapes(t *testing.T) {
	cases := []struct {
		command string
		severed bool
	}{
		{"grep -q alpha data.txt; rc=$?; test \"$rc\" -eq 0", false},
		{"out=$(cat data.txt) || exit 1", false},
		{"out=$(cat data.txt); grep -q alpha \"$out\"", true},
		{"test -f marker.txt", false},
		{"echo \"a;b\"; test -f marker.txt", false},
	}
	for _, tt := range cases {
		if _, got := severedCommandSubstitution(tt.command); got != tt.severed {
			t.Errorf("severedCommandSubstitution(%q) severed = %v, want %v", tt.command, got, tt.severed)
		}
	}
}

func TestValidateSeveredSubstitutionError(t *testing.T) {
	raw := strings.Replace(validChoreCard, "- [ ] bound | verify: `test -f absent-marker.txt`",
		"- [ ] severed is flagged | verify: `out=$(cat data.txt); grep -q alpha \"$out\"`", 1)
	result := validateOneCard(t, "todo/001-shapes.md", raw)
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0].Message,
		"verify binding severs a command substitution with ';', discarding its exit status so the binding cannot go red: out=$(cat data.txt)") {
		t.Fatalf("verdict = %#v, want severed substitution error", result)
	}
}

func TestValidatePlanCountersRequireRoster(t *testing.T) {
	withRoster := "---\nid: TASK-1\ntype: plan\ntitle: \"p\"\nstatus: todo\nchildren:\n  - TASK-2\ntotal-tasks: 1\n---\n\n## Summary\n\np\n\n## Completion Criteria\n\n- [ ] b | verify: `test -f x`\n"
	result := validateOneCard(t, "todo/001-plan.md", withRoster)
	if got := messages(result, true); strings.Contains(got, "children roster") {
		t.Fatalf("roster present, got %q", got)
	}

	noRoster := "---\nid: TASK-1\ntype: plan\ntitle: \"p\"\nstatus: todo\ntotal-tasks: 4\ncompleted-tasks: 1\nprogress: 25\n---\n\n## Summary\n\np\n\n## Completion Criteria\n\n- [ ] b | verify: `test -f x`\n"
	result = validateOneCard(t, "todo/001-plan.md", noRoster)
	if got := messages(result, true); !strings.Contains(got, "Plan progress counters require a children roster") {
		t.Fatalf("roster absent, got %q", got)
	}
}

func TestValidatePlanZoneFilingWarning(t *testing.T) {
	result := validateOneCard(t, "plan/010-with-roster.md",
		"---\nid: TASK-10\ntype: feature\ntitle: \"p\"\nstatus: todo\n---\n\n## Summary\n\np\n\n## Completion Criteria\n\n- [ ] b | verify: `test -f x`\n")
	if got := messages(result, false); !strings.Contains(got, "task document filed under a plan zone") {
		t.Fatalf("warnings = %q, want plan zone filing warning", got)
	}
}

func TestValidateZonePathCitations(t *testing.T) {
	cases := []struct {
		name    string
		summary string
		wantErr bool
	}{
		{"bare unresolved citation", "Cites tasks/todo/999-ghost.md in prose.", true},
		{"backticked example is not a citation", "Example: `tasks/todo/999-ghost.md` in backticks.", false},
		{"non numeric placeholder is not a citation", "See tasks/todo/NNN-thing.md.", false},
		{"resolved citation passes", "Cites tasks/todo/001-cite.md which exists.", false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			raw := strings.Replace(validChoreCard, "body", tt.summary, 1)
			if err := writeFileForTest(root, "tasks/todo/001-cite.md", []byte(raw)); err != nil {
				t.Fatal(err)
			}
			results, _, err := ValidateAll(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			got := messages(results[0], true)
			if strings.Contains(got, "Zone-path citation does not resolve on disk") != tt.wantErr {
				t.Fatalf("errors = %q, want citation error = %v", got, tt.wantErr)
			}
		})
	}
}

func TestRenderValidateAllEmptyBoardEndsAtFinding(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/.keep", []byte("")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	invalid, err := RenderValidateAll(context.Background(), &out, root)
	if err != nil || invalid != 0 {
		t.Fatalf("invalid = %d, err = %v", invalid, err)
	}
	rendered := out.String()
	if !strings.HasSuffix(rendered, "No task files found\n") {
		t.Fatalf("empty board render = %q, want it to end at the finding", rendered)
	}
}
