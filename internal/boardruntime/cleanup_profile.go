package boardruntime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/receiptpath"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// CleanupCandidatesWithProfile lists finalized cards for one explicit
// section-11 profile. The unadopted list stays on CleanupCandidates.
func CleanupCandidatesWithProfile(ctx context.Context, root, source string, profile receiptpath.Profile) ([]CleanupEntry, error) {
	if !profile.Selected() {
		return nil, errors.New("receipt profile is not selected")
	}
	return cleanupCandidates(ctx, root, source, profile)
}

func adoptionClass(profile receiptpath.Profile, card *taskflow.Card) (include, skipReview bool) {
	if card == nil || !profile.Selected() {
		return finalizedCard(card), false
	}
	if adoptedIssuePath(card.TasksRel) {
		return strings.TrimSpace(card.Resolution) != "", true
	}
	if adoptedPlanPath(card.TasksRel) {
		if receiptpath.PlanStatusFinding(card.FMStatus) != "" || !receiptpath.PlanCleanupStatus(card.FMStatus) {
			return false, true
		}
		return true, true
	}
	return finalizedCard(card), false
}

func adoptedIssuePath(tasksRel string) bool {
	return kindPath(tasksRel, "issue")
}

func adoptedPlanPath(tasksRel string) bool {
	return kindPath(tasksRel, "plan")
}

func kindPath(tasksRel, kind string) bool {
	parts := strings.Split(filepath.ToSlash(tasksRel), "/")
	if len(parts) >= 2 && parts[0] == kind {
		return true
	}
	return len(parts) >= 3 && parts[0] == taskflow.StorageWriteDir && parts[1] == kind
}

func bindAdoptedDependencies(ctx context.Context, root, tasksDir, oid string, cards []*taskflow.Card, entries []CleanupEntry) []CleanupEntry {
	present := map[string]*taskflow.Card{}
	for _, card := range cards {
		if card != nil && card.ID != "" {
			present[card.ID] = card
		}
	}
	index := map[string]int{}
	for i := range entries {
		index[entries[i].ID] = i
	}
	for i := range entries {
		rel := strings.TrimPrefix(entries[i].Path, filepath.ToSlash(tasksDir)+"/")
		card := present[entries[i].ID]
		if card == nil {
			continue
		}
		var deps []string
		switch {
		case adoptedIssuePath(rel):
			deps = card.PromotedTo
		case adoptedPlanPath(rel):
			deps = card.Children
		default:
			continue
		}
		for _, dep := range deps {
			if reason := dependencyBlock(ctx, root, tasksDir, oid, dep, present, index, entries); reason != "" {
				entries[i].Blocked = append(entries[i].Blocked, reason)
				break
			}
		}
		entries[i].Removable = len(entries[i].Blocked) == 0
	}
	return entries
}

func dependencyBlock(ctx context.Context, root, tasksDir, oid, dep string, present map[string]*taskflow.Card, index map[string]int, entries []CleanupEntry) string {
	target := present[dep]
	if target == nil {
		if err := proveAbsentFinalized(ctx, root, tasksDir, oid, dep); err != nil {
			return err.Error()
		}
		return ""
	}
	if adoptedPlanPath(target.TasksRel) && strings.TrimSpace(target.FMStatus) == "cancelled" {
		return "cancelled child is not finalized: " + dep
	}
	if snapshotFinalized(target) {
		return ""
	}
	if at, ok := index[dep]; ok {
		if len(entries[at].Blocked) != 0 {
			return "depends on blocked " + dep
		}
		return "depends on earlier cleanup of " + dep
	}
	return "cannot prove finalized history for " + dep
}

func snapshotFinalized(card *taskflow.Card) bool {
	if card == nil {
		return false
	}
	if adoptedIssuePath(card.TasksRel) {
		return strings.TrimSpace(card.Resolution) != ""
	}
	if adoptedPlanPath(card.TasksRel) {
		return strings.TrimSpace(card.FMStatus) == "done" && receiptpath.PlanStatusFinding(card.FMStatus) == ""
	}
	return finalizedCard(card)
}

func proveAbsentFinalized(ctx context.Context, root, tasksDir, oid, id string) error {
	refused := fmt.Errorf("cannot prove finalized history for %s", id)
	raw, err := git(ctx, root, "rev-list", "--max-count=100", oid)
	if err != nil {
		return refused
	}
	for _, commit := range strings.Fields(string(raw)) {
		out, err := git(ctx, root, "grep", "-l", "-I", "-e", id, commit, "--", tasksDir)
		if err != nil {
			continue
		}
		var matched *taskflow.Card
		ambiguous := false
		for _, item := range strings.Fields(string(out)) {
			rel := item
			if cut := strings.IndexByte(item, ':'); cut >= 0 {
				rel = item[cut+1:]
			}
			blob, err := git(ctx, root, "show", commit+":"+rel)
			if err != nil {
				return refused
			}
			tasksRel := strings.TrimPrefix(filepath.ToSlash(rel), filepath.ToSlash(tasksDir)+"/")
			card := taskflow.ParseCard(tasksRel, blob)
			if card == nil || card.ID != id {
				continue
			}
			if matched != nil {
				ambiguous = true
				break
			}
			matched = card
		}
		if ambiguous || (matched != nil && !historicallyFinalized(matched)) {
			return refused
		}
		if matched != nil {
			return nil
		}
	}
	return refused
}

func historicallyFinalized(card *taskflow.Card) bool {
	if adoptedIssuePath(card.TasksRel) {
		return strings.TrimSpace(card.Resolution) != ""
	}
	if adoptedPlanPath(card.TasksRel) {
		return receiptpath.PlanStatusFinding(card.FMStatus) == "" && receiptpath.PlanCleanupStatus(card.FMStatus)
	}
	return finalizedCard(card)
}
