package taskstore

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

// queueEntry validates the execution route only for cards that Queue admits.
// Ready and claim continue to use their existing eligibility rules.
func queueEntry(entry Entry) (QueueEntry, error) {
	item := QueueEntry{
		Entry:         entry,
		NeedsHuman:    entry.NeedsHuman,
		ExecutionMode: entry.ExecutionMode,
		AllowedPaths:  append([]string{}, entry.AllowedPaths...),
	}
	if item.ExecutionMode == "" {
		item.ExecutionMode = "implementation"
	}
	switch item.ExecutionMode {
	case "implementation":
		if len(item.AllowedPaths) == 0 {
			return QueueEntry{}, fmt.Errorf("queue card %s: implementation requires allowed-paths", entry.Path)
		}
		for _, allowed := range item.AllowedPaths {
			if err := validateAllowedPath(allowed); err != nil {
				return QueueEntry{}, fmt.Errorf("queue card %s: invalid allowed-path %q: %w", entry.Path, allowed, err)
			}
		}
	case "external", "decision":
		if !item.NeedsHuman {
			return QueueEntry{}, fmt.Errorf("queue card %s: %s requires needs-human: true", entry.Path, item.ExecutionMode)
		}
		if entry.HasAllowedPaths {
			return QueueEntry{}, fmt.Errorf("queue card %s: %s forbids allowed-paths", entry.Path, item.ExecutionMode)
		}
	default:
		return QueueEntry{}, fmt.Errorf("queue card %s: unsupported execution-mode %q", entry.Path, item.ExecutionMode)
	}
	return item, nil
}

func validateAllowedPath(allowed string) error {
	if allowed == "" || path.IsAbs(allowed) || filepath.IsAbs(allowed) {
		return fmt.Errorf("path must be a non-empty repository-relative path")
	}
	if len(allowed) >= 2 && ((allowed[0] >= 'A' && allowed[0] <= 'Z') || (allowed[0] >= 'a' && allowed[0] <= 'z')) && allowed[1] == ':' {
		return fmt.Errorf("path must not use Windows drive notation")
	}
	if strings.ContainsAny(allowed, "\\*?[]") {
		return fmt.Errorf("path contains an unsafe pattern character")
	}
	for _, r := range allowed {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("path contains whitespace or control characters")
		}
	}
	if path.Clean(allowed) != allowed {
		return fmt.Errorf("path must be clean")
	}
	parts := strings.Split(allowed, "/")
	for _, part := range parts {
		if part == "." || part == ".." || part == "" {
			return fmt.Errorf("path contains traversal")
		}
	}
	if parts[0] == "tasks" {
		return fmt.Errorf("path is inside the configured tasks board")
	}
	return nil
}
