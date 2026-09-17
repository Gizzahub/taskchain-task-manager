package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// archivePendingDelta is the protocol 4/5 shared transport. JournalSchema binds
// all three states to one canonical archive schema version.
type archivePendingDelta struct {
	SchemaVersion          int    `json:"schemaVersion"`
	JournalSchema          int    `json:"journalSchema"`
	BoardPath              string `json:"boardPath"`
	Namespace              string `json:"namespace"`
	RequestID              string `json:"requestId"`
	Owner                  string `json:"owner"`
	ID                     string `json:"id"`
	OriginalJournalSHA256  string `json:"originalJournalSha256"`
	TargetJournalSHA256    string `json:"targetJournalSha256"`
	CompletedJournalSHA256 string `json:"completedJournalSha256"`
	PendingRecord          []byte `json:"pendingRecord"`
}

type archiveDeltaResolution struct {
	State     string
	Pending   archiveRecord
	Target    []byte
	Completed []byte
}

func prepareArchivePendingDelta(original archiveJournal, pending archiveRecord) (archivePendingDelta, error) {
	var zero archivePendingDelta
	if !sharedHex32.MatchString(original.Namespace) || pending.State != "pending" {
		return zero, errors.New("archive delta requires a shared namespace and pending record")
	}
	for _, rec := range original.Records {
		if rec.State != "completed" {
			return zero, errors.New("archive delta original contains pending record")
		}
	}
	originalRaw, err := archiveJournalWire(original)
	if err != nil {
		return zero, err
	}
	target := original
	target.Records = append(append([]archiveRecord{}, original.Records...), pending)
	targetRaw, err := archiveJournalWire(target)
	if err != nil {
		return zero, err
	}
	completedRaw, err := archiveJournalWire(completedArchiveJournal(target))
	if err != nil {
		return zero, err
	}
	recordRaw, err := json.Marshal(pending)
	if err != nil {
		return zero, err
	}
	d := archivePendingDelta{
		SchemaVersion: 1, JournalSchema: original.SchemaVersion,
		BoardPath: original.BoardPath, Namespace: original.Namespace,
		RequestID: pending.RequestID, Owner: pending.Owner, ID: pending.ID,
		OriginalJournalSHA256: bytesDigest(originalRaw), TargetJournalSHA256: bytesDigest(targetRaw),
		CompletedJournalSHA256: bytesDigest(completedRaw), PendingRecord: recordRaw,
	}
	if _, err := archivePendingDeltaBytes(d); err != nil {
		return zero, err
	}
	return d, nil
}

func archiveDeltaRecord(d archivePendingDelta) (archiveRecord, error) {
	var zero archiveRecord
	if d.SchemaVersion != 1 || (d.JournalSchema != 1 && d.JournalSchema != 2) || !validSharedRoot(d.BoardPath) || !sharedHex32.MatchString(d.Namespace) || !sharedHex32.MatchString(d.RequestID) || !sharedHex64.MatchString(d.OriginalJournalSHA256) || !sharedHex64.MatchString(d.TargetJournalSHA256) || !sharedHex64.MatchString(d.CompletedJournalSHA256) {
		return zero, errors.New("invalid archive delta version, scope or hash")
	}
	if len(d.PendingRecord) == 0 || len(d.PendingRecord) > maxRepairsBytes || !utf8.Valid(d.PendingRecord) {
		return zero, errors.New("archive delta record size invalid")
	}
	// Reuse strict record shape, duplicate-key, payload and history validation.
	wrapper, err := json.Marshal(struct {
		SchemaVersion int               `json:"schemaVersion"`
		BoardPath     string            `json:"boardPath"`
		Namespace     string            `json:"namespace"`
		Records       []json.RawMessage `json:"records"`
	}{d.JournalSchema, d.BoardPath, d.Namespace, []json.RawMessage{d.PendingRecord}})
	if err != nil {
		return zero, err
	}
	j, err := decodeArchiveJournalWire(wrapper, d.JournalSchema)
	if err != nil {
		return zero, err
	}
	rec := j.Records[0]
	canonical, err := json.Marshal(rec)
	if err != nil || !bytes.Equal(canonical, d.PendingRecord) || rec.State != "pending" || rec.RequestID != d.RequestID || rec.Owner != d.Owner || rec.ID != d.ID {
		return zero, errors.New("archive delta record is not canonical or differs from envelope")
	}
	return rec, nil
}

