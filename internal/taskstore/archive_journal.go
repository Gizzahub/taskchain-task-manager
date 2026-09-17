package taskstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type archiveJournal struct {
	SchemaVersion int             `json:"schemaVersion"`
	BoardPath     string          `json:"boardPath"`
	Namespace     string          `json:"namespace"`
	Records       []archiveRecord `json:"records"`
}

func decodeArchiveJournal(raw []byte) (archiveJournal, error) {
	return decodeArchiveJournalVersion(raw, 1)
}

func decodeArchiveJournalVersion(raw []byte, maxSchema int) (archiveJournal, error) {
	var j archiveJournal
	if len(raw) > archiveJournalVersionLimit(maxSchema) || !utf8.Valid(raw) {
		return j, fmt.Errorf("archive journal size or UTF-8 invalid")
	}
	if err := rejectJSONSurrogates(raw); err != nil {
		return j, err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return j, err
	}
	if err := relocationShape(raw, []string{"schemaVersion", "boardPath", "namespace", "records"}, nil); err != nil {
		return j, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, err
	}
	if j.SchemaVersion < 1 || j.SchemaVersion > maxSchema || len(raw) > archiveJournalVersionLimit(j.SchemaVersion) {
		return j, fmt.Errorf("archive journal schema or version-specific size invalid")
	}
	var wire struct{ Records []json.RawMessage }
	if err := json.Unmarshal(raw, &wire); err != nil {
		return j, err
	}
	for i, r := range j.Records {
		keys := []string{"state", "operation", "requestId", "id", "owner", "token", "boardPath", "namespace", "source", "target", "originalSha256", "finalSha256", "mode", "policyCanonical", "policyDigest", "rulesCanonical", "rulesDigest", "assertion"}
		payloads := []string{"policyCanonical", "rulesCanonical"}
		if r.State == "pending" {
			keys = append(keys, "original", "patched")
			payloads = append(payloads, "original", "patched")
		}
		if err := relocationShape(wire.Records[i], keys, []string{"completion"}); err != nil {
			return j, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(wire.Records[i], &fields); err != nil {
			return j, err
		}
		for _, key := range payloads {
			if bytes.TrimSpace(fields[key])[0] != '"' {
				return j, fmt.Errorf("archive %s must be a base64 string", key)
			}
		}
		if payload, ok := fields["completion"]; ok {
			if _, err := decodeArchiveCompletion(payload); err != nil {
				return j, err
			}
		}
	}
	return j, validateArchiveJournalVersion(j, maxSchema)
}

func validateArchiveJournal(j archiveJournal) error {
	return validateArchiveJournalVersion(j, 1)
}

func validateArchiveJournalVersion(j archiveJournal, maxSchema int) error {
	if j.SchemaVersion < 1 || j.SchemaVersion > maxSchema || archiveJournalVersionLimit(j.SchemaVersion) == 0 || !utf8.ValidString(j.BoardPath) || !canonicalBoardPath(j.BoardPath) || (j.Namespace != "" && !sharedHex32.MatchString(j.Namespace)) || j.Records == nil {
		return fmt.Errorf("invalid archive journal scope or schema")
	}
	requests, identities := map[string]bool{}, map[string]bool{}
	for i, r := range j.Records {
		if r.BoardPath != j.BoardPath || r.Namespace != j.Namespace {
			return fmt.Errorf("archive record scope differs from journal")
		}
		if err := validateArchiveRecord(r); err != nil {
			return err
		}
		if requests[r.RequestID] || identities[identityKey(r.ID)] {
			return fmt.Errorf("duplicate archive request or identity")
		}
		requests[r.RequestID], identities[identityKey(r.ID)] = true, true
		if r.State == "pending" && i != len(j.Records)-1 {
			return fmt.Errorf("pending archive must be the final record")
		}
	}
	return nil
}

func archiveJournalBytes(j archiveJournal) ([]byte, error) {
	return archiveJournalBytesVersion(j, 1)
}

func archiveJournalBytesVersion(j archiveJournal, maxSchema int) ([]byte, error) {
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := decodeArchiveJournalVersion(raw, maxSchema); err != nil {
		return nil, err
	}
	return raw, nil
}

// archiveCompletedBindings exposes no binding if any transaction is pending.
// The expected scope comes from the acquired board/common session, not from
// this journal. This function does not discover or verify the current cards.
func archiveCompletedBindings(j archiveJournal, board, namespace string) (map[string]archiveCompletionBinding, error) {
	if err := validateArchiveJournalVersion(j, j.SchemaVersion); err != nil {
		return nil, err
	}
	if j.BoardPath != board || j.Namespace != namespace {
		return nil, fmt.Errorf("archive journal does not belong to current board session")
	}
	out := map[string]archiveCompletionBinding{}
	for _, r := range j.Records {
		if r.State != "completed" {
			return nil, fmt.Errorf("archive transaction pending; recover before dependency lookup")
		}
		if r.Completion != nil {
			b := *r.Completion
			b.PolicyCanonical = append([]byte(nil), b.PolicyCanonical...)
			b.RulesCanonical = append([]byte(nil), b.RulesCanonical...)
			out[b.Identity] = b
		}
	}
	return out, nil
}
