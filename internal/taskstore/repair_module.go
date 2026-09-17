package taskstore

import (
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// Repair derives status only from a workflow zone, never a kind or archive.
func repairPathStatus(name string, policy boardpolicy.Policy) (string, bool) {
	if isTopLevelWorkflowPath(name, policy) {
		return policy.Status(filepath.Dir(name))
	}
	scope, err := classifyModulePath(name, policy)
	if err != nil || !policy.Workflow(scope.Zone) {
		return "", false
	}
	return policy.Status(scope.Zone)
}
