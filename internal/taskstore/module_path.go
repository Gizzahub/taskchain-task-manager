package taskstore

import (
	"errors"
	"path"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// modulePath is an explicitly declared scope, not a zone found by searching
// arbitrary path segments. Legacy top-level paths retain their own contract.
type modulePath struct {
	Module   string
	Zone     string
	Category []string
	Filename string
}

func classifyModulePath(name string, policy boardpolicy.Policy) (modulePath, error) {
	var result modulePath
	if name == "" || len(name) > 1023 || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return result, errors.New("invalid module card path")
	}
	parts := strings.Split(name, "/")
	if len(parts) < 3 || !policy.IsModule(parts[0]) {
		return result, errors.New("card path requires a declared module and zone")
	}
	for _, part := range parts {
		if len(part) > 255 {
			return result, errors.New("module path component exceeds 255 bytes")
		}
		if strings.HasPrefix(part, ".") || cardpath.IsExcludedDirectory(part) {
			return result, errors.New("module card path contains a hidden or excluded segment")
		}
	}
	zone := parts[1]
	if !policy.Workflow(zone) && !policy.Parked(zone) && !policy.IsKind(zone) && zone != "archive" && zone != "_archive" {
		return result, errors.New("unsupported module card zone")
	}
	filename := parts[len(parts)-1]
	if path.Ext(filename) != ".md" || cardpath.IsDocumentation(filename) {
		return result, errors.New("module card path requires a card filename")
	}
	for _, category := range parts[2 : len(parts)-1] {
		// Archive preserves the old workflow/kind path. Those segments are
		// provenance, not an executable zone or proof of completion.
		if zone == "archive" || zone == "_archive" {
			continue
		}
		if policy.Workflow(category) || policy.Parked(category) || policy.IsKind(category) || cardpath.WorkflowStatus(category) != "" || category == "archive" || category == "_archive" {
			return result, errors.New("ambiguous module category and zone")
		}
	}
	return modulePath{Module: parts[0], Zone: zone, Category: append([]string(nil), parts[2:len(parts)-1]...), Filename: filename}, nil
}

func (p modulePath) inZone(zone string) string {
	parts := []string{p.Module, zone}
	parts = append(parts, p.Category...)
	parts = append(parts, p.Filename)
	return strings.Join(parts, "/")
}

func transitionTargetPath(source, from, to string, policy boardpolicy.Policy) (string, error) {
	root, _, _ := strings.Cut(source, "/")
	if policy.IsModule(root) {
		classified, err := classifyModulePath(source, policy)
		if err != nil || classified.Zone != from {
			return "", errors.New("module source does not match transition zone")
		}
		target := classified.inZone(to)
		if _, err := classifyModulePath(target, policy); err != nil {
			return "", err
		}
		return target, nil
	}
	if filepath.Dir(source) != from {
		return "", errors.New("source card does not match transition")
	}
	return filepath.ToSlash(filepath.Join(to, filepath.Base(source))), nil
}
