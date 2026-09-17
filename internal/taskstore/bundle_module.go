package taskstore

import (
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

// Reproduce requested paths without using today's policy to reinterpret a
// completed receipt. Admission and pending recovery separately authorize scope.
func scopeBundleCard(prepared preparedCard, draft intentdoc.TaskDraft) (preparedCard, error) {
	if draft.Module == nil || *draft.Module == "" {
		return prepared, nil
	}
	zone, filename, ok := strings.Cut(prepared.Entry.Path, "/")
	if !ok || strings.Contains(filename, "/") {
		return preparedCard{}, errors.New("invalid prepared bundle path")
	}
	parts := []string{*draft.Module, zone}
	if draft.Category != nil && *draft.Category != "" {
		parts = append(parts, *draft.Category)
	}
	parts = append(parts, filename)
	dest := strings.Join(parts, "/")
	if len(dest) > 1023 {
		return preparedCard{}, errors.New("module bundle path exceeds 1023 bytes")
	}
	doc, err := card.Parse(prepared.Raw)
	if err != nil {
		return preparedCard{}, err
	}
	prepared.Entry = Entry{Path: dest, Card: doc.Snapshot(dest)}
	return prepared, nil
}

func validateBundleDestination(r *os.Root, name string, policy boardpolicy.Policy) error {
	root, _, _ := strings.Cut(name, "/")
	if root != "todo" || strings.Count(name, "/") != 1 {
		if _, err := classifyModulePath(name, policy); err != nil {
			return err
		}
	}
	if err := relocationParents(r, name, false); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
