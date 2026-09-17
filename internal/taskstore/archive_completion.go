package taskstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

// archiveCompletionBinding is the completion-only part of a completed archive
// receipt. A transaction being completed does not imply it has this binding.
// No writer or resolver may publish this from a pending operation. These local
// records are consistency evidence, not signatures or certification of work.
type archiveCompletionBinding struct {
	SchemaVersion   int    `json:"schemaVersion"`
	Provenance      string `json:"provenance"`
	RequestID       string `json:"requestId"`
	BoardPath       string `json:"boardPath"`
	ID              string `json:"id"`
	Identity        string `json:"identity"`
	Source          string `json:"source"`
	Target          string `json:"target"`
	FinalSHA256     string `json:"finalSha256"`
	PolicyCanonical []byte `json:"policyCanonical"`
	PolicyDigest    string `json:"policyDigest"`
	RulesCanonical  []byte `json:"rulesCanonical"`
	RulesDigest     string `json:"rulesDigest"`
	Assertion       string `json:"assertion"`
}

const archiveCompletionLimit = 256 * 1024

func decodeArchiveCompletion(raw []byte) (archiveCompletionBinding, error) {
	var b archiveCompletionBinding
	if len(raw) > archiveCompletionLimit || !utf8.Valid(raw) {
		return b, fmt.Errorf("archive completion size or UTF-8 invalid")
	}
	if err := rejectJSONSurrogates(raw); err != nil {
		return b, err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return b, err
	}
	keys := []string{"schemaVersion", "provenance", "requestId", "boardPath", "id", "identity", "source", "target", "finalSha256", "policyCanonical", "policyDigest", "rulesCanonical", "rulesDigest", "assertion"}
	if err := relocationShape(raw, keys, nil); err != nil {
		return b, fmt.Errorf("archive completion shape: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return b, err
	}
	for _, key := range []string{"policyCanonical", "rulesCanonical"} {
		if bytes.TrimSpace(fields[key])[0] != '"' {
			return b, fmt.Errorf("archive %s must be a base64 string", key)
		}
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, err
	}
	return b, validateArchiveCompletion(b)
}

func archiveCompletionBytes(b archiveCompletionBinding) ([]byte, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	if _, err := decodeArchiveCompletion(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func validateArchiveCompletion(b archiveCompletionBinding) error {
	id, err := cardid.Parse(b.ID)
	if err != nil || id.Prefix != "TASK" || id.Key() != b.Identity {
		return fmt.Errorf("archive completion requires exact TASK identity")
	}
	if b.SchemaVersion != 1 || !claimToken.MatchString(b.RequestID) || !utf8.ValidString(b.BoardPath) || !canonicalBoardPath(b.BoardPath) || !sharedHex64.MatchString(b.FinalSHA256) {
		return fmt.Errorf("invalid archive completion version, request, board or hash")
	}
	p, err := boardpolicy.Parse(b.PolicyCanonical)
	if err != nil {
		return err
	}
	canonical, err := p.Canonical()
	if err != nil || !bytes.Equal(canonical, b.PolicyCanonical) || bytesDigest(canonical) != b.PolicyDigest {
		return fmt.Errorf("archive completion policy binding mismatch")
	}
	rules, err := archivepolicy.ParseConfig(b.RulesCanonical)
	if err != nil {
		return err
	}
	canonical, err = rules.Canonical()
	if err != nil || !bytes.Equal(canonical, b.RulesCanonical) || bytesDigest(canonical) != b.RulesDigest {
		return fmt.Errorf("archive completion rules binding mismatch")
	}
	switch b.Provenance {
	case "workflow-done":
		zone, target, err := archiveDestination(b.Source, p)
		if err != nil || !p.Workflow(zone) || zone != p.DoneZone() || target != b.Target || b.Assertion != "" {
			return fmt.Errorf("archive completion is not a normal workflow-done binding")
		}
	case "legacy-completion":
		if b.Source != b.Target || strings.TrimSpace(b.Assertion) == "" || !utf8.ValidString(b.Assertion) || len(b.Assertion) > 4096 {
			return fmt.Errorf("legacy completion requires explicit assertion and unchanged archived path")
		}
		if err := validateArchivedBindingPath(b.Target, p); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operation %q cannot grant archive completion", b.Provenance)
	}
	return nil
}

// verifyArchiveCompletion requires the currently discovered card, never merely
// a receipt. The caller must separately reject duplicate identities and missing
// cards and must only pass bindings from completed, owner-validated operations.
func verifyArchiveCompletion(b archiveCompletionBinding, board, currentPath string, raw []byte) error {
	if err := validateArchiveCompletion(b); err != nil {
		return err
	}
	if board != b.BoardPath || currentPath != b.Target || len(raw) == 0 || len(raw) > maxCardBytes || bytesDigest(raw) != b.FinalSHA256 {
		return fmt.Errorf("archive completion current card or board mismatch")
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return err
	}
	if doc.View().ID != b.ID {
		return fmt.Errorf("archive completion raw identity mismatch")
	}
	if strings.EqualFold(doc.View().Status, "superseded") {
		return fmt.Errorf("superseded card cannot grant archive completion")
	}
	if b.Provenance == "legacy-completion" {
		return nil
	}
	cfg, err := archivepolicy.ParseConfig(b.RulesCanonical)
	if err != nil {
		return err
	}
	m, err := doc.ProjectArchiveMetadata(cfg.Admission.Fields.Mapping())
	if err != nil {
		return err
	}
	value := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	decision, err := archivepolicy.EvaluateNormal(archivepolicy.Rules{AcceptedReviews: cfg.Admission.AcceptedReviews}, archivepolicy.Facts{
		ID: b.ID, Kind: "task", Status: "done", WorkflowDone: true, Review: value(m.QualityReview), Evidence: value(m.QualityReviewEvidence),
	}, nil)
	if err != nil {
		return err
	}
	if !decision.CompletionEligible {
		return fmt.Errorf("archive completion historical admission is not satisfied")
	}
	return nil
}
