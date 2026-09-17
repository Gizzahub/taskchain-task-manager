package taskstore

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// This immutable preparation payload is not a transaction or authorization.
// An activation authority must bind its digest and participant scope before
// any filesystem mutation. Version 1 preserves both exact journal byte sets;
// recovery must publish the saved target, never replace it with a new encoding.
const archiveRebindPayloadMagic = "TCARNS\x00\x01"
const archiveRebindPayloadMagicV2 = "TCARNS\x00\x02"
const archiveRebindPayloadHeader = len(archiveRebindPayloadMagic) + 8 + 8 + 4
const archiveRebindPayloadHeaderV2 = archiveRebindPayloadHeader + 4
const maxArchiveRebindPayloadBytes = archiveRebindPayloadHeaderV2 + maxArchiveCapacityBytes + maxArchiveCapacityBytes

func archiveRebindPayloadBytes(plan ArchiveNamespaceRebindPlan) ([]byte, error) {
	if err := ValidateArchiveNamespaceRebindPlan(plan); err != nil {
		return nil, err
	}
	schema := 1
	if j, err := decodeArchiveCapacityJournal(plan.OriginalJournal); err == nil {
		schema = j.SchemaVersion
	}
	header, magic := archiveRebindPayloadHeader, archiveRebindPayloadMagic
	if schema == 2 {
		header, magic = archiveRebindPayloadHeaderV2, archiveRebindPayloadMagicV2
	}
	raw := make([]byte, header, header+len(plan.OriginalJournal)+len(plan.TargetJournal))
	copy(raw, magic)
	binary.BigEndian.PutUint64(raw[8:16], uint64(len(plan.OriginalJournal)))
	binary.BigEndian.PutUint64(raw[16:24], uint64(len(plan.TargetJournal)))
	binary.BigEndian.PutUint32(raw[24:28], plan.JournalMode)
	if schema == 2 {
		binary.BigEndian.PutUint32(raw[28:32], 2)
	}
	raw = append(raw, plan.OriginalJournal...)
	raw = append(raw, plan.TargetJournal...)
	return raw, nil
}

// decodeArchiveRebindPayload only verifies saved preparation data. A valid
// payload alone never grants permission to adopt a board or resume activation.
// The caller must obtain expectedDigest from the authoritative activation plan,
// then compare the returned board, namespaces, mode and hashes to that plan.
func decodeArchiveRebindPayload(raw []byte, expectedDigest string) (ArchiveNamespaceRebindPlan, error) {
	var zero ArchiveNamespaceRebindPlan
	if len(raw) < archiveRebindPayloadHeader || len(raw) > maxArchiveRebindPayloadBytes {
		return zero, fmt.Errorf("archive rebind payload size invalid")
	}
	if !sharedHex64.MatchString(expectedDigest) || bytesDigest(raw) != expectedDigest {
		return zero, fmt.Errorf("archive rebind payload digest mismatch")
	}
	header, schema := archiveRebindPayloadHeader, 1
	if string(raw[:8]) == archiveRebindPayloadMagicV2 && len(raw) >= archiveRebindPayloadHeaderV2 && binary.BigEndian.Uint32(raw[28:32]) == 2 {
		header, schema = archiveRebindPayloadHeaderV2, 2
	}
	if string(raw[:8]) != archiveRebindPayloadMagic && schema != 2 {
		return zero, fmt.Errorf("unsupported archive rebind payload version")
	}
	originalSize := binary.BigEndian.Uint64(raw[8:16])
	targetSize := binary.BigEndian.Uint64(raw[16:24])
	// Check individual bounds before addition or conversion to int.
	limit := uint64(maxRepairsBytes)
	if schema == 2 {
		limit = uint64(maxArchiveCapacityBytes)
	}
	if originalSize == 0 || originalSize > limit || targetSize == 0 || targetSize > limit {
		return zero, fmt.Errorf("archive rebind payload journal length invalid")
	}
	if schema == 2 && (len(raw) < header || binary.BigEndian.Uint32(raw[28:32]) != 2) {
		return zero, fmt.Errorf("archive rebind payload schema invalid")
	}
	if originalSize+targetSize != uint64(len(raw)-header) {
		return zero, fmt.Errorf("archive rebind payload framing mismatch")
	}
	mode := binary.BigEndian.Uint32(raw[24:28])
	originalRaw := raw[header : header+int(originalSize)]
	targetRaw := raw[header+int(originalSize):]
	original, err := decodeArchiveJournalWire(originalRaw, schema)
	if err != nil {
		return zero, fmt.Errorf("archive rebind payload original: %w", err)
	}
	target, err := decodeArchiveJournalWire(targetRaw, schema)
	if err != nil {
		return zero, fmt.Errorf("archive rebind payload target: %w", err)
	}
	plan, err := PlanArchiveNamespaceRebind(originalRaw, bytesDigest(originalRaw), original.BoardPath, original.Namespace, target.Namespace, mode)
	if err != nil {
		return zero, err
	}
	if !bytes.Equal(plan.TargetJournal, targetRaw) {
		return zero, fmt.Errorf("archive rebind payload target differs from namespace-only transformation")
	}
	// The returned plan owns its bytes, independently of the input buffer.
	plan.TargetJournal = append([]byte(nil), targetRaw...)
	return plan, nil
}
