package taskflow

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

// ActiveBoardPolicy resolves the policy from the board's journal-bound
// authority. It never treats a tracked policy file as permission by itself.
// A repository without a task directory retains the legacy empty-board read
// behaviour; a present board whose authority cannot be checked is an error.
func ActiveBoardPolicy(root string) (boardpolicy.Policy, error) {
	tasksDir, err := ResolveTasksDir()
	if err != nil {
		return boardpolicy.Policy{}, err
	}
	board := filepath.Join(root, tasksDir)
	if _, err := os.Lstat(board); errors.Is(err, fs.ErrNotExist) {
		return boardpolicy.Default(), nil
	} else if err != nil {
		return boardpolicy.Policy{}, fmt.Errorf("inspect task-board directory: %w", err)
	}
	policy, err := taskstore.ReadActivePolicy(board)
	if err != nil {
		return boardpolicy.Policy{}, fmt.Errorf("read active task-board policy: %w", err)
	}
	return policy, nil
}
