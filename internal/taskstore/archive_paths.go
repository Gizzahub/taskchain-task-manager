package taskstore

import (
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// archiveDestination inserts storage beside the declared source zone. Unlike
// replacing or searching path segments, this preserves every category and
// cannot reinterpret a category as a module. This is pure path preparation;
// admission, exact source identity, destination collision and locking belong
// to the archive writer.
func archiveDestination(source string, policy boardpolicy.Policy) (zone, target string, err error) {
	if !utf8.ValidString(source) || len(source) > 1023 || path.IsAbs(source) || path.Clean(source) != source || strings.ContainsAny(source, "\\\x00") {
		return "", "", fmt.Errorf("invalid archive source path")
	}
	parts := strings.Split(source, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("archive source requires an explicit zone")
	}
	for _, part := range parts {
		if len(part) > 255 || strings.HasPrefix(part, ".") || cardpath.IsExcludedDirectory(part) || part == "archive" || part == "_archive" {
			return "", "", fmt.Errorf("archive source contains storage, hidden or excluded segment")
		}
	}
	name := parts[len(parts)-1]
	if path.Ext(name) != ".md" || cardpath.IsDocumentation(name) {
		return "", "", fmt.Errorf("archive source must be a card")
	}
	index := 0
	if policy.IsModule(parts[0]) {
		if _, err := classifyModulePath(source, policy); err != nil {
			return "", "", err
		}
		index = 1
	}
	zone = parts[index]
	if !policy.Workflow(zone) && !policy.IsKind(zone) && !policy.Parked(zone) {
		return "", "", fmt.Errorf("archive source has no declared zone")
	}
	for _, category := range parts[index+1 : len(parts)-1] {
		if policy.Workflow(category) || policy.IsKind(category) || policy.Parked(category) || cardpath.WorkflowStatus(category) != "" {
			return "", "", fmt.Errorf("archive source has ambiguous category")
		}
	}
	out := append([]string(nil), parts[:index]...)
	out = append(out, "_archive")
	out = append(out, parts[index:]...)
	target = strings.Join(out, "/")
	if len(target) > 1023 {
		return "", "", fmt.Errorf("archive destination exceeds path limit")
	}
	if index == 1 {
		if _, err := classifyModulePath(target, policy); err != nil {
			return "", "", err
		}
	}
	return zone, target, nil
}

func validateArchivedBindingPath(name string, policy boardpolicy.Policy) error {
	if !utf8.ValidString(name) || len(name) > 1023 || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") {
		return fmt.Errorf("invalid archived binding path")
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return fmt.Errorf("archived binding requires a storage zone")
	}
	index := 0
	if policy.IsModule(parts[0]) {
		if _, err := classifyModulePath(name, policy); err != nil {
			return err
		}
		index = 1
	}
	if parts[index] != "archive" && parts[index] != "_archive" {
		return fmt.Errorf("binding target is not archived")
	}
	for i, part := range parts {
		if len(part) > 255 || strings.HasPrefix(part, ".") || cardpath.IsExcludedDirectory(part) {
			return fmt.Errorf("invalid archived binding path component")
		}
		if i != index && (part == "archive" || part == "_archive") {
			return fmt.Errorf("nested archive binding is ambiguous")
		}
	}
	name = parts[len(parts)-1]
	if path.Ext(name) != ".md" || cardpath.IsDocumentation(name) {
		return fmt.Errorf("archive binding requires a card filename")
	}
	return nil
}
