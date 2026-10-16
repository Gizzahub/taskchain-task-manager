package taskstore

import (
	"fmt"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// admitBoardFiles rejects an opted-in board with unassigned filesystem
// entries. Callers hold the board mutation lock when admission protects a
// writer, so ScanRoot observes the same pinned root that later receives the
// publication. Legacy policies deliberately remain a no-op.
func admitBoardFiles(r *os.Root, policy boardpolicy.Policy) error {
	result, err := boardfiles.ScanRoot(r, policy)
	if err != nil {
		return fmt.Errorf("scan board file roles: %w", err)
	}
	if len(result.Findings) == 0 {
		return nil
	}
	first := result.Findings[0]
	return fmt.Errorf("board file-role admission rejected %d entry(s); %s: %s", len(result.Findings), first.Path, first.Detail)
}
