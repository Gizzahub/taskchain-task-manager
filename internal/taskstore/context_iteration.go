package taskstore

import (
	"fmt"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func validateIterationContextReferences(r *os.Root, doc intentdoc.Document) (string, error) {
	d, err := doc.IterationSnapshot()
	if err != nil {
		return "", err
	}
	intent, err := requiredContext(r, "intent", intentdoc.ContextRef(d.Intent))
	if err != nil {
		return "", err
	}
	var previous, batch *intentdoc.Document
	if d.Previous != nil {
		value, err := requiredContext(r, "iteration", *d.Previous)
		if err != nil {
			return "", err
		}
		previous = &value
	}
	if d.Batch != nil {
		value, err := requiredContext(r, "batch", *d.Batch)
		if err != nil {
			return "", err
		}
		batch = &value
	}
	if err := intentdoc.ValidateIterationReferences(doc, intent, previous, batch); err != nil {
		return "", err
	}
	return "verified", nil
}

func requiredContext(r *os.Root, kind string, ref intentdoc.ContextRef) (intentdoc.Document, error) {
	doc, found, err := readContext(r, kind, ref.ID, ref.Revision)
	if err != nil {
		return doc, err
	}
	if !found {
		return doc, fmt.Errorf("referenced %s revision is not registered", kind)
	}
	return doc, nil // Exact digest and semantic checks are shared with pure model.
}
