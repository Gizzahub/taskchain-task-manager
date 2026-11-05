package taskflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

// ReissueManual retires the current entry and binds a new generation for one
// human request. The writer is resolved from the run identity. A missing
// identity, a non-human writer, or conflicting actor sources publish nothing.
// The same request publishes the same generation again.
func ReissueManual(repoRoot, cardRel string, profile manualzone.Profile, state manualzone.RuntimeState, authority string, req manualzone.ReissueRequest) (string, error) {
	if authority == "" {
		return "", errors.New("board authority is required")
	}
	if err := admitHumanReissue(repoRoot, req.ActorKind); err != nil {
		return "", err
	}
	rel, fromZone, err := manualSource(cardRel)
	if err != nil {
		return "", err
	}
	if fromZone != "manual" {
		return "", errors.New("manual reissue requires a card in manual")
	}
	full := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel))
	content, err := readContainedRegular(repoRoot, full)
	if err != nil {
		return "", err
	}
	if _, ok := stampFrontmatterContent(content, [][2]string{{"status", "manual"}}); !ok {
		return "", errors.New("card frontmatter has no closing fence")
	}
	fields, err := manualzone.ParseCard(string(content))
	if err != nil {
		return "", err
	}
	_, oldEntryRel, oldCloseRel, err := manualzone.AnchorPaths(profile, fields.ID, req.PreviousGeneration)
	if err != nil {
		return "", err
	}
	entryAbs, err := evidenceAbs(repoRoot, profile, oldEntryRel)
	if err != nil {
		return "", err
	}
	closeAbs, err := evidenceAbs(repoRoot, profile, oldCloseRel)
	if err != nil {
		return "", err
	}
	entryRaw, err := readContainedRegular(repoRoot, entryAbs)
	if err != nil {
		return "", fmt.Errorf("read entry record: %w", err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		return "", err
	}
	existingClose, err := readOptionalContained(repoRoot, closeAbs)
	if err != nil {
		return "", err
	}
	plan, err := manualzone.PlanReissue(profile, fields, string(content), entry, authority, state, req, CanonicalReviewDigest)
	if err != nil {
		return "", err
	}
	encodedEntry, err := manualzone.Encode(plan.Entry)
	if err != nil {
		return "", err
	}
	encodedClose, err := manualzone.Encode(plan.Close)
	if err != nil {
		return "", err
	}
	if existingClose != nil && !bytes.Equal(existingClose, encodedClose) {
		return "", errors.New("previous generation is already closed")
	}
	_, newEntryRel, _, err := manualzone.AnchorPaths(profile, fields.ID, plan.Entry.EntryGeneration)
	if err != nil {
		return "", err
	}
	newEntryAbs, err := evidenceAbs(repoRoot, profile, newEntryRel)
	if err != nil {
		return "", err
	}
	entryKey := entryRequestKey(fields.ID, plan.Entry.EntryGeneration)
	closeKey := closeRequestKey(req.RequestID, req.PreviousGeneration)
	if err := stageRequestPartial(repoRoot, newEntryAbs, entryKey, encodedEntry); err != nil {
		return "", err
	}
	if err := stageRequestPartial(repoRoot, closeAbs, closeKey, encodedClose); err != nil {
		return "", err
	}
	if string(content) != plan.NewCard {
		if err := writePreservingMode(full, []byte(plan.NewCard)); err != nil {
			return "", err
		}
	}
	if err := finalizeRequestPartial(repoRoot, newEntryAbs, entryKey, encodedEntry); err != nil {
		return "", err
	}
	if err := finalizeRequestPartial(repoRoot, closeAbs, closeKey, encodedClose); err != nil {
		return "", err
	}
	return plan.Entry.EntrySHA256, nil
}

func admitHumanReissue(root, requestedKind string) error {
	if strings.TrimSpace(os.Getenv("CE_TASK_ACTOR")) != "" && strings.TrimSpace(os.Getenv("GIT_WORK_ACTOR")) != "" {
		return errors.New("reissue writer identity sources conflict")
	}
	owner, err := taskruntime.ResolveOwner(context.Background(), root)
	if err != nil || !owner.Valid() {
		return errors.New("reissue writer identity is missing")
	}
	if owner.Kind != "human" {
		return errors.New("manual reissue requires a human writer")
	}
	if requestedKind != "" && requestedKind != owner.Kind {
		return errors.New("reissue actor kind conflicts with the resolved writer")
	}
	return nil
}
