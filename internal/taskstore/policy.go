package taskstore

import (
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// currentPolicy is the built-in contract for legacy records and fixed initial
// card rendering. Board operations resolve their binding through policyForBoard.
func currentPolicy() boardpolicy.Policy { return boardpolicy.Default() }

func validZone(zone string) bool { return currentPolicy().Workflow(zone) }

func allowedEdge(from, to string) bool { return currentPolicy().Allows(from, to) }

// zoneStatusView resolves a card's status from the zone that holds it, never
// from an arbitrary path segment. A module path pins the zone at a fixed
// position and rejects workflow-looking categories outright; a top-level path
// has no such guard, so deriving from any matching segment reports the
// directory a card was archived or filed under as its current status. Where the
// zone carries no status of its own -- archive, the kind directories, a parked
// zone declared without one -- the frontmatter value is the only source there
// is, and it is returned unchanged rather than invented.
//
// Every caller resolves zone against the policy before arriving here, so a
// directory alias such as wip never reaches this function; policy.Status is the
// whole rule rather than the last step of a precedence chain.
func zoneStatusView(doc *card.Document, zone string, policy boardpolicy.Policy) card.View {
	view := doc.View()
	if status, ok := policy.Status(zone); ok {
		view.Status = status
	}
	return view
}

func entryInWorkflowZone(entry Entry, zone string, policy boardpolicy.Policy) bool {
	status, ok := policy.Status(zone)
	return ok && policy.Workflow(zone) && isWorkTask(entry.Card.ID) &&
		entryZone(entry.Path, policy) == zone && entry.Card.Status == status
}

// entryZone keeps legacy top-level eligibility while recognizing only declared
// module paths. Neither an archive suffix nor a kind status creates a workflow.
func entryZone(name string, policy boardpolicy.Policy) string {
	root, _, _ := strings.Cut(name, "/")
	if !policy.IsModule(root) {
		return filepath.Dir(name)
	}
	classified, err := classifyModulePath(name, policy)
	if err != nil {
		return ""
	}
	return classified.Zone
}
