package boardpolicy

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// FileRoleKind is the limited set of non-card files a schema 4 policy may
// explicitly classify. It is reader metadata only; it grants no write or
// cleanup authority.
type FileRoleKind string

const (
	FileRoleProjection FileRoleKind = "projection"
	FileRoleGuidance   FileRoleKind = "guidance"
	FileRoleTemplate   FileRoleKind = "template"

	maxFileRoleOwnerBytes    = 128
	maxFileRoleRevisionBytes = 128
	maxFileRoleMarkerBytes   = 512
)

// FileRole describes one exact board-relative regular-file name.
type FileRole struct {
	Path     string
	Kind     FileRoleKind
	Owner    string
	Revision string
	Marker   string
}

func copyFileRoles(d Declaration) (map[string]FileRole, error) {
	if !d.FileRolesDeclared {
		return nil, nil
	}
	roles := make(map[string]FileRole, len(d.FileRoles))
	for _, role := range d.FileRoles {
		if err := validateFileRole(role); err != nil {
			return nil, err
		}
		if _, exists := roles[role.Path]; exists {
			return nil, fmt.Errorf("duplicate file role path %q", role.Path)
		}
		roles[role.Path] = role
	}
	return roles, nil
}

func validateFileRole(role FileRole) error {
	if !validFileRolePath(role.Path) {
		return fmt.Errorf("invalid file role path %q", role.Path)
	}
	switch role.Kind {
	case FileRoleProjection:
		if role.Path != "README.md" && role.Path != "INDEX.md" {
			return fmt.Errorf("projection path %q is unsupported", role.Path)
		}
		if !validFileRoleLabel(role.Owner, maxFileRoleOwnerBytes) || !validFileRoleLabel(role.Revision, maxFileRoleRevisionBytes) || !validFileRoleMarker(role.Marker) {
			return fmt.Errorf("projection %q requires owner, revision, and marker", role.Path)
		}
		if !strings.Contains(role.Marker, role.Owner) || !strings.Contains(role.Marker, role.Revision) {
			return fmt.Errorf("projection %q marker must bind owner and revision", role.Path)
		}
	case FileRoleGuidance:
		if role.Path != "AGENTS.md" && role.Path != "CLAUDE.md" {
			return fmt.Errorf("guidance path %q is unsupported", role.Path)
		}
		if role.Owner != "" || role.Revision != "" || role.Marker != "" {
			return fmt.Errorf("guidance %q cannot carry projection metadata", role.Path)
		}
	case FileRoleTemplate:
		if role.Path != "TEMPLATE.md" {
			return fmt.Errorf("template path %q is unsupported", role.Path)
		}
		if role.Owner != "" || role.Revision != "" || role.Marker != "" {
			return fmt.Errorf("template %q cannot carry projection metadata", role.Path)
		}
	default:
		return fmt.Errorf("unknown file role %q", role.Kind)
	}
	return nil
}

func validFileRoleLabel(value string, limit int) bool {
	return len(value) <= limit && value != "" && value == strings.TrimSpace(value) && validFileRoleText(value)
}

func validFileRoleMarker(value string) bool {
	return len(value) <= maxFileRoleMarkerBytes && value != "" && validFileRoleText(value)
}

func validFileRoleText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validFileRolePath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsAny(value, "*?[]{}\x00") || strings.HasPrefix(value, ".") || path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, "../") || value == ".." {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.HasPrefix(segment, ".") {
			return false
		}
	}
	return true
}

func parseFileRoles(node *yaml.Node) ([]FileRole, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, errors.New("file-roles must be a mapping")
	}
	if err := uniqueMapping(node, "file-roles"); err != nil {
		return nil, err
	}
	var enabled, projections, guidance, templates *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "enabled":
			enabled = node.Content[i+1]
		case "projections":
			projections = node.Content[i+1]
		case "guidance":
			guidance = node.Content[i+1]
		case "templates":
			templates = node.Content[i+1]
		default:
			return nil, fmt.Errorf("unknown file-roles field %q", node.Content[i].Value)
		}
	}
	if enabled == nil || enabled.Kind != yaml.ScalarNode || enabled.Tag != "!!bool" || enabled.Value != "true" {
		return nil, errors.New("file-roles.enabled must be true")
	}
	if projections == nil || guidance == nil || templates == nil {
		return nil, errors.New("file-roles requires projections, guidance, and templates")
	}
	roles, err := parseProjectionRoles(projections)
	if err != nil {
		return nil, err
	}
	for _, group := range []struct {
		node *yaml.Node
		kind FileRoleKind
		name string
	}{{guidance, FileRoleGuidance, "guidance"}, {templates, FileRoleTemplate, "templates"}} {
		items, err := parseFileRolePaths(group.node, group.kind, group.name)
		if err != nil {
			return nil, err
		}
		roles = append(roles, items...)
	}
	if _, err := copyFileRoles(Declaration{FileRolesDeclared: true, FileRoles: roles}); err != nil {
		return nil, err
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Path < roles[j].Path })
	return roles, nil
}

func parseProjectionRoles(node *yaml.Node) ([]FileRole, error) {
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
		return nil, errors.New("file-roles.projections must be a sequence")
	}
	roles := make([]FileRole, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.MappingNode || item.Tag != "!!map" {
			return nil, errors.New("file-roles.projections entries must be mappings")
		}
		if err := uniqueMapping(item, "file-roles.projections"); err != nil {
			return nil, err
		}
		var role FileRole
		role.Kind = FileRoleProjection
		for i := 0; i+1 < len(item.Content); i += 2 {
			key, value := item.Content[i].Value, item.Content[i+1]
			if value.Kind != yaml.ScalarNode || value.Tag != "!!str" || value.Value == "" {
				return nil, fmt.Errorf("file-roles.projections.%s must be a nonempty string", key)
			}
			switch key {
			case "path":
				role.Path = value.Value
			case "owner":
				role.Owner = value.Value
			case "revision":
				role.Revision = value.Value
			case "marker":
				role.Marker = value.Value
			default:
				return nil, fmt.Errorf("unknown file-roles.projections field %q", key)
			}
		}
		if err := validateFileRole(role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}

func parseFileRolePaths(node *yaml.Node, kind FileRoleKind, name string) ([]FileRole, error) {
	if node.Kind != yaml.SequenceNode || node.Tag != "!!seq" {
		return nil, fmt.Errorf("file-roles.%s must be a sequence", name)
	}
	roles := make([]FileRole, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Value == "" {
			return nil, fmt.Errorf("file-roles.%s entries must be nonempty strings", name)
		}
		role := FileRole{Path: item.Value, Kind: kind}
		if err := validateFileRole(role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, nil
}
