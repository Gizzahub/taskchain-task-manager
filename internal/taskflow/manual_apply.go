package taskflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

// EnterManual moves one card from todo, doing, or blocked into manual and
// writes the entry record for an explicit section-11 profile. authority is
// the caller's board authority id. An empty authority, an unselected profile,
// or a refused plan leaves the tree unchanged.
func EnterManual(repoRoot, cardRel string, profile manualzone.Profile, state manualzone.RuntimeState, authority string) (string, error) {
	if authority == "" {
		return "", errors.New("board authority is required")
	}
	rel, fromZone, err := manualSource(cardRel)
	if err != nil {
		return "", err
	}
	full := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel))
	content, err := readContainedRegular(repoRoot, full)
	if os.IsNotExist(err) {
		return resumeManualEntry(repoRoot, rel, profile, state, authority)
	}
	if err != nil {
		return "", err
	}
	fields, err := manualzone.ParseCard(string(content))
	if err != nil {
		return "", err
	}
	_, entryRel, _, err := manualzone.PlanEntry(profile, fromZone, fields, state)
	if err != nil {
		return "", err
	}
	entryAbs, err := evidenceAbs(repoRoot, profile, entryRel)
	if err != nil {
		return "", err
	}
	if err := refuseSymlinkAncestors(repoRoot, entryAbs); err != nil {
		return "", err
	}
	destRel := destinationRel(rel, "manual")
	destFull := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(destRel))
	if _, err := os.Lstat(destFull); err == nil {
		return "", fmt.Errorf("destination already exists: %s", destRel)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	if err := refuseManualLinkEdits(repoRoot, rel, destRel); err != nil {
		return "", err
	}
	if _, ok := stampFrontmatterContent(content, [][2]string{{"status", "manual"}}); !ok {
		return "", errors.New("card frontmatter has no closing fence")
	}
	// The resume baseline is the pre-move card. It is staged before the move
	// so a later crash can recognize that card, the status stamp, and the
	// outbound retarget, and no other edit.
	if err := stageEntryBaseline(repoRoot, entryAbs, profile, authority, string(content), rel, destRel); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(destFull), 0o755); err != nil {
		return "", err
	}
	if err := moveFile(repoRoot, full, destFull); err != nil {
		return "", err
	}
	fail := func(cause error) (string, error) {
		rollbackMoved(repoRoot, full, destFull, content)
		return "", cause
	}
	if err := stampRequiredFrontmatter(destFull, [][2]string{{"status", "manual"}}); err != nil {
		return fail(err)
	}
	undo, err := commitManualLinks(repoRoot, rel, destRel)
	if err != nil {
		return fail(err)
	}
	settled, err := readContainedRegular(repoRoot, destFull)
	if err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	digest, err := CanonicalReviewDigest(string(settled))
	if err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	settledFields, err := manualzone.ParseCard(string(settled))
	if err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	record, err := manualzone.NewEntryRecord(profile, settledFields, authority, string(settled), digest)
	if err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	encoded, err := manualzone.Encode(record)
	if err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	key := entryRequestKey(settledFields.ID, record.EntryGeneration)
	if err := stageRequestPartial(repoRoot, entryAbs, key, encoded); err != nil {
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	if err := finalizeRequestPartial(repoRoot, entryAbs, key, encoded); err != nil {
		confirmed, readErr := readOptionalContained(repoRoot, entryAbs)
		if readErr == nil && bytes.Equal(confirmed, encoded) {
			return digest, nil
		}
		if undo != nil {
			undo()
		}
		return fail(err)
	}
	return digest, nil
}

