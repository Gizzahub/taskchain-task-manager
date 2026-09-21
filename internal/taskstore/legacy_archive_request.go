package taskstore

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// LegacyArchiveRequest records an operator's explicit observation of an
// already archived card. It never authorizes a file move or infers completion.
type LegacyArchiveRequest struct {
	ArchiveRequest
	ExpectedMode      uint32
	ApproveCompletion bool
}

func validateLegacyArchiveRequest(req LegacyArchiveRequest, actualMode uint32, policy boardpolicy.Policy, board, namespace string) ([]byte, error) {
	if req.Operation != "legacy-adoption" {
		return nil, fmt.Errorf("legacy archive operation is required")
	}
	common := req.ArchiveRequest
	common.Operation = string(outputvocab.Force)
	if err := validateArchiveRequest(common); err != nil {
		return nil, err
	}
	if req.ExpectedMode == 0 || req.ExpectedMode&^uint32(0o777) != 0 || actualMode != req.ExpectedMode {
		return nil, fmt.Errorf("legacy archive mode does not match expected mode")
	}
	if !canonicalBoardPath(board) || (namespace != "" && !sharedHex32.MatchString(namespace)) {
		return nil, fmt.Errorf("invalid legacy archive board or namespace")
	}
	if err := validateArchivedBindingPath(req.Source, policy); err != nil {
		return nil, err
	}
	rules, err := archivepolicy.ParseConfig(req.Rules)
	if err != nil {
		return nil, err
	}
	rulesCanonical, err := rules.Canonical()
	if err != nil || len(rulesCanonical) == 0 {
		return nil, fmt.Errorf("invalid legacy archive rules")
	}
	return rulesCanonical, nil
}

// prepareLegacyArchiveRecord creates a pending, source-preserving adoption
// record. The caller supplies the locked board's current mode and identity.
func prepareLegacyArchiveRecord(req LegacyArchiveRequest, raw []byte, actualMode uint32, policy boardpolicy.Policy, board, namespace string) (archiveRecord, error) {
	var zero archiveRecord
	if req.Operation != "legacy-adoption" {
		return zero, fmt.Errorf("legacy archive operation is required")
	}
	if strings.TrimSpace(req.Assertion) == "" || !utf8.ValidString(req.Assertion) {
		return zero, fmt.Errorf("legacy archive requires an explicit assertion")
	}
	if len(raw) == 0 || len(raw) > maxCardBytes || bytesDigest(raw) != req.ExpectedSHA256 {
		return zero, fmt.Errorf("legacy archive source bytes do not match expected hash")
	}
	if req.ApproveCompletion && !isWorkTask(req.ID) {
		return zero, fmt.Errorf("legacy completion requires a TASK")
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return zero, err
	}
	if doc.View().ID != req.ID {
		return zero, fmt.Errorf("legacy archive raw identity mismatch")
	}
	if req.ApproveCompletion && strings.EqualFold(strings.TrimSpace(doc.View().Status), "superseded") {
		return zero, fmt.Errorf("superseded card cannot receive legacy completion")
	}
	rulesCanonical, err := validateLegacyArchiveRequest(req, actualMode, policy, board, namespace)
	if err != nil {
		return zero, err
	}
	pCanonical, err := policy.Canonical()
	if err != nil {
		return zero, err
	}
	record := archiveRecord{
		State: "pending", Operation: string(outputvocab.LegacyAdoption), RequestID: req.RequestID, ID: req.ID,
		Owner: req.Owner, Token: req.Token, BoardPath: board, Namespace: namespace,
		Source: req.Source, Target: req.Source, OriginalSHA256: bytesDigest(raw), FinalSHA256: bytesDigest(raw),
		Mode: actualMode, PolicyCanonical: append([]byte(nil), pCanonical...), PolicyDigest: bytesDigest(pCanonical),
		RulesCanonical: append([]byte(nil), rulesCanonical...), RulesDigest: bytesDigest(rulesCanonical), Assertion: req.Assertion,
		Original: append([]byte(nil), raw...), Patched: append([]byte(nil), raw...),
	}
	if req.ApproveCompletion {
		record.Completion = &archiveCompletionBinding{
			SchemaVersion: 1, Provenance: "legacy-completion", RequestID: req.RequestID, BoardPath: board,
			ID: req.ID, Identity: identityKey(req.ID), Source: req.Source, Target: req.Source,
			FinalSHA256: bytesDigest(raw), PolicyCanonical: append([]byte(nil), pCanonical...), PolicyDigest: bytesDigest(pCanonical),
			RulesCanonical: append([]byte(nil), rulesCanonical...), RulesDigest: bytesDigest(rulesCanonical), Assertion: req.Assertion,
		}
	}
	if err := validateArchiveRecord(record); err != nil {
		return zero, err
	}
	return record, nil
}

func sameLegacyArchive(rec archiveRecord, req LegacyArchiveRequest) bool {
	if !sameArchive(rec, req.ArchiveRequest) || rec.Operation != "legacy-adoption" || rec.Mode != req.ExpectedMode {
		return false
	}
	return (rec.Completion != nil) == req.ApproveCompletion
}
