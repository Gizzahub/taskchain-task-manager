package taskflow

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/receiptpath"
)

// skipReceiptTree reports a board-relative directory whose role is receipts.
// Any path segment named receipts is that directory, including todo/receipts
// and done/receipts. A file named receipts.md is not this directory.
func skipReceiptTree(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, part := range strings.Split(rel, "/") {
		if part == "receipts" {
			return true
		}
	}
	return false
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
