package boardfiles

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardstate"
)

type SnapshotIssueSeverity uint8

const (
	SnapshotIssueError SnapshotIssueSeverity = iota + 1
	SnapshotIssueResidue
)

// SnapshotFinding is an internal diagnostic, not a new CLI/JSON envelope.
type SnapshotFinding struct {
	Path     string
	Detail   string
	Severity SnapshotIssueSeverity
	Contract string
	Action   string
}

const cardsOnlyContract = "docs/file-roles.md#cards-only-snapshot-foundation"

// AssessCardsOnlySnapshot explicitly selects the inactive cards-only layout.
// It consumes only the owned snapshot, never opens the worktree, and does not
// establish adoption, a complete inventory, or physical filesystem safety.
// Policy supplies topology and optional README projection metadata; existing
// schema semantics and the legacy scanner remain unchanged.
func AssessCardsOnlySnapshot(s CardsOnlySnapshot, policy boardpolicy.Policy) ([]SnapshotFinding, error) {
	if s.entries == nil || !policy.Workflow(policy.ReadyZone()) {
		return nil, fmt.Errorf("cards-only assessment requires a constructed snapshot and policy")
	}
	if err := cardsOnlyPolicyIssue(policy); err != nil {
		return nil, err
	}
	findings := []SnapshotFinding{}
	pruned, hasCard := map[string]bool{}, map[string]bool{}
	occupied := map[string]bool{}
	dirs := []string{}
	live := []string{}
	trackedLive := []string{}
	blockedTracked := false
	add := func(entry SnapshotEntry, detail string, empty bool) {
		severity := SnapshotIssueError
		if s.source != SnapshotNonGit && (!entry.Tracked || empty) {
			severity = SnapshotIssueResidue
		}
		action := "Review the board layout and explicitly migrate non-card material outside the board."
		if empty {
			action = "Review empty directories; validation must not remove them."
		}
		findings = append(findings, SnapshotFinding{entry.Path, detail, severity, cardsOnlyContract, action})
		blockedTracked = blockedTracked || severity == SnapshotIssueError
	}
	for _, rel := range s.paths {
		entry := s.entries[rel]
		if belowPruned(rel, pruned) {
			continue
		}
		issue := ""
		switch {
		case entry.Missing:
			issue = "tracked path is missing from the worktree snapshot"
		case entry.Mode.IsRegular() && rel == "README.md":
			continue
		case cardsOnlyReservedName(path.Base(rel)):
			issue = "reserved non-card name; only exact root README.md is permitted"
		case forbiddenCardsOnlyComponent(path.Base(rel)):
			issue = "non-card directory component is not permitted in the board"
		case path.Base(rel) == ".ce" || legacyEngineName(path.Base(rel)) || boardstate.Classify(rel) != boardstate.Unknown ||
			(entry.Mode.IsDir() && boardstate.IsDirectory(path.Base(rel))):
			issue = "engine state must remain outside the board"
		case !entry.Mode.IsRegular() && !entry.Mode.IsDir():
			issue = "symlinks and special files are not permitted in the board"
		case entry.Mode.IsDir() && entry.indexLeaf:
			issue = "tracked file path is now a directory in the worktree snapshot"
		case entry.Mode.IsDir():
			if cardsOnlyDirectory(rel, policy) {
				dirs = append(dirs, rel)
				continue
			}
			issue = "directory has no cards-only board role"
		case cardsOnlyCardPath(rel, policy) && strings.EqualFold(path.Ext(rel), ".md"):
			if issue = projectionPathIssue(rel); issue == "" {
				for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
					hasCard[parent] = true
				}
				if !storageDirectoryPath(rel) {
					live = append(live, rel)
					if entry.Tracked {
						trackedLive = append(trackedLive, rel)
					}
				}
				continue
			}
		default:
			issue = "file has no cards-only board role"
		}
		add(entry, issue, false)
		if !entry.Missing {
			for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
				occupied[parent] = true
			}
		}
		if entry.Mode.IsDir() {
			pruned[rel] = true
		}
	}
	for _, rel := range dirs {
		entry := s.entries[rel]
		if !hasCard[rel] && !occupied[rel] && !(s.source == SnapshotWorktree && entry.inferred) {
			add(s.entries[rel], "directory contains no card candidates", true)
		}
	}
	// Never compare a projection with an incomplete or structurally invalid
	// supplied inventory. This is not proof that the caller supplied all paths.
	if !blockedTracked {
		for _, role := range policy.FileRolesV5() {
			entry, found := s.entries[role.Path]
			if !found {
				return nil, fmt.Errorf("declared README projection lacks a snapshot entry; represent tracked worktree deletion explicitly")
			}
			if !entry.Tracked && len(findings) != 0 {
				continue // Residue view is structurally incomplete.
			}
			expected := live
			if s.source == SnapshotWorktree && entry.Tracked {
				expected = trackedLive
			}
			if len(entry.Data) > maxProjectionBytes {
				add(entry, "README projection exceeds 1 MiB", false)
			} else if issue := projectionIssue(entry.Data, role, expected); issue != "" {
				add(entry, issue, false)
			}
		}
	}
	sortSnapshotFindings(findings)
	return findings, nil
}

