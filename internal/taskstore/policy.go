package taskstore

import (
	"path/filepath"
	"strings"

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
