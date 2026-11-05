package taskflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

func manualExitStampPairs(to, blockedBy string) [][2]string {
	pairs := [][2]string{{"status", to}}
	if to == "blocked" {
		pairs = append(pairs, [2]string{"blocked-by", blockedBy})
	}
	return pairs
}

func projectedManualSettlement(content, rel, destRel string) (string, error) {
	stamped, ok := stampFrontmatterContent([]byte(content), [][2]string{{"status", "manual"}})
	if !ok {
		return "", errors.New("card frontmatter has no closing fence")
	}
	settled := string(stamped)
	oldDir := path.Dir(path.Join(tasksDirName(), rel))
	newDir := path.Dir(path.Join(tasksDirName(), destRel))
	if oldDir != newDir {
		retargeted, _ := retargetOutbound(settled, oldDir, newDir)
		settled = retargeted
	}
	return settled, nil
}

// stamperFrontmatterValue reads one frontmatter value using the same fence
// rule as stampFrontmatterContent. A closing line the stamper rejects, such
// as "--- " with a trailing space, yields an empty value.
func stamperFrontmatterValue(content, key string) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return ""
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return ""
	}
	prefix := key + ":"
	for i := 1; i < closing; i++ {
		line := strings.TrimRight(lines[i], "\r")
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

func moveCardBack(repoRoot, destFull, sourceFull string) error {
	if _, err := os.Lstat(sourceFull); err == nil {
		return errors.New("manual source already exists")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(sourceFull), 0o755); err != nil {
		return err
	}
	return moveFile(repoRoot, destFull, sourceFull)
}

func resumeManualExit(repoRoot, rel, destRel, destFull string, profile manualzone.Profile, receiptPath, to, reason, blockedBy, requestID, authority string) error {
	if to != "todo" && to != "blocked" {
		return errors.New("rejected to cancelled is outside this support range")
	}
	content, err := readContainedRegular(repoRoot, destFull)
	if err != nil {
		return err
	}
	destFields, err := manualzone.ParseCard(string(content))
	if err != nil {
		return err
	}
	if receiptPath != destFields.ReceiptPath {
		return errors.New("receipt path does not match the card")
	}
	_, entryRel, _, err := manualzone.ReceiptNames(profile, destFields)
	if err != nil {
		return err
	}
	entryAbs, err := evidenceAbs(repoRoot, profile, entryRel)
	if err != nil {
		return err
	}
	entryRaw, err := readContainedRegular(repoRoot, entryAbs)
	if err != nil {
		return fmt.Errorf("read entry record: %w", err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		return err
	}
	if err := admitResumeDestination(content, entry.Subject, rel, destRel, to, blockedBy); err != nil {
		return err
	}
	loaded, err := loadManualExit(repoRoot, profile, entry.Subject, receiptPath, to, reason, blockedBy, requestID, authority)
	if err != nil {
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
	if err := stampRequiredFrontmatter(destFull, exitPairs); err != nil {
		removeRegularFile(evidencePartial(loaded.closeAbs, key))
		sourceFull := filepath.Join(repoRoot, tasksDirName(), filepath.FromSlash(rel))
		if restoreErr := moveCardBack(repoRoot, destFull, sourceFull); restoreErr != nil {
			return restoreErr
		}
		return err
	}
	if _, err := commitManualLinks(repoRoot, rel, destRel); err != nil {
		return err
	}
	return finalizeRequestPartial(repoRoot, loaded.closeAbs, key, loaded.encodedClose)
}

// admitResumeDestination accepts a destination only when its canonical digest
// matches the entry subject or a later stamp of that subject. The legal crash
// windows are the moved card, the status stamp, the blocked-by stamp, and the
// outbound retarget that follows those stamps. Review verdict fields stay
// outside the digest. Any other body edit is refused before a close is staged.
func admitResumeDestination(dest []byte, subject, rel, destRel, to, blockedBy string) error {
	destDigest, err := CanonicalReviewDigest(string(dest))
	if err != nil {
		return err
	}
	projections, err := exitSubjectProjections(subject, rel, destRel, to, blockedBy)
	if err != nil {
		return err
	}
	for _, candidate := range projections {
		digest, err := CanonicalReviewDigest(candidate)
		if err != nil {
			return err
		}
		if digest == destDigest {
			return nil
		}
	}
	return errors.New("destination card does not match the entry subject")
}

func exitSubjectProjections(subject, rel, destRel, to, blockedBy string) ([]string, error) {
	projections := []string{subject}
	stamped, ok := stampFrontmatterContent([]byte(subject), [][2]string{{"status", to}})
	if !ok {
		return nil, errors.New("entry subject has no frontmatter to stamp")
	}
	projections = append(projections, string(stamped))
	linked := stamped
	if to == "blocked" {
		both, ok := stampFrontmatterContent(stamped, [][2]string{{"blocked-by", blockedBy}})
		if !ok {
			return nil, errors.New("entry subject has no frontmatter to stamp")
		}
		projections = append(projections, string(both))
		linked = both
	}
	oldDir := path.Dir(path.Join(tasksDirName(), rel))
	newDir := path.Dir(path.Join(tasksDirName(), destRel))
	if oldDir != newDir {
		retargeted, _ := retargetOutbound(string(linked), oldDir, newDir)
		projections = append(projections, retargeted)
	}
	return projections, nil
}

type manualExitLoaded struct {
	entry        manualzone.EntryRecord
	closeAbs     string
	encodedClose []byte
}

func loadManualExit(repoRoot string, profile manualzone.Profile, card, receiptPath, to, reason, blockedBy, requestID, authority string) (manualExitLoaded, error) {
	fields, err := manualzone.ParseCard(card)
	if err != nil {
		return manualExitLoaded{}, err
	}
	if receiptPath != fields.ReceiptPath {
		return manualExitLoaded{}, errors.New("receipt path does not match the card")
	}
	receiptRel, entryRel, closeRel, err := manualzone.ReceiptNames(profile, fields)
	if err != nil {
		return manualExitLoaded{}, err
	}
	entryAbs, err := evidenceAbs(repoRoot, profile, entryRel)
	if err != nil {
		return manualExitLoaded{}, err
	}
	closeAbs, err := evidenceAbs(repoRoot, profile, closeRel)
	if err != nil {
		return manualExitLoaded{}, err
	}
	receiptAbs, err := evidenceAbs(repoRoot, profile, receiptRel)
	if err != nil {
		return manualExitLoaded{}, err
	}
	entryRaw, err := readContainedRegular(repoRoot, entryAbs)
	if err != nil {
		return manualExitLoaded{}, fmt.Errorf("read entry record: %w", err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		return manualExitLoaded{}, err
	}
	receiptRaw, err := readContainedRegular(repoRoot, receiptAbs)
	if err != nil {
		return manualExitLoaded{}, fmt.Errorf("read receipt: %w", err)
	}
	receipt, err := manualzone.DecodeReceipt(receiptRaw)
	if err != nil {
		return manualExitLoaded{}, err
	}
	existing, err := readOptionalContained(repoRoot, closeAbs)
	if err != nil {
		return manualExitLoaded{}, err
	}
	if err := manualzone.PlanExit(profile, fields, card, receipt, entry, authority, to, reason, blockedBy, false, CanonicalReviewDigest); err != nil {
		return manualExitLoaded{}, err
	}
	encoded, err := manualzone.Encode(manualzone.CloseRecord{
		CardID:          fields.ID,
		EntryGeneration: entry.EntryGeneration,
		EntrySHA256:     entry.EntrySHA256,
		RequestID:       requestID,
		Result:          receipt.Result,
		Destination:     to,
		Reason:          reason,
	})
	if err != nil {
		return manualExitLoaded{}, err
	}
	if existing != nil && !bytes.Equal(existing, encoded) {
		return manualExitLoaded{}, errors.New("entry generation is already closed")
	}
	return manualExitLoaded{entry: entry, closeAbs: closeAbs, encodedClose: encoded}, nil
}
