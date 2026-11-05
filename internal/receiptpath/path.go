// Package receiptpath is the explicit section-11 receipt path and cleanup
// ownership judgment. A zero profile is not a selection. Callers that have
// not selected a profile keep the existing writer and cleanup bytes.
package receiptpath

import (
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

// Profile is an explicit revision-5 selection. Base is "section-9" or
// "section-10". Amendment is "section-11". Codec schema versions are not a
// selection.
type Profile struct {
	Base      string
	Amendment string
}

// Selected reports whether this profile is a section-11 receipt profile.
func (p Profile) Selected() bool {
	switch p.Base {
	case "section-9", "section-10":
		return p.Amendment == "section-11"
	default:
		return false
	}
}

// ReviewDir is the directory a new quality-review receipt must use.
// Section 9 is board-relative. Section 10 is repository-relative.
func ReviewDir(p Profile, id string) (string, error) {
	if err := selectedID(p, id); err != nil {
		return "", err
	}
	switch p.Base {
	case "section-9":
		return "receipts/" + id, nil
	case "section-10":
		return "docs/task-reviews/" + id, nil
	default:
		return "", errors.New("receipt profile is not selected")
	}
}

// AdmitReviewPath refuses a new receipt write outside the selected profile
// root. The check is path shape only. The caller still has to open the file
// with a no-follow regular reader.
func AdmitReviewPath(p Profile, id, rel string) error {
	dir, err := ReviewDir(p, id)
	if err != nil {
		return err
	}
	if !validRelative(rel) || strings.HasSuffix(rel, "/") {
		return errors.New("receipt path must be a relative POSIX file")
	}
	if path.Dir(rel) != dir {
		return errors.New("receipt path is outside the selected profile root")
	}
	return nil
}

// Warning is a read-only validate legacy-location warning. It does not by
// itself change validate's exit code.
type Warning struct {
	Path     string
	Cause    string
	Contract string
	Action   string
}

// LegacyReviewWarning reports a quality-review-receipt value that points at
// the selected profile's previous location.
func LegacyReviewWarning(p Profile, id, value string) (Warning, bool, error) {
	if err := selectedID(p, id); err != nil {
		return Warning{}, false, err
	}
	if !validRelative(value) {
		return Warning{}, false, nil
	}
	switch p.Base {
	case "section-9":
		flat := "docs/task-reviews/" + id + ".md"
		dir := "docs/task-reviews/" + id
		if value == flat || value == dir || strings.HasPrefix(value, dir+"/") {
			return Warning{
				Path:     value,
				Cause:    "quality-review-receipt points at the previous review location",
				Contract: "section-9 new receipts live under receipts/" + id + "/",
				Action:   "keep the legacy file read-only or admit a new file under receipts/" + id + "/",
			}, true, nil
		}
	case "section-10":
		if under(value, "receipts/"+id) || under(value, "tasks/receipts/"+id) {
			return Warning{
				Path:     value,
				Cause:    "quality-review-receipt points at the previous in-board receipt location",
				Contract: "section-10 new receipts live under docs/task-reviews/" + id + "/",
				Action:   "do not write the in-board receipt; admit a file under docs/task-reviews/" + id + "/",
			}, true, nil
		}
	}
	return Warning{}, false, nil
}

// OwnershipDirs returns the repository-relative cleanup roots for one card.
// Section 9 keeps the closed list: the in-board receipt anchor and
// docs/task-reviews/<ID>/. Section 10 drops the in-board receipt root.
// The explicitly bound flat docs/task-reviews/<ID>.md exception stays in the
// cleanup walker; this list does not add another ownership root.
func OwnershipDirs(p Profile, tasksDir, id string) ([]string, error) {
	if err := selectedID(p, id); err != nil {
		return nil, err
	}
	if !validRelative(tasksDir) {
		return nil, errors.New("tasks directory must be a relative path")
	}
	reviews := "docs/task-reviews/" + id
	switch p.Base {
	case "section-9":
		return []string{tasksDir + "/receipts/" + id, reviews}, nil
	case "section-10":
		return []string{reviews}, nil
	default:
		return nil, errors.New("receipt profile is not selected")
	}
}

// PlanStatusFinding reports invalid-plan-status when a plan status is missing,
// manual, or outside the six existing status words.
func PlanStatusFinding(status string) string {
	switch strings.TrimSpace(status) {
	case "pending", "in-progress", "review", "blocked", "done", "cancelled":
		return ""
	default:
		return "invalid-plan-status"
	}
}

// PlanCleanupStatus reports whether a plan status can enter the extended
// finalized set. A cancelled plan may be cleaned itself. A cancelled child
// does not satisfy another plan's finalized dependency.
func PlanCleanupStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "done", "cancelled":
		return true
	default:
		return false
	}
}

// Scan is which orphan checks one judgment scope may run.
type Scan struct {
	TrackedSection9   bool
	WorktreeResidue   bool
	ExternalSection10 bool
	NamedAnchorOnly   bool
}