// ExitManual consumes the receipt file named by the card and moves the manual
// card to todo or blocked. The admitted receipt is that file, read without
// following a symlink. The same request and generation can resume a partial
// publish. A different request cannot.
func ExitManual(repoRoot, cardRel string, profile manualzone.Profile, receiptPath, to, reason, blockedBy, requestID, authority string) error {
	if authority == "" {
		return errors.New("board authority is required")
	}
	rel, fromZone, err := manualSource(cardRel)
	if err != nil {
		return err
	}
	if fromZone != "manual" {
		return errors.New("manual exit requires a card in manual")
	}
	if requestID == "" || strings.TrimSpace(requestID) != requestID {
		return errors.New("exit request id is required")
	}
	full := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel))
	destRel := destinationRel(rel, to)
	destFull := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(destRel))
	content, err := readContainedRegular(repoRoot, full)
	if os.IsNotExist(err) {
		return resumeManualExit(repoRoot, rel, destRel, destFull, profile, receiptPath, to, reason, blockedBy, requestID, authority)
	}
	if err != nil {
		return err
	}
	loaded, err := loadManualExit(repoRoot, profile, string(content), receiptPath, to, reason, blockedBy, requestID, authority)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(destFull); err == nil {
		return fmt.Errorf("destination already exists: %s", destRel)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := refuseManualLinkEdits(repoRoot, rel, destRel); err != nil {
		return err
	}
	exitPairs := manualExitStampPairs(to, blockedBy)
	if _, ok := stampFrontmatterContent(content, exitPairs); !ok {
		return errors.New("card frontmatter has no closing fence")
	}
	key := closeRequestKey(requestID, loaded.entry.EntryGeneration)
	if err := stageRequestPartial(repoRoot, loaded.closeAbs, key, loaded.encodedClose); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destFull), 0o755); err != nil {
		return err
	}
	if err := moveFile(repoRoot, full, destFull); err != nil {
		return err
	}
	fail := func(cause error) error {
		rollbackMoved(repoRoot, full, destFull, content)
		return cause
	}
	if err := stampRequiredFrontmatter(destFull, exitPairs); err != nil {
		removeRegularFile(evidencePartial(loaded.closeAbs, key))
		return fail(err)
	}
	undo, err := commitManualLinks(repoRoot, rel, destRel)
	if err != nil {
		return fail(err)
	}
	if err := finalizeRequestPartial(repoRoot, loaded.closeAbs, key, loaded.encodedClose); err != nil {
		confirmed, readErr := readOptionalContained(repoRoot, loaded.closeAbs)
		if readErr != nil || !bytes.Equal(confirmed, loaded.encodedClose) {
			if undo != nil {
				undo()
			}
			return fail(err)
		}
	}
	return nil
}

func manualSource(cardRel string) (string, string, error) {
	rel, ok := relUnderTasks(cardRel)
	if !ok {
		return "", "", fmt.Errorf("%q is not under %s/", cardRel, tasksDirName())
	}
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return "", "", errors.New("manual card has no zone")
	}
	zone := parts[0]
	switch zone {
	case "todo", "doing", "blocked", "manual":
		return rel, zone, nil
	default:
		if st, ok := StatusFromDir(zone); ok {
			return rel, st.Dir(), nil
		}
		return "", "", fmt.Errorf("manual entry from %q is refused", zone)
	}
}

func destinationRel(rel, zone string) string {
	parts := strings.Split(rel, "/")
	parts[0] = zone
	return strings.Join(parts, "/")
}

func evidenceAbs(repoRoot string, profile manualzone.Profile, rel string) (string, error) {
	if strings.Contains(rel, "..") {
		return "", errors.New("evidence path escapes its root")
	}
	switch profile.Base {
	case "section-9":
		return filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel)), nil
	case "section-10":
		return filepath.Join(repoRoot, filepath.FromSlash(rel)), nil
	default:
		return "", errors.New("manual lifecycle is not selected")
	}
}

func rollbackMoved(repoRoot, src, dst string, original []byte) {
	if _, err := os.Lstat(dst); err == nil {
		_ = moveFile(repoRoot, dst, src)
	}
	if _, err := os.Lstat(src); err == nil {
		_ = writePreservingMode(src, original)
	}
}

func refuseSymlinkAncestors(root, abs string) error {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("evidence path escapes the repository")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "" || part == "." || part == ".." {
			return errors.New("evidence path is not a contained relative path")
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
			return errors.New("symlink in the evidence path is refused")
		}
	}
	return nil
}
