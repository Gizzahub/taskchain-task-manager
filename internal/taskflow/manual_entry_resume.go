package taskflow

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

// stageEntryBaseline records the pre-move card under this request's entry
// partial. Resume publishes only that card, the same card with status stamped
// manual, or that result with outbound links retargeted.
func stageEntryBaseline(repoRoot, entryAbs string, profile manualzone.Profile, authority, preMove, rel, destRel string) error {
	projected, err := projectedManualSettlement(preMove, rel, destRel)
	if err != nil {
		return err
	}
	fields, err := manualzone.ParseCard(projected)
	if err != nil {
		return err
	}
	digest, err := CanonicalReviewDigest(projected)
	if err != nil {
		return err
	}
	record, err := manualzone.NewEntryRecord(profile, fields, authority, projected, digest)
	if err != nil {
		return err
	}
	key := entryRequestKey(fields.ID, record.EntryGeneration)
	return stageRequestPartial(repoRoot, entryAbs, key, []byte(preMove))
}

func resumeManualEntry(repoRoot, rel, fromZone string, profile manualzone.Profile, state manualzone.RuntimeState, authority string) (string, error) {
	destRel := destinationRel(rel, "manual")
	destFull := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(destRel))
	sourceFull := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel))
	content, err := readContainedRegular(repoRoot, destFull)
	if err != nil {
		return "", err
	}
	// The stamper is the only fence decision. A fence it cannot stamp must
	// not move, stage, or publish, even when a looser parser can see a status.
	if _, ok := stampFrontmatterContent(content, [][2]string{{"status", "manual"}}); !ok {
		return "", errors.New("card frontmatter has no closing fence")
	}
	fields, err := manualzone.ParseCard(strings.ReplaceAll(string(content), "\r\n", "\n"))
	if err != nil {
		return "", err
	}
	if _, _, _, err := manualzone.PlanEntry(profile, fromZone, fields, state); err != nil {
		return "", err
	}
	status := stamperFrontmatterValue(string(content), "status")
	_, entryRel, _, err := manualzone.ReceiptNames(profile, fields)
	if err != nil {
		return "", err
	}
	entryAbs, err := evidenceAbs(repoRoot, profile, entryRel)
	if err != nil {
		return "", err
	}
	digest, err := CanonicalReviewDigest(string(content))
	if err != nil {
		return "", err
	}
	record, err := manualzone.NewEntryRecord(profile, fields, authority, string(content), digest)
	if err != nil {
		return "", err
	}
	key := entryRequestKey(fields.ID, record.EntryGeneration)
	existing, err := readOptionalContained(repoRoot, entryAbs)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return resumePublishedEntry(string(content), fields.ID, existing)
	}
	partial, err := readOptionalContained(repoRoot, evidencePartial(entryAbs, key))
	if err != nil {
		return "", err
	}
	baseline, staged := classifyEntryPartial(partial, fields.ID)
	if baseline != "" {
		return resumeFromPreMoveBaseline(repoRoot, rel, destRel, destFull, sourceFull, entryAbs, key, profile, authority, baseline, string(content), status)
	}
	if staged != nil {
		return resumeStagedEntry(repoRoot, destFull, sourceFull, entryAbs, key, partial, staged, string(content), fields.ID, status)
	}
	if status != "manual" {
		return restoreUnpublishedEntry(repoRoot, destFull, sourceFull, entryAbs, key)
	}
	return "", errors.New("manual entry is incomplete")
}

func resumePublishedEntry(dest, cardID string, existing []byte) (string, error) {
	stored, err := manualzone.DecodeEntry(existing)
	if err != nil {
		return "", err
	}
	storedDigest, err := CanonicalReviewDigest(stored.Subject)
	if err != nil {
		return "", err
	}
	destDigest, err := CanonicalReviewDigest(dest)
	if err != nil {
		return "", err
	}
	if storedDigest == destDigest && stored.CardID == cardID {
		return storedDigest, nil
	}
	return "", errors.New("manual entry is already published with different bytes")
}