func belowPruned(rel string, pruned map[string]bool) bool {
	for parent := path.Dir(rel); parent != "."; parent = path.Dir(parent) {
		if pruned[parent] {
			return true
		}
	}
	return false
}

func sortSnapshotFindings(findings []SnapshotFinding) {
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path == findings[j].Path {
			return findings[i].Detail < findings[j].Detail
		}
		return findings[i].Path < findings[j].Path
	})
}

func cardsOnlyPolicyIssue(policy boardpolicy.Policy) error {
	if _, err := policy.Canonical(); err != nil {
		return err
	}
	if policy.IsModule("manual") {
		return fmt.Errorf("manual workflow overlay conflicts with a declared module")
	}
	if status, present := policy.Status("manual"); present && status != "manual" {
		return fmt.Errorf("manual workflow overlay conflicts with declared status %q", status)
	}
	if len(policy.FileRoles()) > 0 {
		return fmt.Errorf("cards-only snapshot does not accept schema 4 role metadata")
	}
	for _, role := range policy.FileRolesV5() {
		if role.Kind != boardpolicy.FileRoleProjection || role.Path != "README.md" {
			return fmt.Errorf("cards-only snapshot permits only a root README projection")
		}
	}
	for _, dir := range policy.KnownDirs() {
		if policy.Parked(dir) && forbiddenCardsOnlyComponent(dir) {
			return fmt.Errorf("forbidden cards-only zone %q", dir)
		}
	}
	for _, module := range policy.Modules() {
		if forbiddenCardsOnlyComponent(module) || cardsOnlyReservedName(module) {
			return fmt.Errorf("forbidden cards-only module %q", module)
		}
	}
	return nil
}

func cardsOnlyReservedName(name string) bool {
	var lower strings.Builder
	for i := 0; i < len(name); i++ {
		b := name[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		lower.WriteByte(b)
	}
	switch lower.String() {
	case "readme.md", "index.md", "template.md", "agents.md", ".gitkeep":
		return true
	}
	return false
}

func forbiddenCardsOnlyComponent(name string) bool {
	switch name {
	case "evidence", "receipts", "decision", "decisions", "adr", "adrs", "reference", "templates":
		return true
	}
	return false
}

func cardsOnlyDirectory(rel string, policy boardpolicy.Policy) bool {
	parts := strings.Split(rel, "/")
	if policy.IsModule(parts[0]) && len(parts) > 1 && parts[1] == "manual" {
		return true
	}
	if rel == "manual" || ((parts[0] == "archive" || parts[0] == "_archive") && len(parts) == 2 && parts[1] == "manual") {
		return true
	}
	return allowedDirectory(rel, policy)
}

func cardsOnlyCardPath(rel string, policy boardpolicy.Policy) bool {
	parts := strings.Split(rel, "/")
	if (parts[0] == "manual" && len(parts) == 2) ||
		(policy.IsModule(parts[0]) && len(parts) >= 3 && parts[1] == "manual") ||
		(storage(parts[0]) && len(parts) == 3 && parts[1] == "manual") {
		return true
	}
	return cardPath(rel, policy)
}
