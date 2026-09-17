package taskstore

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// ArchiveNamespaceCard is the caller-supplied current-card snapshot used to
// prove that a rebind did not silently replace an archived card.
type ArchiveNamespaceCard struct {
	Path string
	ID   string
	Raw  []byte
	Mode uint32
}

type ArchiveNamespaceCardBinding struct {
	Path, ID, SHA256 string
	Mode             uint32
}

// ArchiveNamespaceRebindPlan is an in-memory preparation model. Its durable
// envelope and aggregate transaction size limits are not defined here.
type ArchiveNamespaceRebindPlan struct {
	OriginalJournal       []byte
	OriginalJournalSHA256 string
	TargetJournal         []byte
	TargetJournalSHA256   string
	BoardPath             string
	SourceNamespace       string
	TargetNamespace       string
	JournalMode           uint32
	Cards                 []ArchiveNamespaceCardBinding
}

// PlanArchiveNamespaceRebind changes only the namespace fields of a strict
// archive journal. It does not publish, activate, or inspect the filesystem.
func PlanArchiveNamespaceRebind(original []byte, expectedSHA256, expectedBoard, sourceNamespace, targetNamespace string, journalMode uint32) (ArchiveNamespaceRebindPlan, error) {
	var plan ArchiveNamespaceRebindPlan
	if len(original) == 0 || len(original) > maxRepairsBytes || !utf8.Valid(original) || bytesDigest(original) != expectedSHA256 {
		return plan, fmt.Errorf("archive rebind original journal hash mismatch")
	}
	if !validSharedRoot(expectedBoard) || !sharedHex32.MatchString(targetNamespace) || (sourceNamespace != "" && !sharedHex32.MatchString(sourceNamespace)) {
		return plan, fmt.Errorf("invalid archive rebind board or namespace")
	}
	if sourceNamespace != "" && targetNamespace != sourceNamespace {
		return plan, fmt.Errorf("archive rebind cannot change an established namespace")
	}
	if journalMode == 0 || journalMode&^0777 != 0 {
		return plan, fmt.Errorf("invalid archive rebind journal mode")
	}
	j, err := decodeArchiveJournal(original)
	if err != nil {
		return plan, err
	}
	if j.BoardPath != expectedBoard || j.Namespace != sourceNamespace {
		return plan, fmt.Errorf("archive rebind source scope mismatch")
	}
	for _, rec := range j.Records {
		if rec.State == "pending" {
			return plan, fmt.Errorf("archive rebind cannot contain pending records")
		}
		if rec.BoardPath != expectedBoard || rec.Namespace != sourceNamespace {
			return plan, fmt.Errorf("archive rebind record scope mismatch")
		}
		if rec.Mode == 0 || rec.Mode&^0777 != 0 || !utf8.ValidString(rec.Target) || strings.TrimSpace(rec.Target) == "" {
			return plan, fmt.Errorf("invalid archive rebind record mode or target")
		}
		plan.Cards = append(plan.Cards, ArchiveNamespaceCardBinding{Path: rec.Target, ID: rec.ID, SHA256: rec.FinalSHA256, Mode: rec.Mode})
	}
	target := j
	target.Namespace = targetNamespace
	target.Records = make([]archiveRecord, len(j.Records))
	for i, rec := range j.Records {
		target.Records[i] = rec
		target.Records[i].Namespace = targetNamespace
		if rec.Completion != nil {
			completion := *rec.Completion
			completion.PolicyCanonical = append([]byte(nil), rec.Completion.PolicyCanonical...)
			completion.RulesCanonical = append([]byte(nil), rec.Completion.RulesCanonical...)
			target.Records[i].Completion = &completion
		}
		target.Records[i].PolicyCanonical = append([]byte(nil), rec.PolicyCanonical...)
		target.Records[i].RulesCanonical = append([]byte(nil), rec.RulesCanonical...)
	}
	targetRaw, err := archiveJournalBytes(target)
	if err != nil {
		return ArchiveNamespaceRebindPlan{}, err
	}
	plan.OriginalJournalSHA256 = expectedSHA256
	plan.OriginalJournal = append([]byte(nil), original...)
	plan.TargetJournal = targetRaw
	plan.TargetJournalSHA256 = bytesDigest(targetRaw)
	plan.BoardPath, plan.SourceNamespace, plan.TargetNamespace, plan.JournalMode = expectedBoard, sourceNamespace, targetNamespace, journalMode
	return plan, nil
}

// ValidateArchiveNamespaceRebindPlan derives the only allowed target from
// immutable saved input, never from current policy or mutable filesystem state.
func ValidateArchiveNamespaceRebindPlan(plan ArchiveNamespaceRebindPlan) error {
	expected, err := PlanArchiveNamespaceRebind(plan.OriginalJournal, plan.OriginalJournalSHA256, plan.BoardPath, plan.SourceNamespace, plan.TargetNamespace, plan.JournalMode)
	if err != nil {
		return err
	}
	if plan.TargetJournalSHA256 != expected.TargetJournalSHA256 || !bytes.Equal(plan.TargetJournal, expected.TargetJournal) {
		return fmt.Errorf("archive rebind target differs from exact namespace-only transformation")
	}
	if len(plan.Cards) != len(expected.Cards) {
		return fmt.Errorf("archive rebind inventory binding differs from journal")
	}
	for i := range expected.Cards {
		if plan.Cards[i] != expected.Cards[i] {
			return fmt.Errorf("archive rebind inventory binding differs from journal")
		}
	}
	return nil
}

// VerifyArchiveNamespaceRebindInventory checks the exact current archived
// card set described by a prepared plan. Raw bytes are never rewritten.
func VerifyArchiveNamespaceRebindInventory(plan ArchiveNamespaceRebindPlan, cards map[string]ArchiveNamespaceCard) error {
	if err := ValidateArchiveNamespaceRebindPlan(plan); err != nil {
		return err
	}
	if len(cards) != len(plan.Cards) {
		return fmt.Errorf("archive rebind current inventory differs")
	}
	journal, err := decodeArchiveJournal(plan.TargetJournal)
	if err != nil {
		return err
	}
	for i, binding := range plan.Cards {
		current, ok := cards[binding.Path]
		if !ok || len(current.Raw) == 0 || len(current.Raw) > maxCardBytes || current.Path != binding.Path || current.ID != binding.ID || current.Mode != binding.Mode || bytesDigest(current.Raw) != binding.SHA256 {
			return fmt.Errorf("archive rebind current card differs: %s", binding.Path)
		}
		doc, err := card.Parse(current.Raw)
		if err != nil || doc.View().ID != binding.ID {
			return fmt.Errorf("archive rebind current raw identity differs: %s", binding.Path)
		}
		if completion := journal.Records[i].Completion; completion != nil {
			if err := verifyArchiveCompletion(*completion, plan.BoardPath, binding.Path, current.Raw); err != nil {
				return err
			}
		}
	}
	return nil
}