// OrphanScan is the section-11 scope table. It does not scan a tree.
func OrphanScan(p Profile, scope string) (Scan, error) {
	if !p.Selected() {
		return Scan{}, errors.New("receipt profile is not selected")
	}
	section9 := p.Base == "section-9"
	switch scope {
	case "all", "worktree":
		return Scan{
			TrackedSection9:   section9,
			WorktreeResidue:   section9,
			ExternalSection10: p.Base == "section-10",
		}, nil
	case "staged", "gate":
		return Scan{TrackedSection9: section9}, nil
	case "named":
		return Scan{TrackedSection9: section9, NamedAnchorOnly: true}, nil
	default:
		return Scan{}, errors.New("unknown receipt judgment scope")
	}
}

// Orphan is one card-anchor orphan classification. Exit 1 is a lint/gate
// structure finding. Exit 0 is a warning or an informational result. Exit 2
// means the diagnosis is unavailable.
type Orphan struct {
	Class string
	Exit  int
}

// ClassifyAnchorOrphan classifies one anchor against an already enumerated ID
// set. idKnown is false when the path cannot determine an ID. An enumeration
// I/O failure must be returned by the caller and must not be passed as an
// empty set.
func ClassifyAnchorOrphan(p Profile, scope, anchorID string, idKnown bool, enumerated map[string]bool, tracked bool) (Orphan, error) {
	scan, err := OrphanScan(p, scope)
	if err != nil {
		return Orphan{}, err
	}
	if !idKnown || anchorID == "" {
		return Orphan{Class: "unavailable", Exit: 2}, nil
	}
	if enumerated[anchorID] {
		return Orphan{Class: "bound"}, nil
	}
	switch p.Base {
	case "section-9":
		if !scan.TrackedSection9 {
			return Orphan{Class: "not-scanned"}, nil
		}
		if !tracked && scan.WorktreeResidue {
			return Orphan{Class: "residue"}, nil
		}
		if !tracked {
			return Orphan{Class: "not-scanned"}, nil
		}
		return Orphan{Class: "structure", Exit: 1}, nil
	case "section-10":
		if scope == "named" || scan.ExternalSection10 {
			return Orphan{Class: "informational"}, nil
		}
		return Orphan{Class: "not-scanned"}, nil
	default:
		return Orphan{}, errors.New("receipt profile is not selected")
	}
}

// InBoardReceiptFinding reports a receipts-tree shape problem for a selected
// profile. applies is false when the path is not an in-board receipt path.
// A well-formed section-9 card anchor returns applies true and an empty finding.
func InBoardReceiptFinding(p Profile, tasksRel string) (bool, string, error) {
	if !p.Selected() {
		return false, "", errors.New("receipt profile is not selected")
	}
	if !validRelative(tasksRel) {
		return false, "", nil
	}
	parts := strings.Split(tasksRel, "/")
	index := -1
	for i, part := range parts {
		if part == "receipts" {
			index = i
			break
		}
	}
	if index < 0 {
		return false, "", nil
	}
	if p.Base == "section-10" {
		return true, "in-board receipts are forbidden", nil
	}
	rest := parts[index+1:]
	if len(rest) != 2 {
		return true, "receipts path is not a card-anchor file", nil
	}
	if _, err := cardid.Parse(rest[0]); err != nil {
		return true, "receipts directory is not a card id", nil
	}
	return true, "", nil
}

// PreflightZone refuses an explicit decision zone on a selected profile.
// Manual known-but-not-runnable handling stays with the manual lifecycle.
func PreflightZone(p Profile, zone string) error {
	if !p.Selected() {
		return errors.New("receipt profile is not selected")
	}
	if strings.EqualFold(strings.TrimSpace(zone), "decision") {
		return errors.New("unsupported zone \"decision\"")
	}
	return nil
}

// ActiveManual returns the generation of an entry record that has no close
// record. Consumed and retired generations are not active.
func ActiveManual(names []string) (string, bool) {
	entries := map[string]bool{}
	closes := map[string]bool{}
	for _, name := range names {
		generation, kind, ok := manualRecord(path.Base(name))
		if !ok {
			continue
		}
		switch kind {
		case "entry":
			entries[generation] = true
		case "close":
			closes[generation] = true
		}
	}
	active := make([]string, 0, len(entries))
	for generation := range entries {
		if !closes[generation] {
			active = append(active, generation)
		}
	}
	if len(active) == 0 {
		return "", false
	}
	sort.Strings(active)
	return active[0], true
}

func manualRecord(base string) (string, string, bool) {
	const prefix = "manual-"
	if !strings.HasPrefix(base, prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(base, prefix)
	var kind string
	switch {
	case strings.HasSuffix(rest, ".entry.json"):
		kind = "entry"
		rest = strings.TrimSuffix(rest, ".entry.json")
	case strings.HasSuffix(rest, ".close.json"):
		kind = "close"
		rest = strings.TrimSuffix(rest, ".close.json")
	default:
		return "", "", false
	}
	if len(rest) != 32 {
		return "", "", false
	}
	for _, r := range rest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return "", "", false
		}
	}
	return rest, kind, true
}

func selectedID(p Profile, id string) error {
	if !p.Selected() {
		return errors.New("receipt profile is not selected")
	}
	if _, err := cardid.Parse(id); err != nil {
		return errors.New("card id is not canonical")
	}
	return nil
}

func validRelative(rel string) bool {
	if rel == "" || strings.Contains(rel, `\`) || strings.HasPrefix(rel, "/") {
		return false
	}
	if path.Clean(rel) != rel {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func under(value, dir string) bool {
	return value == dir || strings.HasPrefix(value, dir+"/")
}
