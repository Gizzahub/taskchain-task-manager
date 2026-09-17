package taskstore

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// archiveRecord is an internal transaction record, not evidence that work was
// implemented. Normal admission against the locked board is a writer preflight.
// Only completed records may supply completion bindings to dependency readers.
type archiveRecord struct {
	State           string                    `json:"state"`
	Operation       string                    `json:"operation"`
	RequestID       string                    `json:"requestId"`
	ID              string                    `json:"id"`
	Owner           string                    `json:"owner"`
	Token           string                    `json:"token"`
	BoardPath       string                    `json:"boardPath"`
	Namespace       string                    `json:"namespace"`
	Source          string                    `json:"source"`
	Target          string                    `json:"target"`
	OriginalSHA256  string                    `json:"originalSha256"`
	FinalSHA256     string                    `json:"finalSha256"`
	Mode            uint32                    `json:"mode"`
	PolicyCanonical []byte                    `json:"policyCanonical"`
	PolicyDigest    string                    `json:"policyDigest"`
	RulesCanonical  []byte                    `json:"rulesCanonical"`
	RulesDigest     string                    `json:"rulesDigest"`
	Assertion       string                    `json:"assertion"`
	Original        []byte                    `json:"original,omitempty"`
	Patched         []byte                    `json:"patched,omitempty"`
	Completion      *archiveCompletionBinding `json:"completion,omitempty"`
}

func validateArchiveRecord(r archiveRecord) error {
	if r.State != "pending" && r.State != "completed" {
		return fmt.Errorf("invalid archive transaction state")
	}
	if !claimToken.MatchString(r.RequestID) || identityKey(r.ID) == "" || !utf8.ValidString(r.Owner) || !validClaimOwner(r.Owner) || (r.Token != "" && !claimToken.MatchString(r.Token)) {
		return fmt.Errorf("invalid archive identity, owner or token")
	}
	if !utf8.ValidString(r.BoardPath) || !canonicalBoardPath(r.BoardPath) || (r.Namespace != "" && !sharedHex32.MatchString(r.Namespace)) || !sharedHex64.MatchString(r.OriginalSHA256) || !sharedHex64.MatchString(r.FinalSHA256) {
		return fmt.Errorf("invalid archive board, namespace or hash")
	}
	if r.Mode == 0 || r.Mode&^0777 != 0 || len(r.Original) > maxCardBytes || len(r.Patched) > maxCardBytes || !utf8.ValidString(r.Assertion) || len(r.Assertion) > 4096 {
		return fmt.Errorf("invalid archive mode, payload or assertion")
	}
	p, err := boardpolicy.Parse(r.PolicyCanonical)
	if err != nil {
		return err
	}
	canonical, err := p.Canonical()
	if err != nil || !bytes.Equal(canonical, r.PolicyCanonical) || bytesDigest(canonical) != r.PolicyDigest {
		return fmt.Errorf("archive record policy binding mismatch")
	}
	cfg, err := archivepolicy.ParseConfig(r.RulesCanonical)
	if err != nil {
		return err
	}
	canonical, err = cfg.Canonical()
	if err != nil || !bytes.Equal(canonical, r.RulesCanonical) || bytesDigest(canonical) != r.RulesDigest {
		return fmt.Errorf("archive record rules binding mismatch")
	}
	zone := ""
	switch r.Operation {
	case "archive", "supersede", "force":
		var target string
		zone, target, err = archiveDestination(r.Source, p)
		if err != nil {
			return err
		}
		if target != r.Target {
			return fmt.Errorf("archive target does not preserve source scope")
		}
		if r.Operation == "archive" && r.Assertion != "" {
			return fmt.Errorf("normal archive cannot use an override assertion")
		}
		if r.Operation == "force" && strings.TrimSpace(r.Assertion) == "" {
			return fmt.Errorf("forced archive requires explicit assertion")
		}
	case "legacy-adoption":
		if r.Source != r.Target || strings.TrimSpace(r.Assertion) == "" {
			return fmt.Errorf("legacy adoption requires unchanged target and explicit assertion")
		}
		if err := validateArchivedBindingPath(r.Target, p); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown archive operation")
	}
	if r.Operation != "supersede" && r.OriginalSHA256 != r.FinalSHA256 {
		return fmt.Errorf("archive operation unexpectedly changes source bytes")
	}
	needsCompletion := r.Operation == "archive" && isWorkTask(r.ID) && p.Workflow(zone) && zone == p.DoneZone()
	if needsCompletion && r.Completion == nil {
		return fmt.Errorf("normal done TASK archive requires completion binding")
	}
	if r.Completion != nil {
		if err := validateArchiveRecordCompletion(r); err != nil {
			return err
		}
	}
	if r.State == "completed" {
		if len(r.Original) != 0 || len(r.Patched) != 0 {
			return fmt.Errorf("completed archive retains source payload")
		}
		return nil
	}
	if len(r.Original) == 0 || len(r.Patched) == 0 || bytesDigest(r.Original) != r.OriginalSHA256 || bytesDigest(r.Patched) != r.FinalSHA256 {
		return fmt.Errorf("archive pending payload hash mismatch")
	}
	doc, err := card.Parse(r.Original)
	if err != nil {
		return err
	}
	if doc.View().ID != r.ID {
		return fmt.Errorf("archive pending raw identity mismatch")
	}
	if r.Operation == "archive" && strings.EqualFold(doc.View().Status, "superseded") {
		return fmt.Errorf("superseded card requires separate archive operation")
	}
	patch := doc.Bytes()
	if r.Operation == "supersede" {
		patch, _, err = doc.SetFrontmatterStatus("superseded")
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(patch, r.Patched) {
		return fmt.Errorf("archive pending patch differs from operation")
	}
	if r.Completion != nil {
		if err := verifyArchiveCompletion(*r.Completion, r.BoardPath, r.Target, r.Patched); err != nil {
			return err
		}
	}
	return nil
}

func validateArchiveRecordCompletion(r archiveRecord) error {
	b := r.Completion
	if err := validateArchiveCompletion(*b); err != nil {
		return err
	}
	if (r.Operation != "archive" && r.Operation != "legacy-adoption") || (r.Operation == "archive" && b.Provenance != "workflow-done") || (r.Operation == "legacy-adoption" && b.Provenance != "legacy-completion") {
		return fmt.Errorf("archive operation cannot publish this completion provenance")
	}
	if b.RequestID != r.RequestID || b.BoardPath != r.BoardPath || b.ID != r.ID || b.Source != r.Source || b.Target != r.Target || b.FinalSHA256 != r.FinalSHA256 || b.PolicyDigest != r.PolicyDigest || b.RulesDigest != r.RulesDigest || b.Assertion != r.Assertion || !bytes.Equal(b.PolicyCanonical, r.PolicyCanonical) || !bytes.Equal(b.RulesCanonical, r.RulesCanonical) {
		return fmt.Errorf("archive receipt and completion binding disagree")
	}
	return nil
}
