// Package cardpath defines explicit non-card names shared by live and Git scans.
package cardpath

import "strings"

// IsDocumentation matches whole basenames, not arbitrary prefixes.
func IsDocumentation(name string) bool {
	switch strings.ToLower(name) {
	case "readme.md", "index.md", "template.md":
		return true
	}
	return false
}

// IsExcludedDirectory applies only below the selected board root.
func IsExcludedDirectory(name string) bool { return name == ".ce" || name == "evidence" }

// WorkflowStatus is the common interpretation of a single workflow segment.
func WorkflowStatus(segment string) string {
	switch strings.ToLower(segment) {
	case "todo", "todos", "pending":
		return "pending"
	case "doing", "doings", "in-progress", "in_progress", "inprogress", "wip":
		return "in-progress"
	case "review", "reviews", "in-review", "in_review":
		return "review"
	case "blocked", "blockeds", "suspended", "suspend", "on-hold", "on_hold", "waiting":
		return "blocked"
	case "done", "dones", "completed", "complete", "finished":
		return "done"
	}
	return ""
}

// IsStatusOpaqueZone names the reserved directories that hold cards without
// expressing a status of their own. A workflow-looking segment below one of
// them is a category, or the provenance of an archived card -- never the zone
// that decides status. Board policy refuses every one of these names as a
// module root, so a reader holding no policy can still recognise them.
func IsStatusOpaqueZone(segment string) bool {
	switch strings.ToLower(segment) {
	case "archive", "_archive", "plan", "issue", "backlog":
		return true
	}
	return false
}