func classifyEntryPartial(raw []byte, cardID string) (string, *manualzone.EntryRecord) {
	if len(raw) == 0 {
		return "", nil
	}
	if stored, err := manualzone.DecodeEntry(raw); err == nil && stored.CardID == cardID && stored.Scope == "card" {
		return "", &stored
	}
	if _, ok := stampFrontmatterContent(raw, [][2]string{{"status", "manual"}}); !ok {
		return "", nil
	}
	fields, err := manualzone.ParseCard(strings.ReplaceAll(string(raw), "\r\n", "\n"))
	if err != nil || fields.ID != cardID {
		return "", nil
	}
	return string(raw), nil
}

func resumeFromPreMoveBaseline(repoRoot, rel, destRel, destFull, sourceFull, entryAbs, key string, profile manualzone.Profile, authority, baseline, dest, status string) (string, error) {
	stamped, settled, err := entryBaselineStates(baseline, rel, destRel)
	if err != nil {
		if status != "manual" {
			if restoreErr := moveCardBack(repoRoot, destFull, sourceFull); restoreErr != nil {
				return "", restoreErr
			}
		}
		return "", err
	}
	// Restore only the exact pre-move bytes. Publish only the stamper's status
	// rewrite, or that rewrite plus the outbound retarget. The CRLF status
	// rewrite is those stamper bytes. A digest-equal edit is not a stamp.
	if dest == baseline {
		return restoreUnpublishedEntry(repoRoot, destFull, sourceFull, entryAbs, key)
	}
	if dest != stamped && dest != settled {
		return "", errors.New("destination card does not match the entry subject")
	}
	return finishBaselineEntry(repoRoot, rel, destRel, destFull, entryAbs, key, profile, authority, settled)
}

func resumeStagedEntry(repoRoot, destFull, sourceFull, entryAbs, key string, partial []byte, staged *manualzone.EntryRecord, dest, cardID, status string) (string, error) {
	if status != "manual" {
		return restoreUnpublishedEntry(repoRoot, destFull, sourceFull, entryAbs, key)
	}
	storedDigest, err := CanonicalReviewDigest(staged.Subject)
	if err != nil {
		return "", err
	}
	destDigest, err := CanonicalReviewDigest(dest)
	if err != nil {
		return "", err
	}
	if storedDigest != destDigest || staged.CardID != cardID {
		return "", errors.New("destination card does not match the entry subject")
	}
	if err := finalizeRequestPartial(repoRoot, entryAbs, key, partial); err != nil {
		return "", err
	}
	return storedDigest, nil
}

func restoreUnpublishedEntry(repoRoot, destFull, sourceFull, entryAbs, key string) (string, error) {
	if err := moveCardBack(repoRoot, destFull, sourceFull); err != nil {
		return "", err
	}
	removeRegularFile(evidencePartial(entryAbs, key))
	return "", errors.New("manual entry is incomplete")
}

func entryBaselineStates(preMove, rel, destRel string) (string, string, error) {
	stamped, ok := stampFrontmatterContent([]byte(preMove), [][2]string{{"status", "manual"}})
	if !ok {
		return "", "", errors.New("card frontmatter has no closing fence")
	}
	settled, err := projectedManualSettlement(preMove, rel, destRel)
	if err != nil {
		return "", "", err
	}
	return string(stamped), settled, nil
}

func finishBaselineEntry(repoRoot, rel, destRel, destFull, entryAbs, key string, profile manualzone.Profile, authority, settled string) (string, error) {
	if err := stampRequiredFrontmatter(destFull, [][2]string{{"status", "manual"}}); err != nil {
		return "", err
	}
	undo, err := commitManualLinks(repoRoot, rel, destRel)
	if err != nil {
		return "", err
	}
	onDisk, err := readContainedRegular(repoRoot, destFull)
	if err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	if string(onDisk) != settled {
		if undo != nil {
			undo()
		}
		return "", errors.New("destination card does not match the entry subject")
	}
	got, err := CanonicalReviewDigest(settled)
	if err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	fields, err := manualzone.ParseCard(strings.ReplaceAll(settled, "\r\n", "\n"))
	if err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	record, err := manualzone.NewEntryRecord(profile, fields, authority, string(onDisk), got)
	if err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	encoded, err := manualzone.Encode(record)
	if err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	if err := stageRequestPartial(repoRoot, entryAbs, key, encoded); err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	if err := finalizeRequestPartial(repoRoot, entryAbs, key, encoded); err != nil {
		if undo != nil {
			undo()
		}
		return "", err
	}
	return got, nil
}
