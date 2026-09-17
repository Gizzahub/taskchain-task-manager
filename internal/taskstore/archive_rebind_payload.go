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
const archiveRebindPayloadHeader = len(archiveRebindPayloadMagic) + 8 + 8 + 4
const maxArchiveRebindPayloadBytes = archiveRebindPayloadHeader + 2*maxRepairsBytes

func archiveRebindPayloadBytes(plan ArchiveNamespaceRebindPlan) ([]byte, error) {
	if err := ValidateArchiveNamespaceRebindPlan(plan); err != nil {
		return nil, err
	}
	raw := make([]byte, archiveRebindPayloadHeader, archiveRebindPayloadHeader+len(plan.OriginalJournal)+len(plan.TargetJournal))
	copy(raw, archiveRebindPayloadMagic)
	binary.BigEndian.PutUint64(raw[8:16], uint64(len(plan.OriginalJournal)))
	binary.BigEndian.PutUint64(raw[16:24], uint64(len(plan.TargetJournal)))
	binary.BigEndian.PutUint32(raw[24:28], plan.JournalMode)
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
	if string(raw[:8]) != archiveRebindPayloadMagic {
		return zero, fmt.Errorf("unsupported archive rebind payload version")
	}
	originalSize := binary.BigEndian.Uint64(raw[8:16])
	targetSize := binary.BigEndian.Uint64(raw[16:24])
	// Check individual bounds before addition or conversion to int.
	if originalSize == 0 || originalSize > maxRepairsBytes || targetSize == 0 || targetSize > maxRepairsBytes {
		return zero, fmt.Errorf("archive rebind payload journal length invalid")
	}
	if originalSize+targetSize != uint64(len(raw)-archiveRebindPayloadHeader) {
		return zero, fmt.Errorf("archive rebind payload framing mismatch")
	}
	mode := binary.BigEndian.Uint32(raw[24:28])
	originalRaw := raw[archiveRebindPayloadHeader : archiveRebindPayloadHeader+int(originalSize)]
	targetRaw := raw[archiveRebindPayloadHeader+int(originalSize):]
	original, err := decodeArchiveJournal(originalRaw)
	if err != nil {
		return zero, fmt.Errorf("archive rebind payload original: %w", err)
	}
	target, err := decodeArchiveJournal(targetRaw)
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