func archivePendingDeltaBytes(d archivePendingDelta) ([]byte, error) {
	if _, err := archiveDeltaRecord(d); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxSharedStateBytes {
		return nil, errors.New("archive delta exceeds shared envelope limit")
	}
	return raw, nil
}

func decodeArchivePendingDelta(raw []byte) (archivePendingDelta, error) {
	var d archivePendingDelta
	if len(raw) == 0 || len(raw) > maxSharedStateBytes || !utf8.Valid(raw) {
		return d, errors.New("archive delta size invalid")
	}
	if err := rejectJSONSurrogates(raw); err != nil {
		return d, err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return d, err
	}
	keys := []string{"schemaVersion", "journalSchema", "boardPath", "namespace", "requestId", "owner", "id", "originalJournalSha256", "targetJournalSha256", "completedJournalSha256", "pendingRecord"}
	if err := relocationShape(raw, keys, nil); err != nil {
		return d, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return d, err
	}
	if bytes.TrimSpace(fields["pendingRecord"])[0] != '"' {
		return d, errors.New("archive delta record must be a base64 string")
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		return d, err
	}
	_, err := archiveDeltaRecord(d)
	return d, err
}

// resolveArchivePendingDelta never creates a missing original. Every accepted
// state must prove the same original history, pending target and completed
// target. The caller must additionally verify authority, request/claim, policy,
// current cards and legacy approval/mode before performing any mutation.
func resolveArchivePendingDelta(d archivePendingDelta, currentRaw []byte, board, namespace string, reserved []string) (archiveDeltaResolution, error) {
	var zero archiveDeltaResolution
	pending, err := archiveDeltaRecord(d)
	if err != nil {
		return zero, err
	}
	if d.BoardPath != board || d.Namespace != namespace {
		return zero, errors.New("archive delta differs from current board or namespace")
	}
	found := false
	for _, id := range reserved {
		found = found || sameIdentity(id, pending.ID)
	}
	if !found {
		return zero, errors.New("archive delta ID is not reserved")
	}
	current, err := decodeArchiveJournalWire(currentRaw, d.JournalSchema)
	if err != nil {
		return zero, err
	}
	if current.SchemaVersion != d.JournalSchema || current.BoardPath != board || current.Namespace != namespace {
		return zero, errors.New("archive delta current journal scope or schema differs")
	}
	canonical, err := archiveJournalWire(current)
	if err != nil {
		return zero, err
	}
	state := ""
	switch bytesDigest(canonical) {
	case d.OriginalJournalSHA256:
		state = "original"
	case d.TargetJournalSHA256:
		state = "pending"
	case d.CompletedJournalSHA256:
		state = "completed"
	default:
		return zero, errors.New("archive delta current journal is a third state")
	}
	base := current
	if state != "original" {
		if len(current.Records) == 0 {
			return zero, errors.New("archive delta progressed journal has no final record")
		}
		base.Records = append([]archiveRecord{}, current.Records[:len(current.Records)-1]...)
	}
	derived, err := prepareArchivePendingDelta(base, pending)
	if err != nil {
		return zero, err
	}
	if derived.OriginalJournalSHA256 != d.OriginalJournalSHA256 || derived.TargetJournalSHA256 != d.TargetJournalSHA256 || derived.CompletedJournalSHA256 != d.CompletedJournalSHA256 {
		return zero, errors.New("archive delta does not preserve exact original, pending and completed journals")
	}
	target := base
	target.Records = append(append([]archiveRecord{}, base.Records...), pending)
	targetRaw, err := archiveJournalWire(target)
	if err != nil {
		return zero, err
	}
	completedRaw, err := archiveJournalWire(completedArchiveJournal(target))
	if err != nil {
		return zero, err
	}
	return archiveDeltaResolution{State: state, Pending: pending, Target: targetRaw, Completed: completedRaw}, nil
}
