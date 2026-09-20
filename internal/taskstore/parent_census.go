package taskstore

import (
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// PlanProgressWriter optionally rewrites a plan card's derived counters.
// Callers type-assert or pass nil; absence is a concrete skip, not an error.
type PlanProgressWriter interface {
	WritePlanProgress(planPath string, progress PlanProgress) (written bool, err error)
}

// ParentCensusResult reports what parent-plan reconciliation did after a child
// ended work. A skipped or failed census never implies the child move failed.
type ParentCensusResult struct {
	PlanPath  string `json:"planPath,omitempty"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Progress  int    `json:"progress"`
	Written   bool   `json:"written"`
	Skipped   string `json:"skipped,omitempty"`
}

// ChildMoveCensusResult keeps card-move success distinct from census write
// outcome so a caller can retry the census without undoing the move.
type ChildMoveCensusResult struct {
	CardOK bool               `json:"cardOk"`
	Census ParentCensusResult `json:"census"`
}

// Skip reasons for a reconciliation that did not write.
const (
	ParentCensusSkipNoParent    = "card declares no parent plan"
	ParentCensusSkipNoPlan      = "parent plan not found"
	ParentCensusSkipNoChildren  = "parent plan declares no children to count"
	ParentCensusSkipUnsupported = "storage cannot write plan progress"
)

// ParentCensusCard is the minimal board projection the census needs. Parent is
// read from the child before the move; Children come from the plan card's
// declared children list. Body-table decomposition is intentionally out of
// scope for this ID-only census.
type ParentCensusCard struct {
	Path     string
	ID       string
	Parent   string
	Children []string
	Zone     string
	Kind     string
	Archived bool
	Terminal bool
}

// ChildDestinationEndsWork reports whether a destination changes plan
// completion. Mid-queue moves (doing/review) must not trigger reconciliation.
func ChildDestinationEndsWork(zone string, archived, terminal bool) bool {
	if archived || terminal {
		return true
	}
	return zone == "done"
}

// ClassifyCensusPath derives zone/kind/archive flags from a board-relative
// path. Archive and kind directories are opaque: they do not contribute a
// workflow zone of their own.
func ClassifyCensusPath(path string) (zone, kind string, archived bool) {
	parts := strings.Split(strings.ReplaceAll(path, "\\", "/"), "/")
	if len(parts) > 0 {
		parts = parts[:len(parts)-1]
	}
	for _, part := range parts {
		if cardpath.IsStatusOpaqueZone(part) {
			switch strings.ToLower(part) {
			case "archive", "_archive":
				return "", "", true
			case "plan", "issue", "backlog":
				return "", strings.ToLower(part), false
			}
			return "", "", false
		}
		if status := cardpath.WorkflowStatus(part); status != "" {
			zone = status
		}
	}
	return zone, kind, false
}

// AfterSuccessfulChildMove runs the optional parent census after a child card
// move already succeeded. Census read/write failure leaves CardOK true.
func AfterSuccessfulChildMove(parent string, cards []ParentCensusCard, writer PlanProgressWriter) ChildMoveCensusResult {
	return ChildMoveCensusResult{
		CardOK: true,
		Census: ReconcileParentPlan(parent, cards, writer),
	}
}

// ReconcileParentPlan recomputes a parent plan's derived counters from the
// current locations of its declared children. Writer may be nil. This is an
// additional write, not an atomic transaction with the child move.
func ReconcileParentPlan(parent string, cards []ParentCensusCard, writer PlanProgressWriter) ParentCensusResult {
	parent = strings.TrimSpace(parent)
	if parent == "" {
		return ParentCensusResult{Skipped: ParentCensusSkipNoParent}
	}
	if writer == nil {
		return ParentCensusResult{Skipped: ParentCensusSkipUnsupported}
	}

	index := indexCensusCards(cards)
	var plan *ParentCensusCard
	for i := range cards {
		c := &cards[i]
		if c.Kind != "plan" {
			continue
		}
		if planMatchesCensus(*c, parent) {
			plan = c
			break
		}
	}
	if plan == nil {
		return ParentCensusResult{Skipped: ParentCensusSkipNoPlan}
	}
	if len(plan.Children) == 0 {
		return ParentCensusResult{PlanPath: plan.Path, Skipped: ParentCensusSkipNoChildren}
	}

	total := 0
	completed := 0
	for _, childID := range plan.Children {
		total++
		ref, ok := lookupCensusChild(childID, index)
		if !ok {
			continue
		}
		if censusChildComplete(ref) {
			completed++
		}
	}
	progress := NewPlanProgress(total, completed)
	update := ParentCensusResult{
		PlanPath:  plan.Path,
		Total:     progress.Total,
		Completed: progress.Completed,
		Progress:  progress.Progress,
	}
	written, err := writer.WritePlanProgress(plan.Path, progress)
	if err != nil {
		update.Written = false
		update.Skipped = err.Error()
		return update
	}
	update.Written = written
	return update
}

func censusChildComplete(c ParentCensusCard) bool {
	if c.Archived || c.Terminal {
		return true
	}
	return c.Zone == "done"
}

func planMatchesCensus(plan ParentCensusCard, parent string) bool {
	if plan.ID != "" && sameIdentity(plan.ID, parent) {
		return true
	}
	return plan.ID != "" && plan.ID == parent
}

func indexCensusCards(cards []ParentCensusCard) map[string]ParentCensusCard {
	index := make(map[string]ParentCensusCard, len(cards))
	for _, c := range cards {
		if c.Kind == "plan" || c.ID == "" {
			continue
		}
		key := identityKey(c.ID)
		if key == "" {
			key = c.ID
		}
		index[key] = c
		index[c.ID] = c
	}
	return index
}

func lookupCensusChild(childID string, index map[string]ParentCensusCard) (ParentCensusCard, bool) {
	if c, ok := index[childID]; ok {
		return c, true
	}
	if key := identityKey(childID); key != "" {
		if c, ok := index[key]; ok {
			return c, true
		}
	}
	return ParentCensusCard{}, false
}
