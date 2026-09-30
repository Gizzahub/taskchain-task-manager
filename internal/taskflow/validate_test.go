package taskflow

import (
	"context"
	"strings"
	"testing"
)

func TestValidateCardFrontmatterErrors(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr []string
	}{
		{
			name:    "no frontmatter",
			raw:     "## Summary\n\nbody\n",
			wantErr: []string{"task file has no frontmatter"},
		},
		{
			name:    "missing id and title",
			raw:     "---\ntype: feature\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f x`\n",
			wantErr: []string{"Missing required frontmatter field: id", "Missing required frontmatter field: title"},
		},
		{
			name:    "invalid id shape",
			raw:     "---\nid: task-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f x`\n",
			wantErr: []string{"Invalid id: task-1"},
		},
		{
			name:    "bad priority and effort",
			raw:     "---\nid: TASK-1\ntype: feature\ntitle: x\npriority: urgent\neffort: XXL\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f x`\n",
			wantErr: []string{"Invalid priority: urgent", "Invalid effort: XXL"},
		},
		{
			name:    "missing summary and criteria",
			raw:     "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Goal\n\nbody\n",
			wantErr: []string{"Missing Summary section", "Missing Completion Criteria section"},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(tt.raw)); err != nil {
				t.Fatal(err)
			}
			results, _, err := ValidateAll(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 {
				t.Fatalf("results = %d, want 1", len(results))
			}
			var messages []string
			for _, e := range results[0].Errors {
				messages = append(messages, e.Message)
			}
			joined := strings.Join(messages, "\n")
			for _, want := range tt.wantErr {
				if !strings.Contains(joined, want) {
					t.Errorf("errors missing %q; got:\n%s", want, joined)
				}
			}
		})
	}
}

func TestValidateFilenameWarningIsNotAnError(t *testing.T) {
	root := t.TempDir()
	raw := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f x`\n"
	if err := writeFileForTest(root, "tasks/todo/Weird Name.md", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Errors) != 0 || len(results[0].Warnings) != 1 {
		t.Fatalf("verdict = %#v, want one warning, no errors", results)
	}
	if !strings.Contains(results[0].Warnings[0].Message, "Non-standard filename") {
		t.Fatalf("warning = %q", results[0].Warnings[0].Message)
	}
}

func TestValidateOrphanBindingIsAnError(t *testing.T) {
	orphan := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound\n  | verify: `test -f x`\n"
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(orphan)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Errors) != 1 {
		t.Fatalf("verdict = %#v, want exactly one error", results)
	}
	if !strings.Contains(results[0].Errors[0].Message, "binding must be on the same checkbox line") {
		t.Fatalf("error = %q", results[0].Errors[0].Message)
	}
}

func TestValidateMalformedBindingValueIsAWarning(t *testing.T) {
	malformed := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: just prose\n"
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(malformed)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Errors) != 0 || len(results[0].Warnings) != 1 {
		t.Fatalf("verdict = %#v, want one warning, no errors", results)
	}
	if !strings.Contains(results[0].Warnings[0].Message, "neither a backtick command nor") {
		t.Fatalf("warning = %q", results[0].Warnings[0].Message)
	}
}

func TestValidateUnknownStatusOutsideAnyZone(t *testing.T) {
	// A card filed outside a zone directory has no zone to speak for it, so
	// its own frontmatter word is validated.
	raw := "---\nid: TASK-1\ntype: feature\ntitle: x\nstatus: fishing\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f x`\n"
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/001-a.md", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Errors) != 1 ||
		!strings.Contains(results[0].Errors[0].Message, "Invalid status: fishing") {
		t.Fatalf("verdict = %#v, want Invalid status error", results)
	}
}

func TestValidateCitationsCountBindings(t *testing.T) {
	root := t.TempDir()
	raw := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] good | verify: `test -f x`\n- [ ] odd | verify: `curl example.com`\n"
	if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	_, citations, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if citations.Examined != 1 || citations.Skipped != 1 {
		t.Fatalf("citations = %#v, want 1 examined, 1 skipped", citations)
	}
}

func TestValidateInsideFenceIsNotCriteria(t *testing.T) {
	root := t.TempDir()
	// A fenced block must not satisfy the criteria requirement, and a checkbox
	// indented inside it must not become a criterion.
	raw := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n```bash\n- [ ] fake | verify: `test -f x`\n```\n\n- [ ] real | verify: `test -f x`\n"
	if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(raw)); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAll(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Errors) != 0 {
		t.Fatalf("verdict = %#v, want clean board", results)
	}
	if got := results[0].Path; got != "tasks/todo/001-a.md" {
		t.Fatalf("path = %q", got)
	}
}

func TestRenderValidateAllSummaryShape(t *testing.T) {
	root := t.TempDir()
	valid := "---\nid: TASK-1\ntype: feature\ntitle: x\n---\n\n## Summary\n\nbody\n\n## Completion Criteria\n\n- [ ] good | verify: `test -f absent-x`\n"
	if err := writeFileForTest(root, "tasks/todo/001-a.md", []byte(valid)); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	invalid, err := RenderValidateAll(context.Background(), &out, root)
	if err != nil {
		t.Fatal(err)
	}
	if invalid != 0 {
		t.Fatalf("invalid = %d, want 0", invalid)
	}
	rendered := out.String()
	for _, want := range []string{
		ReferenceStamp,
		"📋 Validating all tasks...",
		"Validating: tasks/todo/001-a.md",
		"  ✅ Valid (no errors or warnings)",
		"Summary: 1 valid, 0 invalid (total: 1)",
		"Path citations: 1 binding(s) examined, 0 skipped as unreadable without a shell",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("render missing %q", want)
		}
	}
}

func TestRenderValidateAllEmptyBoard(t *testing.T) {
	root := t.TempDir()
	var out strings.Builder
	invalid, err := RenderValidateAll(context.Background(), &out, root)
	if err != nil {
		t.Fatal(err)
	}
	if invalid != 0 || !strings.Contains(out.String(), "No task files found") {
		t.Fatalf("empty board render = %d, %q", invalid, out.String())
	}
}
