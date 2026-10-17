package boardpolicy

import (
	"errors"
	"fmt"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// FileRoleV5 is the reader-only schema 5 declaration for one exact root file.
type FileRoleV5 struct {
	Path           string
	Kind           FileRoleKind
	Owner          string
	SourceRevision string
	ContentSHA256  string
	Format         string
}

var sha256Lower = regexp.MustCompile(`^[0-9a-f]{64}$`)

func copyFileRolesV5(items []FileRoleV5) (map[string]FileRoleV5, error) {
	roles := make(map[string]FileRoleV5, len(items))
	for _, role := range items {
		if err := validateFileRoleV5(role); err != nil {
			return nil, err
		}
		if _, found := roles[role.Path]; found {
			return nil, fmt.Errorf("duplicate file role path %q", role.Path)
		}
		roles[role.Path] = role
	}
	return roles, nil
}

func validateFileRoleV5(role FileRoleV5) error {
	if !validFileRolePath(role.Path) || !validFileRoleLabel(role.Owner, maxFileRoleOwnerBytes) || !validFileRoleLabel(role.SourceRevision, maxFileRoleRevisionBytes) {
		return fmt.Errorf("invalid schema 5 file-role metadata for %q", role.Path)
	}
	switch role.Kind {
	case FileRoleProjection:
		if (role.Path != "README.md" && role.Path != "INDEX.md") || role.Format != "live-card-paths-v1" || role.ContentSHA256 != "" {
			return fmt.Errorf("invalid schema 5 projection %q", role.Path)
		}
	case FileRoleGuidance:
		if (role.Path != "AGENTS.md" && role.Path != "CLAUDE.md") || role.Format != "" || !sha256Lower.MatchString(role.ContentSHA256) {
			return fmt.Errorf("invalid schema 5 guidance %q", role.Path)
		}
	case FileRoleTemplate:
		if role.Path != "TEMPLATE.md" || role.Format != "" || !sha256Lower.MatchString(role.ContentSHA256) {
			return fmt.Errorf("invalid schema 5 template %q", role.Path)
		}
	default:
		return fmt.Errorf("unknown schema 5 file role %q", role.Kind)
	}
	return nil
}

func parseFileRolesV5(node *yaml.Node) ([]FileRoleV5, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, errors.New("file-roles must be a mapping")
	}
	if err := uniqueMapping(node, "file-roles"); err != nil {
		return nil, err
	}
	groups := map[string]*yaml.Node{}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i].Value
		if k != "enabled" && k != "projections" && k != "guidance" && k != "templates" {
			return nil, fmt.Errorf("unknown file-roles field %q", k)
		}
		groups[k] = node.Content[i+1]
	}
	if v := groups["enabled"]; v == nil || v.Kind != yaml.ScalarNode || v.Tag != "!!bool" || v.Value != "true" {
		return nil, errors.New("file-roles.enabled must be true")
	}
	var out []FileRoleV5
	for _, group := range []struct {
		name   string
		kind   FileRoleKind
		fields []string
	}{{"projections", FileRoleProjection, []string{"path", "owner", "source-revision", "format"}}, {"guidance", FileRoleGuidance, []string{"path", "owner", "source-revision", "content-sha256"}}, {"templates", FileRoleTemplate, []string{"path", "owner", "source-revision", "content-sha256"}}} {
		n := groups[group.name]
		if n == nil || n.Kind != yaml.SequenceNode || n.Tag != "!!seq" {
			return nil, fmt.Errorf("file-roles.%s must be a sequence", group.name)
		}
		for _, item := range n.Content {
			if item.Kind != yaml.MappingNode || item.Tag != "!!map" {
				return nil, fmt.Errorf("file-roles.%s entries must be mappings", group.name)
			}
			if err := uniqueMapping(item, "file-roles."+group.name); err != nil {
				return nil, err
			}
			values := map[string]string{}
			for i := 0; i+1 < len(item.Content); i += 2 {
				k, v := item.Content[i].Value, item.Content[i+1]
				if !containsV5(group.fields, k) {
					return nil, fmt.Errorf("unknown file-roles.%s field %q", group.name, k)
				}
				if v.Kind != yaml.ScalarNode || v.Tag != "!!str" || v.Value == "" {
					return nil, fmt.Errorf("file-roles.%s.%s must be a nonempty string", group.name, k)
				}
				values[k] = v.Value
			}
			for _, k := range group.fields {
				if values[k] == "" {
					return nil, fmt.Errorf("file-roles.%s.%s is required", group.name, k)
				}
			}
			out = append(out, FileRoleV5{Path: values["path"], Kind: group.kind, Owner: values["owner"], SourceRevision: values["source-revision"], ContentSHA256: values["content-sha256"], Format: values["format"]})
		}
	}
	if _, err := copyFileRolesV5(out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
func containsV5(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
