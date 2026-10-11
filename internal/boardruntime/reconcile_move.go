package boardruntime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// reconcileMove publishes one card into zone. applied is true only after the
// destination rename succeeds. Earlier failures put the original path back.
func reconcileMove(root string, card ReconcileCard, recoveryRoot, zone, heading string) (recovery string, applied bool, err error) {
	if card.execution == nil || card.receipt == nil {
		return "", false, fmt.Errorf("missing execution evidence")
	}
	status := taskflow.StatusBlocked
	if zone == "review" {
		status = taskflow.StatusReview
	}
	rewritten, ok := taskflow.RewriteStatusCellText(string(card.content), status)
	if !ok {
		return "", false, fmt.Errorf("no **Status** cell to rewrite")
	}
	dest := card.Destination
	if dest == "" {
		dest = replaceZone(card.Path, zone)
	}
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(dest))); statErr == nil {
		return "", false, fmt.Errorf("destination exists: %s", dest)
	}
	if err := os.MkdirAll(recoveryRoot, 0o755); err != nil {
		return "", false, err
	}
	recovery, err = os.MkdirTemp(recoveryRoot, "card-")
	if err != nil {
		return "", false, err
	}
	sourceFull := filepath.Join(root, filepath.FromSlash(card.Path))
	journal := map[string]any{
		"source": card.Path, "destination": dest,
		"execution": *card.execution, "receipt": *card.receipt,
	}
	raw, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return recovery, false, err
	}
	if err := os.WriteFile(filepath.Join(recovery, "journal.json"), append(raw, '\n'), 0o644); err != nil {
		return recovery, false, err
	}
	if err := os.Link(sourceFull, filepath.Join(recovery, "original")); err != nil {
		return recovery, false, fmt.Errorf("hard link original: %w", err)
	}
	claimed := filepath.Join(recovery, "claimed")
	if _, statErr := os.Lstat(claimed); statErr == nil {
		return recovery, false, fmt.Errorf("claimed path exists")
	}
	if err := os.Rename(sourceFull, claimed); err != nil {
		return recovery, false, err
	}
	restore := func(cause error) (string, bool, error) {
		if _, statErr := os.Lstat(sourceFull); statErr == nil {
			return recovery, false, fmt.Errorf("%w; original path occupied", cause)
		}
		if renErr := os.Rename(claimed, sourceFull); renErr != nil {
			return recovery, false, fmt.Errorf("%w; restore: %v", cause, renErr)
		}
		return recovery, false, cause
	}
	evidence, err := json.MarshalIndent(map[string]any{
		"execution": *card.execution, "receipt": *card.receipt, "recovery": recovery,
	}, "", "  ")
	if err != nil {
		return restore(err)
	}
	updated := append([]byte(rewritten), []byte("\n\n## "+heading+"\n\n```json\n"+string(evidence)+"\n```\n")...)
	if err := physicalParents(root, dest); err != nil {
		return restore(err)
	}
	published := filepath.Join(recovery, "published")
	file, err := os.OpenFile(published, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return restore(err)
	}
	_, writeErr := file.Write(updated)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return restore(fmt.Errorf("write published: %v %v", writeErr, closeErr))
	}
	claimedInfo, err := os.ReadFile(claimed)
	if err != nil || !bytesEqual(claimedInfo, card.content) {
		return restore(fmt.Errorf("claimed bytes changed"))
	}
	if _, statErr := os.Lstat(sourceFull); statErr == nil {
		return restore(fmt.Errorf("source recreated during reconcile"))
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, filepath.FromSlash(dest))), 0o755); err != nil {
		return restore(err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, filepath.FromSlash(dest))); statErr == nil {
		return restore(fmt.Errorf("destination exists: %s", dest))
	}
	if err := os.Rename(published, filepath.Join(root, filepath.FromSlash(dest))); err != nil {
		return restore(fmt.Errorf("publish without replacement: %w", err))
	}
	return recovery, true, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func physicalParents(root, dest string) error {
	current := root
	parts := strings.Split(filepath.ToSlash(dest), "/")
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == ".." {
			return fmt.Errorf("unsafe destination: %s", dest)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("destination parent is a symlink: %s", dest)
		}
		if !info.IsDir() {
			return fmt.Errorf("destination parent is not a directory: %s", dest)
		}
	}
	return nil
}
