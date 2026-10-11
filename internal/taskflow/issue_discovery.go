package taskflow

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const discoveredInRequired = "discovered-in is required for issue cards; pass --discovered-in REF"

// validateIssueDiscovery checks issue frontmatter discovered-in. Callers wire
// it for issue cards; this helper does not decide kind.
// Missing, empty, and whitespace-only values share one required finding.
// Null, non-string, and non-scalar nodes, and a string that still contains
// CR or LF after a nonempty trim, are explicit malformed findings.
func validateIssueDiscovery(result *ValidationResult, fields map[string]yaml.Node) {
	node, ok := fields["discovered-in"]
	if !ok {
		result.addError("discovered-in", discoveredInRequired)
		return
	}
	if node.Tag == "!!null" {
		result.addError("discovered-in", "discovered-in must be a YAML string scalar, got null")
		return
	}
	if node.Kind != yaml.ScalarNode {
		result.addError("discovered-in", fmt.Sprintf(
			"discovered-in must be a YAML string scalar, got %s", yamlKindName(node.Kind)))
		return
	}
	if node.Tag != "!!str" {
		got := node.Tag
		if got == "" {
			got = "non-string"
		}
		result.addError("discovered-in", "discovered-in must be a YAML string scalar, got "+got)
		return
	}
	if strings.TrimSpace(node.Value) == "" {
		result.addError("discovered-in", discoveredInRequired)
		return
	}
	if strings.ContainsAny(node.Value, "\r\n") {
		result.addError("discovered-in", "discovered-in must be a single line")
	}
}
