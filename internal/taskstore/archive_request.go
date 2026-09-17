package taskstore

import (
	"bytes"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

// ArchiveRequest is the pure, writer-independent archive admission request.
type ArchiveRequest struct {
	ID, Owner, Token, RequestID string
	Source, ExpectedSHA256      string
	Operation, Assertion        string
	Rules                       []byte
}

func validateArchiveRequest(req ArchiveRequest) error {
	if identityKey(req.ID) == "" || !utf8.ValidString(req.Owner) || !validClaimOwner(req.Owner) || !claimToken.MatchString(req.RequestID) || !sharedHex64.MatchString(req.ExpectedSHA256) {
		return fmt.Errorf("invalid archive request identity, owner, request or hash")
	}
	if req.Token != "" && !claimToken.MatchString(req.Token) {
		return fmt.Errorf("invalid archive request token")
	}
	if req.Operation != "archive" && req.Operation != "supersede" && req.Operation != "force" {
		return fmt.Errorf("unsupported archive operation %q", req.Operation)
	}
	if !utf8.ValidString(req.Source) || req.Source == "" || req.Source == "." || req.Source == ".." || strings.HasPrefix(req.Source, "../") || len(req.Source) > 1023 || path.IsAbs(req.Source) || path.Clean(req.Source) != req.Source || strings.ContainsAny(req.Source, "\\\x00") {
		return fmt.Errorf("invalid archive source path")
	}
	if !utf8.ValidString(req.Assertion) || len(req.Assertion) > 4096 || strings.ContainsAny(req.Assertion, "\x00\r\n") {
		return fmt.Errorf("invalid archive assertion")
	}
	if req.Operation == "force" && strings.TrimSpace(req.Assertion) == "" {
		return fmt.Errorf("forced archive requires explicit assertion")
	}
	if req.Operation != "force" && req.Assertion != "" {
		return fmt.Errorf("archive assertion is only valid for force")
	}
	cfg, err := archivepolicy.ParseConfig(req.Rules)
	if err != nil {
		return err
	}
	canonical, err := cfg.Canonical()
	if err != nil || len(canonical) == 0 {
		return fmt.Errorf("invalid archive rules")
	}
	return nil
}

func prepareArchiveRecord(req ArchiveRequest, raw []byte, mode uint32, policy boardpolicy.Policy, board, namespace string, resolve func(string) (bool, error)) (archiveRecord, error) {
	var out archiveRecord
	if err := validateArchiveRequest(req); err != nil {
		return out, err
	}
	if len(raw) == 0 || len(raw) > maxCardBytes || bytesDigest(raw) != req.ExpectedSHA256 {
		return out, fmt.Errorf("archive source bytes do not match expected hash")
	}
	if !canonicalBoardPath(board) || (namespace != "" && !sharedHex32.MatchString(namespace)) {
		return out, fmt.Errorf("invalid archive board or namespace")
	}
	pCanonical, err := policy.Canonical()
	if err != nil {
		return out, err
	}
	cfg, err := archivepolicy.ParseConfig(req.Rules)
	if err != nil {
		return out, err
	}
	rulesCanonical, err := cfg.Canonical()
	if err != nil {
		return out, err
	}
	_, target, err := archiveDestination(req.Source, policy)
	if err != nil {
		return out, err
	}
	doc, err := card.Parse(raw)
	if err != nil || doc.View().ID != req.ID {
		if err != nil {
			return out, err
		}
		return out, fmt.Errorf("archive request raw identity mismatch")
	}
	patched := append([]byte(nil), raw...)
	decision := archivepolicy.Decision{}
	switch req.Operation {
	case "archive":
		decision, err = observeArchiveAdmission(raw, req.ID, req.Source, policy, cfg, resolve)
		if err != nil {
			return out, err
		}
		if !decision.Allowed {
			return out, fmt.Errorf("archive admission denied: %s", strings.Join(decision.Reasons, "; "))
		}
	case "supersede":
		patched, _, err = doc.SetFrontmatterStatus("superseded")
		if err != nil {
			return out, err
		}
	case "force":
		// Force preserves source bytes and records explicit operator provenance.
	}
	out = archiveRecord{
		State: "pending", Operation: req.Operation, RequestID: req.RequestID, ID: req.ID, Owner: req.Owner, Token: req.Token,
		BoardPath: board, Namespace: namespace, Source: req.Source, Target: target,
		OriginalSHA256: bytesDigest(raw), FinalSHA256: bytesDigest(patched), Mode: mode,
		PolicyCanonical: append([]byte(nil), pCanonical...), PolicyDigest: bytesDigest(pCanonical),
		RulesCanonical: append([]byte(nil), rulesCanonical...), RulesDigest: bytesDigest(rulesCanonical), Assertion: req.Assertion,
		Original: append([]byte(nil), raw...), Patched: append([]byte(nil), patched...),
	}
	if req.Operation == "archive" && decision.CompletionEligible {
		out.Completion = &archiveCompletionBinding{SchemaVersion: 1, Provenance: "workflow-done", RequestID: req.RequestID, BoardPath: board, ID: req.ID, Identity: identityKey(req.ID), Source: req.Source, Target: target, FinalSHA256: out.FinalSHA256, PolicyCanonical: append([]byte(nil), pCanonical...), PolicyDigest: bytesDigest(pCanonical), RulesCanonical: append([]byte(nil), rulesCanonical...), RulesDigest: bytesDigest(rulesCanonical)}
	}
	if err := validateArchiveRecord(out); err != nil {
		return archiveRecord{}, err
	}
	return out, nil
}

func sameArchive(rec archiveRecord, req ArchiveRequest) bool {
	if rec.ID != req.ID || rec.Owner != req.Owner || rec.Token != req.Token || rec.RequestID != req.RequestID || rec.Source != req.Source || rec.OriginalSHA256 != req.ExpectedSHA256 || rec.Operation != req.Operation || rec.Assertion != req.Assertion {
		return false
	}
	cfg, err := archivepolicy.ParseConfig(req.Rules)
	if err != nil {
		return false
	}
	canonical, err := cfg.Canonical()
	return err == nil && bytes.Equal(rec.RulesCanonical, canonical)
}
