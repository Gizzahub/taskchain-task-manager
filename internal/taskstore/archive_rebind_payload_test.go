package taskstore

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

func archiveRebindPayloadFixture(t *testing.T) ArchiveNamespaceRebindPlan {
	t.Helper()
	j, rec, completion, _ := archiveJournalFixture(t)
	j.Namespace, rec.Namespace = "", ""
	rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, &completion
	j.Records = []archiveRecord{rec}
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, "", strings.Repeat("b", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestArchiveRebindPayloadV2RoundTripAndArtifactRecoveryAboveLegacyLimit(t *testing.T) {
	t.Parallel()
	base := archiveRebindPayloadFixture(t)
	j, err := decodeArchiveJournal(base.OriginalJournal)
	if err != nil {
		t.Fatal(err)
	}
	j.SchemaVersion = 2
	original, err := archiveCapacityJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	original = append(original, bytes.Repeat([]byte{' '}, maxRepairsBytes+1-len(original))...)
	if len(original) <= maxRepairsBytes {
		t.Fatal("schema-2 fixture did not exceed legacy limit")
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, j.Namespace, base.TargetNamespace, base.JournalMode)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeArchiveRebindPayload(payload, bytesDigest(payload))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.OriginalJournal, original) || decoded.TargetJournalSHA256 != plan.TargetJournalSHA256 {
		t.Fatal("schema-2 payload round trip changed bytes")
	}
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	digest, err := publishArchiveRebindArtifact(r, plan)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := loadArchiveRebindArtifact(r, digest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(recovered.OriginalJournal, original) || !bytes.Equal(recovered.TargetJournal, plan.TargetJournal) {
		t.Fatal("schema-2 artifact recovery changed bytes")
	}
}

func TestArchiveRebindPayloadRoundTripExactBytesAndScope(t *testing.T) {
	t.Parallel()
	plan := archiveRebindPayloadFixture(t)
	raw, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeArchiveRebindPayload(raw, bytesDigest(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.OriginalJournal, plan.OriginalJournal) || !bytes.Equal(decoded.TargetJournal, plan.TargetJournal) || decoded.TargetNamespace != plan.TargetNamespace || decoded.SourceNamespace != plan.SourceNamespace || decoded.BoardPath != plan.BoardPath || decoded.OriginalJournalSHA256 != plan.OriginalJournalSHA256 || decoded.JournalMode != plan.JournalMode || decoded.TargetJournalSHA256 != bytesDigest(plan.TargetJournal) {
		t.Fatalf("decoded plan differs: got=%+v want=%+v", decoded, plan)
	}
	if len(decoded.Cards) != 1 || decoded.Cards[0].Path != plan.Cards[0].Path || decoded.Cards[0].ID != plan.Cards[0].ID || decoded.Cards[0].SHA256 != plan.Cards[0].SHA256 || decoded.Cards[0].Mode != plan.Cards[0].Mode {
		t.Fatalf("decoded card binding differs: %+v", decoded.Cards)
	}
	raw[archiveRebindPayloadHeader] ^= 1
	if bytes.Equal(decoded.OriginalJournal, raw[archiveRebindPayloadHeader:archiveRebindPayloadHeader+len(decoded.OriginalJournal)]) {
		t.Fatal("decoded plan aliases payload input")
	}
	raw[archiveRebindPayloadHeader+len(decoded.OriginalJournal)] ^= 1
	if bytes.Equal(decoded.TargetJournal, raw[archiveRebindPayloadHeader+len(decoded.OriginalJournal):]) {
		t.Fatal("decoded target aliases payload input")
	}
}

func TestArchiveRebindPayloadRejectsFramingDigestAndMode(t *testing.T) {
	t.Parallel()
	plan := archiveRebindPayloadFixture(t)
	payload, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(t *testing.T, name, want string, fn func([]byte)) {
		t.Helper()
		candidate := append([]byte(nil), payload...)
		fn(candidate)
		if _, err := decodeArchiveRebindPayload(candidate, bytesDigest(candidate)); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s err=%v want substring=%q", name, err, want)
		}
	}
	mutate(t, "magic", "unsupported archive rebind payload version", func(raw []byte) { raw[0] ^= 1 })
	mutate(t, "version", "unsupported archive rebind payload version", func(raw []byte) { raw[7] = 2 })
	trailing := append(append([]byte(nil), payload...), 0)
	if _, err := decodeArchiveRebindPayload(trailing, bytesDigest(trailing)); err == nil || !strings.Contains(err.Error(), "framing mismatch") {
		t.Fatalf("trailing payload err=%v", err)
	}
	mutate(t, "mode", "invalid archive rebind journal mode", func(raw []byte) { raw[24], raw[25], raw[26], raw[27] = 0, 0, 0, 0 })
	mutate(t, "original zero", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[8:16], 0) })
	mutate(t, "target zero", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[16:24], 0) })
	mutate(t, "original overflow", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[8:16], ^uint64(0)) })
	mutate(t, "target overflow", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[16:24], ^uint64(0)) })
	mutate(t, "original over max", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[8:16], uint64(maxRepairsBytes+1)) })
	mutate(t, "target over max", "archive rebind payload journal length invalid", func(raw []byte) { binary.BigEndian.PutUint64(raw[16:24], uint64(maxRepairsBytes+1)) })
	if _, err := decodeArchiveRebindPayload(payload, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatal("wrong expected payload digest accepted")
	}
	short := payload[:archiveRebindPayloadHeader-1]
	if _, err := decodeArchiveRebindPayload(short, bytesDigest(short)); err == nil || !strings.Contains(err.Error(), "size invalid") {
		t.Fatalf("short header err=%v", err)
	}
}

