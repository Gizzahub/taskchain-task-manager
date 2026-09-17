package taskstore

import (
	"fmt"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// completionIndex is the single structural dependency verdict. Its archive
// inputs must come from a scope-validated completed journal in a locked board
// session. A receipt alone never inserts a missing card into the graph.
type completionIndex map[string]bool

func (c completionIndex) done(id string) bool { return c[identityKey(id)] }

// completionForJournal couples scope and pending checks to the index. A nil
// journal means legacy workflow-only semantics, not an adopted journal that
// went missing; the storage loader must reject that missing-marker case.
func completionForJournal(entries []Entry, policy boardpolicy.Policy, journal *archiveJournal, board, namespace string, read func(Entry) ([]byte, error)) (completionIndex, error) {
	var bindings map[string]archiveCompletionBinding
	if journal != nil {
		var err error
		bindings, err = archiveCompletedBindings(*journal, board, namespace)
		if err != nil {
			return nil, err
		}
	}
	return buildCompletionIndex(entries, policy, bindings, board, read)
}

func buildCompletionIndex(entries []Entry, policy boardpolicy.Policy, bindings map[string]archiveCompletionBinding, board string, read func(Entry) ([]byte, error)) (completionIndex, error) {
	if err := validateGraph(entries); err != nil {
		return nil, err
	}
	byID := map[string]Entry{}
	out := completionIndex{}
	for _, entry := range entries {
		key := identityKey(entry.Card.ID)
		byID[key] = entry
		out[key] = entryInWorkflowZone(entry, policy.DoneZone(), policy)
	}
	keys := make([]string, 0, len(bindings))
	for key := range bindings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		b := bindings[key]
		if b.Identity != key || b.BoardPath != board {
			return nil, fmt.Errorf("archive completion index identity mismatch")
		}
		if err := validateArchiveCompletion(b); err != nil {
			return nil, err
		}
		entry, exists := byID[key]
		if !exists {
			continue
		} // A dependent's missing reference was rejected above.
		if entry.Card.ID != b.ID {
			return nil, fmt.Errorf("archive completion snapshot identity mismatch")
		}
		if read == nil {
			return nil, fmt.Errorf("archive completion requires current card bytes")
		}
		raw, err := read(entry)
		if err != nil {
			return nil, fmt.Errorf("read archive completion card %s: %w", entry.Path, err)
		}
		if err := verifyArchiveCompletion(b, board, entry.Path, raw); err != nil {
			return nil, err
		}
		out[key] = true
	}
	return out, nil
}
