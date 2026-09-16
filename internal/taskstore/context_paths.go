package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

const contextDirectory = ".task-manager-context"

func contextKindDirectory(kind string) string {
	switch kind {
	case "batch":
		return "batches"
	case "iteration":
		return "iterations"
	case "intent":
		return "intents"
	default:
		return "" // Callers validate the identity before resolving paths.
	}
}

func contextPath(kind, id string, revision uint32) (string, error) {
	if err := intentdoc.ValidateIdentity(kind, id, revision); err != nil {
		return "", err
	}
	return path.Join(contextDirectory, contextKindDirectory(kind), id, fmt.Sprintf("%d.json", revision)), nil
}

func contextDirectories(r *os.Root, kind, id string, create bool) (bool, error) {
	if err := intentdoc.ValidateIdentity(kind, id, 1); err != nil {
		return false, err
	}
	name := ""
	for _, part := range []string{contextDirectory, contextKindDirectory(kind), id} {
		name = path.Join(name, part)
		info, err := r.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return false, nil
			}
			if err := r.Mkdir(name, 0o755); err != nil {
				return false, err
			}
			info, err = r.Lstat(name)
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("context path is not a real directory: %s", name)
		}
	}
	return true, nil
}

func readContext(r *os.Root, kind, id string, revision uint32) (intentdoc.Document, bool, error) {
	name, err := contextPath(kind, id, revision)
	if err != nil {
		return intentdoc.Document{}, false, err
	}
	ready, err := contextDirectories(r, kind, id, false)
	if err != nil || !ready {
		return intentdoc.Document{}, false, err
	}
	raw, err := boundedSnapshotFile(r, name, intentdoc.MaxDocumentBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return intentdoc.Document{}, false, nil
	}
	if err != nil {
		return intentdoc.Document{}, false, fmt.Errorf("read registered context: %w", err)
	}
	doc, err := intentdoc.Parse(raw)
	if err != nil {
		return intentdoc.Document{}, false, fmt.Errorf("corrupt registered context: %w", err)
	}
	if doc.Kind() != kind || doc.ID() != id || doc.Revision() != revision {
		return intentdoc.Document{}, false, errors.New("registered context identity does not match its path")
	}
	canonical, err := doc.Canonical()
	if err != nil {
		return intentdoc.Document{}, false, err
	}
	if !bytes.Equal(canonical, raw) {
		return intentdoc.Document{}, false, errors.New("registered context is not canonical; preserve the original file")
	}
	return doc, true, nil
}
