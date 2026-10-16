// Package boardstate classifies the task-manager files that are private to
// the engine. It intentionally does not accept a generic hidden-file prefix:
// every accepted name is either an exact stable state file or a digest-bound
// transaction artifact.
package boardstate

import "strings"

// Kind identifies the only engine-private filesystem roles. Unknown must be
// handled by the board policy rather than silently treated as private state.
type Kind uint8

const (
	Unknown Kind = iota
	Directory
	File
)

var stableFiles = map[string]struct{}{
	".task-manager-ids.json":               {},
	".task-manager-claims.json":            {},
	".task-manager-transitions.json":       {},
	".task-manager-archives.json":          {},
	".task-manager-repairs.json":           {},
	".task-manager-bundles.json":           {},
	".task-manager-relocations.json":       {},
	".task-manager-policy.json":            {},
	".task-manager-policy-activation.json": {},
	".task-manager-archive-capacity.json":  {},
	".task-manager-owner-rejoin.json":      {},
	".ce-maintenance-receipt.json":         {},
}

// Classify accepts slash-relative names below a board root. It returns
// Unknown for nested paths and malformed dynamic names so callers can report
// them as board-policy violations instead of creating a hidden-file bypass.
func Classify(rel string) Kind {
	if rel == ".task-manager-context" || rel == ".task-manager.lock" {
		return Directory
	}
	if strings.Contains(rel, "/") || rel == "" {
		return Unknown
	}
	if _, ok := stableFiles[rel]; ok {
		return File
	}
	if staged(rel) || digestArtifact(rel, ".task-manager-archive-capacity-", ".bin") ||
		digestArtifact(rel, ".task-manager-archive-rebind-", ".bin") ||
		digestArtifact(rel, ".task-manager-owner-rejoin-capacity-", ".bin") ||
		digestArtifact(rel, ".task-manager-owner-rejoin-plan-", ".json") ||
		digestArtifact(rel, ".task-manager-owner-rejoin-payload-", ".bin") {
		return File
	}
	return Unknown
}

// IsDirectory reports whether rel is an exact engine-owned directory. The
// caller may skip its descendants only after separately rejecting symlinks.
func IsDirectory(rel string) bool { return Classify(rel) == Directory }

// IsFile reports whether rel is an exact engine-owned regular-file name.
func IsFile(rel string) bool { return Classify(rel) == File }

func staged(name string) bool {
	const prefix = ".task-manager-stage-"
	return strings.HasPrefix(name, prefix) && hex(name[len(prefix):], 32)
}

func digestArtifact(name, prefix, suffix string) bool {
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	digest := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	return hex(digest, 64)
}

func hex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, c := range value {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
