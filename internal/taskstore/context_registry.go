package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

type ContextResult struct {
	SchemaVersion        int             `json:"schemaVersion"`
	Scope                string          `json:"scope"`
	Kind                 string          `json:"kind"`
	ID                   string          `json:"id"`
	Revision             uint32          `json:"revision"`
	Digest               string          `json:"digest"`
	Path                 string          `json:"path"`
	Status               string          `json:"status"`
	Registered           bool            `json:"registered"`
	ReferenceValidation  string          `json:"referenceValidation"`
	EvaluationValidation string          `json:"evaluationValidation"`
	Canonical            json.RawMessage `json:"canonical"`
}

func contextResult(doc intentdoc.Document, path, status, references string) (ContextResult, error) {
	canonical, err := doc.Canonical()
	if err != nil {
		return ContextResult{}, err
	}
	digest, err := doc.Digest()
	if err != nil {
		return ContextResult{}, err
	}
	return ContextResult{1, "board-context", doc.Kind(), doc.ID(), doc.Revision(), digest, path, status, true, references, "not_evaluated", canonical}, nil
}

// RegisterContext publishes exactly one immutable document. It creates no
// tasks and makes no claim about an evaluation's truth or actor authority.
func RegisterContext(dir string, raw []byte) (ContextResult, error) {
	return registerContextWithStep(dir, raw, nil)
}

func registerContextWithStep(dir string, raw []byte, step func(string) error) (result ContextResult, err error) {
	doc, err := intentdoc.Parse(raw)
	if err != nil {
		return result, err
	}
	canonical, err := doc.Canonical()
	if err != nil {
		return result, err
	}
	session, err := openBoardSession(dir)
	if err != nil {
		return result, err
	}
	registeredPath := ""
	defer func() {
		closeErr := session.close()
		if closeErr != nil && registeredPath != "" {
			closeErr = contextPublishedError(registeredPath, closeErr)
		}
		err = errors.Join(err, closeErr)
	}()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return result, err
	}
	path, err := contextPath(doc.Kind(), doc.ID(), doc.Revision())
	if err != nil {
		return result, err
	}
	existing, found, err := readContext(r, doc.Kind(), doc.ID(), doc.Revision())
	if err != nil {
		return result, err
	}
	if found {
		previous, err := existing.Canonical()
		if err != nil {
			return result, err
		}
		if !bytes.Equal(previous, canonical) {
			return result, errors.New("context key already contains different immutable content")
		}
		registeredPath = path
		return contextResult(existing, path, "unchanged", "not_rechecked")
	}
	references, err := validateContextReferences(r, doc)
	if err != nil {
		return result, err
	}
	if _, err := contextDirectories(r, doc.Kind(), doc.ID(), true); err != nil {
		return result, err
	}
	name, err := stage(r, canonical)
	if err != nil {
		return result, err
	}
	if step != nil {
		if err := step("after-stage"); err != nil {
			return result, errors.Join(err, r.Remove(name))
		}
	}
	if err := r.Link(name, path); err != nil {
		return result, errors.Join(err, r.Remove(name))
	}
	registeredPath = path
	// The no-overwrite link is the commit point. Never remove its destination
	// during cleanup, including when reporting a subsequent error.
	if step != nil {
		if err := step("after-link"); err != nil {
			return result, contextPublishedError(path, errors.Join(err, r.Remove(name)))
		}
	}
	if err := r.Remove(name); err != nil {
		return result, contextPublishedError(path, err)
	}
	if step != nil {
		if err := step("after-cleanup"); err != nil {
			return result, contextPublishedError(path, err)
		}
	}
	return contextResult(doc, path, "registered", references)
}

func contextPublishedError(path string, err error) error {
	return fmt.Errorf("document may already be registered at %s; retry the same key and content: %w", path, err)
}

func ShowContext(dir, kind, id string, revision uint32) (result ContextResult, err error) {
	path, err := contextPath(kind, id, revision)
	if err != nil {
		return result, err
	}
	session, err := openBoardSession(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	if err := rejectPendingTransitions(session.root); err != nil {
		return result, err
	}
	doc, found, err := readContext(session.root, kind, id, revision)
	if err != nil {
		return result, err
	}
	if !found {
		return result, fmt.Errorf("context document not found: %s", path)
	}
	return contextResult(doc, path, "stored", "not_rechecked")
}

func validateContextReferences(r *os.Root, doc intentdoc.Document) (string, error) {
	if doc.Kind() == "intent" {
		return "not_applicable", nil
	}
	raw, err := doc.Canonical()
	if err != nil {
		return "", err
	}
	var batch intentdoc.Batch
	if err := json.Unmarshal(raw, &batch); err != nil {
		return "", err
	}
	intent, found, err := readContext(r, "intent", batch.Intent.ID, batch.Intent.Revision)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("referenced intent revision is not registered")
	}
	digest, err := intent.Digest()
	if err != nil {
		return "", err
	}
	if digest != batch.Intent.Digest {
		return "", errors.New("referenced intent digest mismatch")
	}
	entries, err := listLocked(r)
	if err != nil {
		return "", err
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		if isWorkTask(entry.Card.ID) {
			ids[identityKey(entry.Card.ID)] = true
		}
	}
	for _, id := range batch.TaskIDs {
		if !ids[identityKey(id)] {
			return "", fmt.Errorf("referenced TASK is not present: %s", id)
		}
	}
	return "verified", nil
}
