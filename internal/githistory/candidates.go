// Package githistory contains conservative, read-only Git candidate helpers.
package githistory

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// The line shape intentionally mirrors CE's domain frontmatterIDLine. It is
// line-anchored rather than a YAML parser so history scans remain tolerant of
// body examples and fenced snippets. CE source: internal/domain/task_id.go
// (MIT-licensed project source; predicate copied for public compatibility).
var idLine = regexp.MustCompile(`(?m)^id:[ \t]*(\S+)(?:[ \t]+#[^\n]*)?[ \t]*\r?$`)

// CandidateIDs returns normalized, unique canonical IDs found in every
// line-anchored id scalar. Invalid scalar values are ignored; overflow of a
// known prefix is an error because silently lowering an allocation floor is
// unsafe.
func CandidateIDs(raw []byte) ([]string, error) {
	set := map[string]bool{}
	for _, match := range idLine.FindAllSubmatch(raw, -1) {
		value := unquote(string(match[1]))
		id, err := cardid.Parse(value)
		if err != nil {
			prefix, digits, ok := strings.Cut(value, "-")
			if ok && (prefix == "TASK" || prefix == "PLAN" || prefix == "ISSUE" || prefix == "BACKLOG") && digits != "" && allDigits(digits) {
				if _, parseErr := strconv.ParseUint(digits, 10, 64); parseErr != nil {
					return nil, fmt.Errorf("%s ID overflow: %w", prefix, parseErr)
				}
			}
			continue
		}
		set[id.Key()] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func unquote(value string) string {
	if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}
	return value
}
func allDigits(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != ""
}

// IsCardPath applies CE's conservative non-card predicates below boardDir.
// CE source: internal/domain/task_zone.go (MIT-licensed project source).
func IsCardPath(file, boardDir string) bool {
	board := strings.TrimSuffix(boardDir, "/")
	if board == "" || file == board || !strings.HasPrefix(file, board+"/") || path.Ext(file) != ".md" {
		return false
	}
	rel := strings.TrimPrefix(file, board+"/")
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		if cardpath.IsExcludedDirectory(part) {
			return false
		}
	}
	base := parts[len(parts)-1]
	return !cardpath.IsDocumentation(base)
}
