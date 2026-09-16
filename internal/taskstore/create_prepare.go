package taskstore

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

type preparedCard struct {
	Entry Entry
	Raw   []byte
}

func createPrefix(req CreateRequest) (string, error) {
	prefix, err := cardid.PrefixForKind(req.Kind)
	if err != nil {
		return "", err
	}
	if req.ID != "" {
		parsed, err := cardid.Parse(req.ID)
		if err != nil {
			return "", err
		}
		if req.Kind != "" && prefix != parsed.Prefix {
			return "", errors.New("card kind does not match ID prefix")
		}
		prefix = parsed.Prefix
	}
	return prefix, nil
}

// prepareCreatedCard renders and validates without touching files or reserving
// an ID. Dependency graph validation belongs to the caller's whole proposal.
func prepareCreatedCard(id string, req CreateRequest) (preparedCard, error) {
	if len(id)+len(".md") > 255 {
		return preparedCard{}, errors.New("task filename exceeds 255 bytes")
	}
	parsed, err := cardid.Parse(id)
	if err != nil {
		return preparedCard{}, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return preparedCard{}, errors.New("task title is empty")
	}
	var raw []byte
	var doc *card.Document
	if req.Template != nil {
		if err := validateConfiguredRequest(req); err != nil {
			return preparedCard{}, err
		}
		raw, doc, err = validateConfiguredCard(id, req)
	} else {
		raw, err = renderWithDependencies(id, req.Title, req.DependsOn)
		if err == nil {
			doc, err = card.Parse(raw)
		}
	}
	if err != nil {
		return preparedCard{}, fmt.Errorf("parse created task: %w", err)
	}
	zone := strings.ToLower(parsed.Prefix)
	if parsed.Prefix == "TASK" {
		zone = currentPolicy().InitialZone()
	}
	dest := path.Join(zone, id+".md")
	return preparedCard{Entry: Entry{Path: dest, Card: doc.Snapshot(dest)}, Raw: raw}, nil
}
