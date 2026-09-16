package intentdoc

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

var intentID = regexp.MustCompile(`^INTENT-[0-9a-f]{32}$`)
var batchID = regexp.MustCompile(`^BATCH-[0-9a-f]{32}$`)
var digestID = regexp.MustCompile(`^[0-9a-f]{64}$`)
var criterionKey = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// ValidateIdentity checks a registry key without constructing document content.
func ValidateIdentity(kind, id string, revision uint32) error {
	if revision == 0 {
		return errors.New("revision must be positive")
	}
	if kind == "intent" && intentID.MatchString(id) {
		return nil
	}
	if kind == "batch" && batchID.MatchString(id) {
		return nil
	}
	return errors.New("invalid intent/batch kind or ID")
}

func text(value string, max int, multiline bool) error {
	if value == "" || len(value) > max || strings.TrimSpace(value) != value {
		return errors.New("text must be nonempty, bounded and without surrounding whitespace")
	}
	for _, r := range value {
		if unicode.IsControl(r) && !(multiline && (r == '\n' || r == '\t')) {
			return errors.New("text contains a forbidden control character")
		}
	}
	return nil
}

func texts(values []string) error {
	if values == nil || len(values) > 128 {
		return errors.New("list must contain 0..128 strings")
	}
	for i, value := range values {
		if err := text(value, 16<<10, true); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}
	return nil
}

func validateIntent(d Intent) error {
	if d.SchemaVersion != 1 || d.Kind != "intent" || ValidateIdentity(d.Kind, d.ID, d.Revision) != nil {
		return errors.New("invalid intent schema, kind, ID or revision")
	}
	if d.Mode != "completion" {
		return errors.New("only completion mode is supported; maintenance requires a separate loop contract")
	}
	if err := text(d.Title, 256, false); err != nil {
		return fmt.Errorf("title: %w", err)
	}
	if err := text(d.Outcome, 16<<10, true); err != nil {
		return fmt.Errorf("outcome: %w", err)
	}
	if err := texts(d.Constraints); err != nil {
		return fmt.Errorf("constraints: %w", err)
	}
	if err := texts(d.NonGoals); err != nil {
		return fmt.Errorf("nonGoals: %w", err)
	}
	if len(d.SuccessCriteria) == 0 || len(d.SuccessCriteria) > 128 {
		return errors.New("successCriteria requires 1..128 criteria")
	}
	seen := map[string]bool{}
	for _, c := range d.SuccessCriteria {
		if !criterionKey.MatchString(c.Key) || seen[c.Key] {
			return errors.New("invalid or duplicate success criterion key")
		}
		seen[c.Key] = true
		if err := text(c.Text, 16<<10, true); err != nil {
			return fmt.Errorf("criterion %s: %w", c.Key, err)
		}
	}
	return nil
}

func validateRef(ref IntentRef) error {
	if !intentID.MatchString(ref.ID) || ref.Revision == 0 || !digestID.MatchString(ref.Digest) {
		return errors.New("intent reference requires ID, positive revision and lowercase SHA-256 digest")
	}
	return nil
}

func validateBatch(d Batch) error {
	if d.SchemaVersion != 1 || d.Kind != "batch" || ValidateIdentity(d.Kind, d.ID, d.Revision) != nil {
		return errors.New("invalid batch schema, kind, ID or revision")
	}
	if err := validateRef(d.Intent); err != nil {
		return err
	}
	if err := text(d.Gap, 16<<10, true); err != nil {
		return fmt.Errorf("gap: %w", err)
	}
	if err := texts(d.Constraints); err != nil {
		return fmt.Errorf("constraints: %w", err)
	}
	if err := texts(d.AuthorizationRefs); err != nil {
		return fmt.Errorf("authorizationRefs: %w", err)
	}
	if len(d.TaskIDs) == 0 || len(d.TaskIDs) > 128 {
		return errors.New("taskIds requires 1..128 TASK IDs")
	}
	seen := map[string]bool{}
	for _, raw := range d.TaskIDs {
		if len(raw) > 16<<10 {
			return errors.New("TASK ID exceeds 16 KiB")
		}
		id, err := cardid.Parse(raw)
		if err != nil || id.Prefix != "TASK" || seen[id.Key()] {
			return errors.New("invalid or duplicate TASK identity")
		}
		seen[id.Key()] = true
	}
	if d.Evaluation != nil {
		return validateEvaluation(*d.Evaluation, d.Intent)
	}
	return nil
}

func validateEvaluation(e Evaluation, ref IntentRef) error {
	if e.Intent != ref {
		return errors.New("evaluation must reference the batch's exact intent revision and digest")
	}
	if err := text(e.Actor, 128, false); err != nil {
		return fmt.Errorf("actor: %w", err)
	}
	if err := text(e.Reason, 16<<10, true); err != nil {
		return fmt.Errorf("reason: %w", err)
	}
	if err := texts(e.RemainingGaps); err != nil {
		return fmt.Errorf("remainingGaps: %w", err)
	}
	if err := texts(e.EvidenceRefs); err != nil {
		return fmt.Errorf("evidenceRefs: %w", err)
	}
	switch e.Decision {
	case "achieved":
		if len(e.RemainingGaps) != 0 || len(e.EvidenceRefs) == 0 {
			return errors.New("achieved assertion requires evidence references and no remaining gaps")
		}
	case "continue", "blocked", "revise":
	default:
		return errors.New("unsupported evaluation decision")
	}
	return nil
}