func TestArchiveRebindPayloadRejectsMalformedJournalsAndForgedTarget(t *testing.T) {
	t.Parallel()
	plan := archiveRebindPayloadFixture(t)
	validTarget := append([]byte(nil), plan.TargetJournal...)
	build := func(original, target []byte, mode uint32) []byte {
		raw := make([]byte, archiveRebindPayloadHeader, archiveRebindPayloadHeader+len(original)+len(target))
		copy(raw, archiveRebindPayloadMagic)
		binary.BigEndian.PutUint64(raw[8:16], uint64(len(original)))
		binary.BigEndian.PutUint64(raw[16:24], uint64(len(target)))
		binary.BigEndian.PutUint32(raw[24:28], mode)
		return append(append(raw, original...), target...)
	}
	for name, malformed := range map[string][]byte{
		"malformed original": []byte("{}"),
		"malformed target":   []byte("{}"),
	} {
		t.Run(name, func(t *testing.T) {
			o, tg := plan.OriginalJournal, validTarget
			if name == "malformed target" {
				tg = malformed
			} else {
				o = malformed
			}
			raw := build(o, tg, plan.JournalMode)
			if _, err := decodeArchiveRebindPayload(raw, bytesDigest(raw)); err == nil {
				t.Fatal("malformed journal accepted")
			}
		})
	}
	decoded, err := decodeArchiveJournal(validTarget)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Records[0].Owner = "forged-owner"
	forgedTarget, err := archiveJournalBytes(decoded)
	if err != nil {
		t.Fatal(err)
	}
	forged := build(plan.OriginalJournal, forgedTarget, plan.JournalMode)
	if _, err := decodeArchiveRebindPayload(forged, bytesDigest(forged)); err == nil || !strings.Contains(err.Error(), "transformation") {
		t.Fatalf("forged internally valid target err=%v", err)
	}
	forgedPlan := plan
	forgedPlan.TargetJournal = forgedTarget
	forgedPlan.TargetJournalSHA256 = bytesDigest(forgedTarget)
	if _, err := archiveRebindPayloadBytes(forgedPlan); err == nil || !strings.Contains(err.Error(), "transformation") {
		t.Fatalf("encoder accepted forged target err=%v", err)
	}
}

func TestArchiveRebindPayloadAcceptsNearMaxOriginalJournal(t *testing.T) {
	t.Parallel()
	base := archiveRebindPayloadFixture(t)
	padded := append([]byte(nil), base.OriginalJournal...)
	padded = append(padded, bytes.Repeat([]byte{' '}, maxRepairsBytes-len(padded))...)
	plan, err := PlanArchiveNamespaceRebind(padded, bytesDigest(padded), base.BoardPath, "", base.TargetNamespace, base.JournalMode)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.OriginalJournal) <= 4*1024*1024 {
		t.Fatal("near-max fixture did not exceed old 4 MiB boundary")
	}
	payload, err := archiveRebindPayloadBytes(plan)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeArchiveRebindPayload(payload, bytesDigest(payload))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.OriginalJournal) != maxRepairsBytes || !bytes.Equal(decoded.OriginalJournal, padded) {
		t.Fatal("near-max original bytes were not preserved exactly")
	}
}
