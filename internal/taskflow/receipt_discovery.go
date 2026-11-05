package taskflow

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/receiptpath"
)

// skipReceiptTree reports a board-relative path inside an in-board receipts
// directory. The exclusion is the directory role, not a file-name exception.
func skipReceiptTree(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "receipts" {
		return true
	}
	return strings.HasPrefix(rel, "receipts/") || strings.Contains(rel, "/receipts/")
}

// LoadLiveCardsAdopted reads non-storage cards and skips in-board receipts.
func LoadLiveCardsAdopted(root string, profile receiptpath.Profile) ([]*Card, error) {
	if !profile.Selected() {
		return nil, errors.New("receipt profile is not selected")
	}
	return loadLiveCards(root, true)
}

// ReexecCheckedBindingsAdopted re-executes checked bindings and does not
// re-enter in-board receipt files as cards.
func ReexecCheckedBindingsAdopted(ctx context.Context, tasksDir, workDir string, profile receiptpath.Profile) (ReexecReport, error) {
	if !profile.Selected() {
		return ReexecReport{}, errors.New("receipt profile is not selected")
	}
	return reexecCheckedBindings(ctx, tasksDir, workDir, true)
}
