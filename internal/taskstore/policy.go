package taskstore

import (
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// currentPolicy is the built-in contract for legacy records and fixed initial
// card rendering. Board operations resolve their binding through policyForBoard.
func currentPolicy() boardpolicy.Policy { return boardpolicy.Default() }

func validZone(zone string) bool { return currentPolicy().Workflow(zone) }

func allowedEdge(from, to string) bool { return currentPolicy().Allows(from, to) }

func entryInWorkflowZone(entry Entry, zone string, policy boardpolicy.Policy) bool {
	status, ok := policy.Status(zone)
	return ok && policy.Workflow(zone) && isWorkTask(entry.Card.ID) &&
		filepath.Dir(entry.Path) == zone && entry.Card.Status == status
}
