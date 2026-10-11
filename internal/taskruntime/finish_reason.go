package taskruntime

import "strings"

// failingCheckReason names the check that made the provider refuse, using
// only output already captured. An empty result keeps the generic reason.
func failingCheckReason(index int, result CommandResult) string {
	stage := "integrate-check"
	if index == 1 {
		stage = "integrate-run"
	}
	line := firstMatch(result.Stdout, true)
	if line == "" {
		line = firstMatch(result.Stderr, true)
	}
	if line == "" {
		line = firstMatch(result.Stderr, false)
	}
	if line == "" {
		line = firstMatch(result.Stdout, false)
	}
	if line == "" {
		return ""
	}
	return stage + " failed: " + line
}

func firstMatch(output string, failOnly bool) string {
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		rest, ok := strings.CutPrefix(line, "FAIL ")
		if ok {
			return strings.TrimSpace(rest)
		}
		if !failOnly {
			return line
		}
	}
	return ""
}
