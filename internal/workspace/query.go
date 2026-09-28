package workspace

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

// Selector identifies one card or one registered context in a workspace.
// Repository, when set, restricts the lookup to that configured repository.
type Selector struct {
	Repository      string
	CardID          string
	ContextKind     string
	ContextID       string
	ContextRevision uint32
}

// Result is one workspace-qualified task-store projection. Paths within Entry
// and Context are relative to the selected repository's board.
type Result struct {
	Repository string                   `json:"repository"`
	Entry      *taskstore.Entry         `json:"entry,omitempty"`
	Context    *taskstore.ContextResult `json:"context,omitempty"`
}

type queryMode uint8

const (
	queryCard queryMode = iota + 1
	queryContext
)

// Query reads configured boards without changing them. It requires exactly one
// result: unscoped collisions are reported instead of choosing a repository.
func Query(manifest Manifest, selector Selector) (Result, error) {
	mode, cardKey, err := validateSelector(selector)
	if err != nil {
		return Result{}, err
	}
	repositories, err := selectedRepositories(manifest, selector.Repository)
	if err != nil {
		return Result{}, err
	}
	results := make([]Result, 0, 1)
	for _, repository := range repositories {
		var result Result
		var found bool
		if mode == queryCard {
			result, found, err = queryCardInRepository(repository, cardKey)
		} else {
			result, found, err = queryContextInRepository(repository, selector)
		}
		if err != nil {
			return Result{}, fmt.Errorf("query repository %q: %w", repository.Name, err)
		}
		if found {
			results = append(results, result)
		}
	}
	if len(results) == 0 {
		return Result{}, errors.New("workspace query found no matching result")
	}
	if len(results) > 1 {
		names := make([]string, len(results))
		for i, result := range results {
			names[i] = result.Repository
		}
		sort.Strings(names)
		return Result{}, fmt.Errorf("workspace query is ambiguous across repositories: %s", strings.Join(names, ", "))
	}
	return results[0], nil
}

func validateSelector(selector Selector) (queryMode, string, error) {
	hasContext := selector.ContextKind != "" || selector.ContextID != "" || selector.ContextRevision != 0
	if selector.CardID != "" {
		if hasContext {
			return 0, "", errors.New("workspace query selector must specify a card or a context, not both")
		}
		id, err := cardid.Parse(selector.CardID)
		if err != nil {
			return 0, "", fmt.Errorf("invalid card selector: %w", err)
		}
		return queryCard, id.Key(), nil
	}
	if !hasContext {
		return 0, "", errors.New("workspace query selector is empty")
	}
	if err := intentdoc.ValidateIdentity(selector.ContextKind, selector.ContextID, selector.ContextRevision); err != nil {
		return 0, "", fmt.Errorf("invalid context selector: %w", err)
	}
	return queryContext, "", nil
}

func selectedRepositories(manifest Manifest, name string) ([]Repository, error) {
	repositories := append([]Repository(nil), manifest.Repositories...)
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Name < repositories[j].Name })
	if name == "" {
		return repositories, nil
	}
	for _, repository := range repositories {
		if repository.Name == name {
			return []Repository{repository}, nil
		}
	}
	return nil, fmt.Errorf("workspace repository not found: %s", name)
}

func queryCardInRepository(repository Repository, key string) (Result, bool, error) {
	entries, err := taskstore.List(repository.Board)
	if err != nil {
		return Result{}, false, err
	}
	for _, entry := range entries {
		id, err := cardid.Parse(entry.Card.ID)
		if err != nil {
			return Result{}, false, fmt.Errorf("invalid listed card ID %q: %w", entry.Card.ID, err)
		}
		if id.Key() == key {
			entry := entry
			return Result{Repository: repository.Name, Entry: &entry}, true, nil
		}
	}
	return Result{}, false, nil
}

func queryContextInRepository(repository Repository, selector Selector) (Result, bool, error) {
	context, err := taskstore.ShowContext(repository.Board, selector.ContextKind, selector.ContextID, selector.ContextRevision)
	if err != nil {
		if errors.Is(err, taskstore.ErrContextNotFound) {
			return Result{}, false, nil
		}
		return Result{}, false, err
	}
	return Result{Repository: repository.Name, Context: &context}, true, nil
}
