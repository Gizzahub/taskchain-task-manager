package boardfiles

import "strings"

// These are denied legacy CE state names, not product-owned boardstate
// resources. Recognition grants no migration, cleanup or deletion authority.
func legacyEngineName(name string) bool {
	switch name {
	case ".ce-metrics", ".batch-state.yaml", ".ce-task-cleanup.lock":
		return true
	}
	// boardruntime/cleanup_apply.go creates this quarantine with MkdirTemp.
	const prefix = ".ce-task-cleanup-"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := name[len(prefix):]
	if len(suffix) == 0 || len(suffix) > 10 {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
