package boardpolicy

import (
	"fmt"
	"sort"
)

type fileRoleLineageKey struct {
	kind     FileRoleKind
	owner    string
	revision string
}

// ValidateFileRoleLineage compares immutable role bindings in supplied, validated
// policies. Legacy policies contribute no schema-5 bindings. A caller must supply
// the complete authority-bound history and any proposed target policy; this pure
// check does not prove history completeness, provenance, or activation authority.
// It neither mutates policies nor enables file-role activation.
func ValidateFileRoleLineage(policies ...Policy) error {
	bindings := make(map[fileRoleLineageKey]string)
	conflicts := make(map[fileRoleLineageKey]struct{})
	for _, policy := range policies {
		for _, role := range policy.FileRolesV5() {
			key := fileRoleLineageKey{kind: role.Kind, owner: role.Owner, revision: role.SourceRevision}
			value := role.ContentSHA256
			if role.Kind == FileRoleProjection {
				value = role.Format
			}
			if previous, found := bindings[key]; found && previous != value {
				conflicts[key] = struct{}{}
			} else {
				bindings[key] = value
			}
		}
	}
	if len(conflicts) == 0 {
		return nil
	}
	keys := make([]fileRoleLineageKey, 0, len(conflicts))
	for key := range conflicts {
		keys = append(keys, key)
	}
	// Report the same first conflict regardless of history/map traversal order.
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kind != keys[j].kind {
			return keys[i].kind < keys[j].kind
		}
		if keys[i].owner != keys[j].owner {
			return keys[i].owner < keys[j].owner
		}
		return keys[i].revision < keys[j].revision
	})
	key := keys[0]
	return fmt.Errorf("file-role lineage conflict for kind %q owner %q source revision %q", key.kind, key.owner, key.revision)
}
