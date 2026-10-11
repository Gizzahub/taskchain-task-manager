package taskflow

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestValidateIssueDiscovery(t *testing.T) {
	const required = "discovered-in is required for issue cards; pass --discovered-in REF"
	cases := []struct {
		name    string
		yaml    string
		want    string
		wantNil bool
	}{
		{name: "missing", yaml: "id: ISSUE-001\n", want: required},
		{name: "empty", yaml: "discovered-in: \"\"\n", want: required},
		{name: "whitespace", yaml: "discovered-in: \"  \\t  \"\n", want: required},
		{name: "null", yaml: "discovered-in: null\n", want: "discovered-in must be a YAML string scalar, got null"},
		{name: "non-string", yaml: "discovered-in: 12\n", want: "discovered-in must be a YAML string scalar, got !!int"},
		{name: "list", yaml: "discovered-in:\n  - TASK-1\n", want: "discovered-in must be a YAML string scalar, got sequence"},
		{name: "crlf", yaml: "discovered-in: \"ref\\r\\nmore\"\n", want: "discovered-in must be a single line"},
		{name: "valid reference", yaml: "discovered-in: internal/taskflow/newcard.go:340\n", wantNil: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var fields map[string]yaml.Node
			if err := yaml.Unmarshal([]byte(tc.yaml), &fields); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			result := &ValidationResult{}
			validateIssueDiscovery(result, fields)
			if tc.wantNil {
				if len(result.Errors) != 0 || len(result.Warnings) != 0 {
					t.Fatalf("findings = %+v warnings %+v", result.Errors, result.Warnings)
				}
				return
			}
			if len(result.Errors) != 1 {
				t.Fatalf("errors = %+v", result.Errors)
			}
			got := result.Errors[0]
			if got.Field != "discovered-in" || got.Message != tc.want {
				t.Fatalf("finding = %+v, want field discovered-in message %q", got, tc.want)
			}
		})
	}
}
