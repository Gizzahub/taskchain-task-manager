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
