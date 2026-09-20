package taskstore

import (
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// PlanProgressRequest names the plan to count and carries the same archive
// admission rules that name the children field. The field name is declared,
// not fixed, so a census cannot be taken without it.
type PlanProgressRequest struct {
	ID    string `json:"id"`
	Rules []byte `json:"-"`
}

// ChildProgress is one declared child's completion as the shared census sees
// it. Complete means the card reached the done zone or left it through a
// verified archive completion. Present is not decoration: a child the board
// has never heard of counts as incomplete exactly like a real unfinished one,
// so without this flag a typo in the children list reads as outstanding work.
type ChildProgress struct {
	ID       string `json:"id"`
	Complete bool   `json:"complete"`
	Present  bool   `json:"present"`
}

// PlanProgress is a derived count, never a stored field. Nothing here is
// written back to the plan card: a consumer that needs the number in a file
// owns that write and owns reporting whether it succeeded. Coupling the count
// to a card move is what makes "moved but not counted" possible, and this
// query is deliberately not coupled to one.
type PlanProgress struct {
	ID       string          `json:"id"`
	Path     string          `json:"path"`
	Total    int             `json:"total"`
	Done     int             `json:"done"`
	Children []ChildProgress `json:"children"`
}

// ReadPlanProgress counts a plan's declared children against the board's
// shared completion index — the same verdict archive admission uses. Sharing
// the index is not enough to keep the two from disagreeing: archive admission
// only reads it after resumeArchive has settled every pending transition, so
// this query rejects pending transitions for the same reason List and Ready
// do. A card with an unsettled move has no zone to be counted in yet.
func ReadPlanProgress(dir string, req PlanProgressRequest) (result PlanProgress, err error) {
	if identityKey(req.ID) == "" {
		return result, errors.New("plan progress requires a canonical plan ID")
	}
	// ParseConfig ends in Canonical, which validates, so a config that parses
	// is already valid and every one of the five field names is present. A
	// Validate call or a mapping guard here would be a branch that never runs.
	cfg, err := archivepolicy.ParseConfig(req.Rules)
	if err != nil {
		return result, err
	}
	mapping := cfg.Admission.Fields.Mapping()
	session, err := openBoardSession(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return result, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return result, err
	}
	policy, err := policyForBoard(r)
	if err != nil {
		return result, err
	}
	var plan Entry
	for _, entry := range entries {
		if sameIdentity(entry.Card.ID, req.ID) {
			plan = entry
			break
		}
	}
	if plan.Card.ID == "" {
		return result, fmt.Errorf("plan %s not found on this board", req.ID)
	}
	if entryZone(plan.Path, policy) != "plan" {
		return result, fmt.Errorf("card %s is not a plan; it sits in zone %q", req.ID, entryZone(plan.Path, policy))
	}
	raw, err := readTransitionCard(r, plan.Path)
	if err != nil {
		return result, err
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return result, err
	}
	m, err := doc.ProjectArchiveMetadata(mapping)
	if err != nil {
		return result, err
	}
	if len(m.Children) == 0 {
		return result, fmt.Errorf("plan %s declares no children", req.ID)
	}
	completion, err := completionForBoard(r, entries, policy)
	if err != nil {
		return result, err
	}
	present := map[string]bool{}
	for _, entry := range entries {
		present[identityKey(entry.Card.ID)] = true
	}
	result = PlanProgress{ID: plan.Card.ID, Path: plan.Path, Children: make([]ChildProgress, 0, len(m.Children))}
	for _, ref := range m.Children {
		key := identityKey(ref)
		child := ChildProgress{ID: ref, Complete: completion.done(ref), Present: present[key]}
		if child.Complete {
			result.Done++
		}
		result.Children = append(result.Children, child)
	}
	result.Total = len(result.Children)
	return result, nil
}
