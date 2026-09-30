package taskflow

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// --- canonical kinds -----------------------------------------------------------
// A document's kind travels with its ID (per the pinned reference's ADR-0003):
// the id prefix is the single source of truth, the kind-zone directory only
// fallback for ID-less documents. Workflow zones carry state, not kind.

type canonicalTaskKind string

const (
	canonicalTask    canonicalTaskKind = "task"
	canonicalPlan    canonicalTaskKind = "plan"
	canonicalIssue   canonicalTaskKind = "issue"
	canonicalBacklog canonicalTaskKind = "backlog"
)

var canonicalTaskIDPattern = regexp.MustCompile(`^(TASK|PLAN|ISSUE|BACKLOG)-(\d+)$`)

var taskIDPrefixes = map[string]canonicalTaskKind{
	"TASK": canonicalTask, "PLAN": canonicalPlan, "ISSUE": canonicalIssue, "BACKLOG": canonicalBacklog,
}

// parseCanonicalTaskID parses id as a canonical task ID and reports its kind
// and number. ok is false for anything that is not exactly PREFIX-NNN with
// PREFIX one of the four fixed canonical prefixes.
func parseCanonicalTaskID(id string) (canonicalTaskKind, int, bool) {
	m := canonicalTaskIDPattern.FindStringSubmatch(id)
	if m == nil {
		return "", 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, false
	}
	kind, ok := taskIDPrefixes[m[1]]
	if !ok {
		return "", 0, false
	}
	return kind, n, true
}

func canonicalKindFromID(id string) (canonicalTaskKind, bool) {
	kind, _, ok := parseCanonicalTaskID(id)
	return kind, ok
}

func canonicalKindFromPath(path string) (canonicalTaskKind, bool) {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		switch part {
		case "plan":
			return canonicalPlan, true
		case "issue":
			return canonicalIssue, true
		case "backlog":
			return canonicalBacklog, true
		case "todo", "doing", "review", "blocked", "done":
			return canonicalTask, true
		}
	}
	return "", false
}

// resolveCanonicalKind picks the schema to enforce. The id prefix wins; when
// a document sits in a kind zone that disagrees with its ID, that is genuine
// misfiling and gets a warning. A workflow zone never misfiles: moving an
// ISSUE-NNN through blocked/ and done/ is the intended arrangement.
func resolveCanonicalKind(path string, fields map[string]yaml.Node, result *ValidationResult) (canonicalTaskKind, bool) {
	zoneKind, zoneOK := canonicalKindFromPath(path)
	id, _ := scalarField(fields, "id")
	idKind, idOK := canonicalKindFromID(id)
	if !idOK {
		return zoneKind, zoneOK
	}
	if zoneOK && zoneKind != idKind && zoneKind != canonicalTask {
		result.Warnings = append(result.Warnings, Finding{"location",
			fmt.Sprintf("%s document filed under a %s zone", idKind, zoneKind)})
	}
	return idKind, true
}

// --- document parse ------------------------------------------------------------
// parseCanonicalDocument splits a card into frontmatter fields, body, and the
// three detection outcomes: no frontmatter fence at all (not detected — the
// card is skipped entirely), a fence that never closes (an error that stops
// the card), and a parsed block.

func parseCanonicalDocument(content string) (map[string]yaml.Node, string, bool, error) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content, false, nil
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "---" {
			continue
		}
		fields := make(map[string]yaml.Node)
		if err := yaml.Unmarshal([]byte(strings.Join(lines[1:index], "\n")), &fields); err != nil {
			return nil, "", true, err
		}
		return fields, strings.Join(lines[index+1:], "\n"), true, nil
	}
	return nil, "", true, fmt.Errorf("unterminated frontmatter")
}

// scalarField reads a scalar frontmatter value; ok is false only for an
// absent key, so a present non-scalar or null still reports present.
func scalarField(fields map[string]yaml.Node, name string) (string, bool) {
	node, ok := fields[name]
	if !ok || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return "", ok
	}
	return strings.TrimSpace(node.Value), true
}

// --- zones ---------------------------------------------------------------------

// kindDirFromPath returns the kind-zone segment of path (plan, issue,
// backlog), or "" when the card is not parked in one. The filename is never
// a zone.
func kindDirFromPath(path string) string {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if IsKindDir(part) {
			return part
		}
	}
	return ""
}

// storageDirFromPath returns the storage-directory segment of path, or "".
func storageDirFromPath(path string) string {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if IsStorageDir(part) {
			return part
		}
	}
	return ""
}

// statusZone returns the longest meaningful directory state for a card path.
// Storage wins over a nested workflow directory (archive preserves the source
// layout, but its terminal contract governs the subtree); then workflow
// zones; then the two kind zones that carry status claims of their own.
func statusZone(path string) (string, bool) {
	if storage := storageDirFromPath(path); storage != "" {
		return storage, true
	}
	if _, zone, ok := ZoneSegment(strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")); ok {
		return zone.Dir(), true
	}
	if kind := kindDirFromPath(path); kind == "issue" || kind == "backlog" {
		return kind, true
	}
	return "", false
}

// supersededTerminal is the second status an archive zone permits.
const supersededTerminal = "superseded"

func statusMatches(value string, expected Status) bool {
	actual, ok := StatusFromWord(value)
	return ok && actual == expected
}
