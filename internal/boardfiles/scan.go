// Package boardfiles classifies files below an opted-in task board.
//
// It is deliberately reader-only. Policy activation and every writer remain
// responsible for binding a policy authority to this scanner.
package boardfiles

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardstate"
)

// Finding is one file-tree shape which is not assigned a declared role.
// Path is relative to the task directory and always uses slash separators.
type Finding struct {
	Path   string
	Detail string
}

// Result is a read-only file-role classification. Disabled is explicit so a
// caller cannot mistake an old schema's no-op for a clean opted-in board.
type Result struct {
	Enabled  bool
	Findings []Finding
}

// Scan classifies every filesystem entry under tasksDir. It does not read card
// content and does not execute or modify anything. root exists to keep the
// public signature aligned with callers that already resolve a repository;
// tasksDir may be absolute or repository-relative.
func Scan(root, tasksDir string, policy boardpolicy.Policy) (result Result, err error) {
	if !policy.FileRolesEnabled() {
		return Result{Enabled: false, Findings: []Finding{}}, nil
	}
	base := tasksDir
	if !filepath.IsAbs(base) {
		base = filepath.Join(root, base)
	}
	info, err := os.Lstat(base)
	if err != nil {
		return Result{}, fmt.Errorf("lstat tasks directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return Result{}, fmt.Errorf("tasks directory is not a non-symlink directory")
	}

	r, err := os.OpenRoot(base)
	if err != nil {
		return Result{}, fmt.Errorf("open tasks directory: %w", err)
	}
	defer func() { err = errors.Join(err, r.Close()) }()
	opened, err := r.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return Result{}, errors.New("tasks directory changed while opening")
	}
	return ScanRoot(r, policy)
}

