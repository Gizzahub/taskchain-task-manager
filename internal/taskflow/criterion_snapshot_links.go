package taskflow

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// validateCriterionLinks resolves every committed symlink before any link is
// created. An absolute, empty, circular, or escaping target makes the base
// unavailable. A ".." that stays inside the snapshot is kept.
func validateCriterionLinks(entries []snapshotEntry) error {
	links := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.mode == "120000" {
			links[entry.path] = entry.link
		}
	}
	for link := range links {
		if _, err := resolveCriterionLink(link, links, map[string]bool{}); err != nil {
			return fmt.Errorf("criterion symlink %q: %w", link, err)
		}
	}
	return nil
}

func resolveCriterionLink(link string, links map[string]string, seen map[string]bool) (string, error) {
	if seen[link] {
		return "", fmt.Errorf("symlink cycle")
	}
	if len(seen) >= criterionSymlinkDepthLimit {
		return "", fmt.Errorf("symlink chain exceeds %d links", criterionSymlinkDepthLimit)
	}
	seen[link] = true
	defer delete(seen, link)
	target, ok := links[link]
	if !ok {
		return "", fmt.Errorf("symlink cycle")
	}
	if target == "" || path.IsAbs(target) || filepath.IsAbs(target) || strings.Contains(target, "\\") {
		return "", fmt.Errorf("absolute or empty target")
	}
	segments := criterionPathSegments(path.Dir(link))
	for _, segment := range strings.Split(target, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) == 0 {
				return "", fmt.Errorf("target %q escapes snapshot", target)
			}
			segments = segments[:len(segments)-1]
		default:
			candidate := path.Join(append(append([]string{}, segments...), segment)...)
			if _, chained := links[candidate]; chained {
				next, err := resolveCriterionLink(candidate, links, seen)
				if err != nil {
					return "", err
				}
				segments = criterionPathSegments(next)
				continue
			}
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return ".", nil
	}
	return path.Join(segments...), nil
}

func criterionPathSegments(name string) []string {
	if name == "." || name == "" {
		return nil
	}
	return strings.Split(name, "/")
}

func writeCriterionLinks(dir string, entries []snapshotEntry) error {
	for _, entry := range entries {
		if entry.mode != "120000" {
			continue
		}
		filename := filepath.Join(dir, filepath.FromSlash(entry.path))
		if !pathInside(dir, filename) {
			return fmt.Errorf("criterion snapshot has unsafe path %q", entry.path)
		}
		if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
			return fmt.Errorf("create criterion snapshot directory for symlink %q: %w", entry.path, err)
		}
		if err := os.Symlink(entry.link, filename); err != nil {
			return fmt.Errorf("write criterion snapshot symlink %q: %w", entry.path, err)
		}
	}
	return nil
}

// verifySnapshotLinks walks the links the filesystem will follow. Lexical
// validation compares Git's names; this pass refuses a link that still leaves
// the snapshot once it is written.
func verifySnapshotLinks(dir string, entries []snapshotEntry) error {
	for _, entry := range entries {
		if entry.mode != "120000" {
			continue
		}
		if !snapshotPathAllowed(dir, entry.path) {
			return fmt.Errorf("criterion symlink %q: target %q escapes snapshot", entry.path, entry.link)
		}
	}
	return nil
}
