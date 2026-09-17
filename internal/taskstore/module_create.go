package taskstore

import (
	"errors"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// scopeCreatedCard preserves legacy rendering and validates the exact requested
// path, without cleaning away unsafe segments or reserving an ID.
func scopeCreatedCard(prepared preparedCard, req CreateRequest, policy boardpolicy.Policy) (preparedCard, error) {
	if req.Module == "" && req.Category == "" {
		return prepared, nil
	}
	if !policy.IsModule(req.Module) {
		return preparedCard{}, errors.New("creation requires an explicitly declared module")
	}
	zone, filename, ok := strings.Cut(prepared.Entry.Path, "/")
	if !ok || strings.Contains(filename, "/") {
		return preparedCard{}, errors.New("invalid prepared creation path")
	}
	parts := []string{req.Module, zone}
	if req.Category != "" {
		parts = append(parts, req.Category)
	}
	parts = append(parts, filename)
	dest := strings.Join(parts, "/")
	if _, err := classifyModulePath(dest, policy); err != nil {
		return preparedCard{}, err
	}
	doc, err := card.Parse(prepared.Raw)
	if err != nil {
		return preparedCard{}, err
	}
	prepared.Entry = Entry{Path: dest, Card: doc.Snapshot(dest)}
	return prepared, nil
}
