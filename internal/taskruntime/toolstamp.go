package taskruntime

import "strings"

const unknownToolRevision = "unknown"

var (
	toolBuildRevision string
	toolBuildDescribe string
)

// toolRevision is the commit this process was built from. An unstamped build
// says unknown rather than inventing an identifier. Settle and close require
// the field to be non-blank, and unknown satisfies that without a claim.
func toolRevision() string {
	if strings.TrimSpace(toolBuildRevision) == "" {
		return unknownToolRevision
	}
	return strings.TrimSpace(toolBuildRevision)
}

func toolVersion() string {
	if described := strings.TrimSpace(toolBuildDescribe); described != "" {
		return described
	}
	return toolRevision()
}