// ScanRoot scans a caller-owned pinned board handle. Writers call it under
// their existing mutation lock; it never opens a second path-based board root.
func ScanRoot(r *os.Root, policy boardpolicy.Policy) (Result, error) {
	if !policy.FileRolesEnabled() {
		return Result{Enabled: false, Findings: []Finding{}}, nil
	}

	result := Result{Enabled: true, Findings: []Finding{}}
	seen := map[string]bool{}
	v5 := policy.FileRolesV5() != nil
	var live []string
	err := fs.WalkDir(r.FS(), ".", func(rel string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("read %s: %w", rel, walkErr)
		}
		if rel == "." {
			return nil
		}
		seen[rel] = true
		if entry.Type()&os.ModeSymlink != 0 {
			result.Findings = append(result.Findings, Finding{Path: rel, Detail: "symlinks are not permitted below an opted-in task board"})
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() && !entry.IsDir() {
			result.Findings = append(result.Findings, Finding{Path: rel, Detail: "special files are not permitted below an opted-in task board"})
			return nil
		}
		if entry.IsDir() {
			if privateDir(rel, policy) || boardstate.IsDirectory(rel) {
				return filepath.SkipDir
			}
			if allowedDirectory(rel, policy) {
				return nil
			}
			result.Findings = append(result.Findings, Finding{Path: rel, Detail: "directory has no declared task-board file role"})
			return filepath.SkipDir
		}
		if v5 && policy.HasFileRole(rel) {
			return nil // Content is checked only after the complete structural pass.
		}
		if role, ok := policy.FileRole(rel); ok {
			issue, issueErr := declaredRoleIssue(r, rel, role)
			if issueErr != nil {
				return issueErr
			}
			if issue != "" {
				result.Findings = append(result.Findings, Finding{Path: rel, Detail: issue})
			}
			return nil
		}
		if boardstate.IsFile(rel) {
			return nil
		}
		if reservedNonCardName(entry.Name()) {
			result.Findings = append(result.Findings, Finding{Path: rel, Detail: "reserved non-card filename requires an exact declared root role"})
			return nil
		}
		if cardPath(rel, policy) {
			if strings.EqualFold(filepath.Ext(rel), ".md") {
				if v5 && !storage(strings.Split(rel, "/")[0]) {
					if issue := projectionPathIssue(rel); issue != "" {
						result.Findings = append(result.Findings, Finding{Path: rel, Detail: issue})
					} else {
						live = append(live, rel)
					}
				}
				return nil
			}
			result.Findings = append(result.Findings, Finding{Path: rel, Detail: "card locations permit Markdown cards only"})
			return nil
		}
		result.Findings = append(result.Findings, Finding{Path: rel, Detail: "file has no declared task-board role"})
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	for _, role := range policy.FileRoles() {
		if !seen[role.Path] {
			result.Findings = append(result.Findings, Finding{Path: role.Path, Detail: "declared file role is missing"})
		}
	}
	for _, role := range policy.FileRolesV5() {
		if !seen[role.Path] {
			result.Findings = append(result.Findings, Finding{Path: role.Path, Detail: "declared file role is missing"})
		}
	}
	// Unknown directories are pruned during traversal. Never compare a
	// projection with the incomplete inventory of a structurally invalid tree.
	if v5 && len(result.Findings) == 0 {
		sort.Strings(live)
		for _, role := range policy.FileRolesV5() {
			issue, err := declaredRoleV5Issue(r, role, live)
			if err != nil {
				return Result{}, err
			}
			if issue != "" {
				result.Findings = append(result.Findings, Finding{Path: role.Path, Detail: issue})
			}
		}
	}
	sort.Slice(result.Findings, func(i, j int) bool {
		if result.Findings[i].Path == result.Findings[j].Path {
			return result.Findings[i].Detail < result.Findings[j].Detail
		}
		return result.Findings[i].Path < result.Findings[j].Path
	})
	return result, nil
}

// privateDir names the existing reader exclusions. The scanner deliberately
// does not inspect their contents, but still rejects a symlink standing in for
// the directory before it gets here.
func privateDir(rel string, policy boardpolicy.Policy) bool {
	parts := strings.Split(rel, "/")
	for index, part := range parts {
		if part == ".ce" || part == "evidence" {
			return index == 0 || allowedDirectory(strings.Join(parts[:index], "/"), policy)
		}
	}
	return false
}

func allowedDirectory(rel string, policy boardpolicy.Policy) bool {
	parts := strings.Split(rel, "/")
	if len(parts) == 0 || parts[0] == "" {
		return false
	}
	if policy.IsModule(parts[0]) {
		return len(parts) == 1 || knownZone(parts[1], policy)
	}
	if knownZone(parts[0], policy) {
		return len(parts) == 1
	}
	if storage(parts[0]) {
		return len(parts) == 1 || (len(parts) == 2 && storagePartition(parts[1], policy))
	}
	return false
}

func cardPath(rel string, policy boardpolicy.Policy) bool {
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return false
	}
	if policy.IsModule(parts[0]) {
		return len(parts) >= 3 && knownZone(parts[1], policy)
	}
	if knownZone(parts[0], policy) {
		return len(parts) == 2
	}
	return storage(parts[0]) && len(parts) == 3 && storagePartition(parts[1], policy)
}

func knownZone(name string, policy boardpolicy.Policy) bool {
	return policy.Workflow(name) || policy.Parked(name) || policy.IsKind(name)
}

func storage(name string) bool { return name == "archive" || name == "_archive" }

func storagePartition(name string, policy boardpolicy.Policy) bool {
	if knownZone(name, policy) {
		return true
	}
	if len(name) != 7 || name[4] != '-' {
		return false
	}
	for index, char := range name {
		if index == 4 {
			continue
		}
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func reservedNonCardName(name string) bool {
	switch strings.ToLower(name) {
	case "readme.md", "index.md", "template.md", "agents.md", "claude.md":
		return true
	}
	return false
}

func declaredRoleIssue(r *os.Root, rel string, role boardpolicy.FileRole) (string, error) {
	if !strings.EqualFold(filepath.Ext(rel), ".md") {
		return "declared file role requires a Markdown regular file", nil
	}
	switch role.Kind {
	case boardpolicy.FileRoleProjection:
		if role.Owner == "" || role.Revision == "" || role.Marker == "" {
			return "projection declaration is incomplete", nil
		}
		if len(role.Marker) > 64<<10 {
			return "projection marker exceeds 65536 bytes", nil
		}
		before, err := r.Lstat(rel)
		if err != nil {
			return "", fmt.Errorf("inspect declared projection %s: %w", rel, err)
		}
		if !before.Mode().IsRegular() {
			return "", fmt.Errorf("declared projection %s changed to a non-regular file", rel)
		}
		file, err := openProjection(r, rel)
		if err != nil {
			return "", fmt.Errorf("open declared projection %s: %w", rel, err)
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
			return "", errors.Join(fmt.Errorf("declared projection %s changed while opening", rel), statErr, file.Close())
		}
		data, readErr := io.ReadAll(io.LimitReader(file, int64(len(role.Marker)+2)))
		closeErr := file.Close()
		if readErr != nil {
			return "", fmt.Errorf("read declared projection %s: %w", rel, errors.Join(readErr, closeErr))
		}
		if closeErr != nil {
			return "", fmt.Errorf("close declared projection %s: %w", rel, closeErr)
		}
		line := string(data)
		if index := strings.IndexByte(line, '\n'); index >= 0 {
			line = line[:index]
		}
		line = strings.TrimSuffix(line, "\r")
		if line != role.Marker {
			return "projection first line does not match its declared marker", nil
		}
	case boardpolicy.FileRoleGuidance:
	case boardpolicy.FileRoleTemplate:
	default:
		return "declared file role is unsupported", nil
	}
	return "", nil
}
