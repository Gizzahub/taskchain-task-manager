package taskstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func deltaHistoryFixture(t *testing.T) (archiveJournal, archiveRecord) {
	t.Helper()
	original, pending, _ := archiveDeltaFixture(t)
	history := pending
	history.ID, history.RequestID = "TASK-002", strings.Repeat("e", 32)
	history.Source = strings.ReplaceAll(history.Source, "TASK-001", "TASK-002")
	history.Target = strings.ReplaceAll(history.Target, "TASK-001", "TASK-002")
	history.Operation, history.Assertion, history.Completion = "force", "historical operator assertion", nil
	history.State, history.Original, history.Patched = "completed", nil, nil
	original.Records = []archiveRecord{history}
	if _, err := archiveJournalBytes(original); err != nil {
		t.Fatal(err)
	}
	return original, pending
}

func deltaJournalBytes(t *testing.T, j archiveJournal) []byte {
	t.Helper()
	raw, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestArchiveDeltaPreservesHistoryAndInputAcrossAllStates(t *testing.T) {
	t.Parallel()
	original, pending := deltaHistoryFixture(t)
	before := deltaJournalBytes(t, original)
	d, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	target := original
	target.Records = append(append([]archiveRecord{}, original.Records...), pending)
	targetRaw := deltaJournalBytes(t, target)
	completedRaw := deltaJournalBytes(t, completedArchiveJournal(target))
	for state, raw := range map[string][]byte{"original": before, "pending": targetRaw, "completed": completedRaw} {
		t.Run(state, func(t *testing.T) {
			input := append([]byte(" \n"), raw...)
			snapshot := append([]byte(nil), input...)
			resolved, err := resolveArchivePendingDelta(d, input, d.BoardPath, d.Namespace, []string{d.ID, "TASK-002"})
			if err != nil || resolved.State != state || !bytes.Equal(resolved.Target, targetRaw) || !bytes.Equal(resolved.Completed, completedRaw) {
				t.Fatalf("state=%s resolution=%s err=%v", state, resolved.State, err)
			}
			resolved.Target[0] ^= 1
			resolved.Completed[0] ^= 1
			resolved.Pending.Original[0] ^= 1
			if !bytes.Equal(input, snapshot) {
				t.Fatal("resolution aliases caller journal bytes")
			}
			if _, err := archiveDeltaRecord(d); err != nil {
				t.Fatalf("resolution aliases saved delta record: %v", err)
			}
		})
	}
	if !bytes.Equal(before, deltaJournalBytes(t, original)) {
		t.Fatal("preparation mutated original history")
	}
}

func TestArchiveDeltaRejectsHistoryReplacementDespiteMatchingCurrentHash(t *testing.T) {
	t.Parallel()
	original, pending := deltaHistoryFixture(t)
	d, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	target := original
	target.Records = append(append([]archiveRecord{}, original.Records...), pending)
	for state, current := range map[string]archiveJournal{"original": original, "pending": target, "completed": completedArchiveJournal(target)} {
		t.Run(state, func(t *testing.T) {
			baseline := deltaJournalBytes(t, current)
			if _, err := resolveArchivePendingDelta(d, baseline, d.BoardPath, d.Namespace, []string{d.ID}); err != nil {
				t.Fatal(err)
			}
			forged := current
			forged.Records = append([]archiveRecord{}, current.Records...)
			forged.Records[0].Owner = "replacement-history-owner"
			raw := deltaJournalBytes(t, forged)
			candidate := d
			switch state {
			case "original":
				candidate.OriginalJournalSHA256 = bytesDigest(raw)
			case "pending":
				candidate.TargetJournalSHA256 = bytesDigest(raw)
			case "completed":
				candidate.CompletedJournalSHA256 = bytesDigest(raw)
			}
			if _, err := resolveArchivePendingDelta(candidate, raw, d.BoardPath, d.Namespace, []string{d.ID}); err == nil || !strings.Contains(err.Error(), "preserve exact") {
				t.Fatalf("matched current hash bypassed history verification: %v", err)
			}
		})
	}
}

func TestArchiveDeltaLegacyApprovalAndModeRemainBound(t *testing.T) {
	t.Parallel()
	original, pending := deltaHistoryFixture(t)
	pending.Operation, pending.Source, pending.Assertion = "legacy-adoption", pending.Target, "explicit legacy completion approval"
	completion := *pending.Completion
	completion.Provenance, completion.Source, completion.Assertion = "legacy-completion", pending.Target, pending.Assertion
	pending.Completion = &completion
	d, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	target := original
	target.Records = append(append([]archiveRecord{}, original.Records...), pending)
	states := []archiveJournal{original, target, completedArchiveJournal(target)}
	for _, current := range states {
		raw := deltaJournalBytes(t, current)
		if _, err := resolveArchivePendingDelta(d, raw, d.BoardPath, d.Namespace, []string{d.ID}); err != nil {
			t.Fatal(err)
		}
		for name, mutate := range map[string]func(*archiveRecord){
			"approval": func(r *archiveRecord) { r.Completion = nil },
			"mode":     func(r *archiveRecord) { r.Mode ^= 0100 },
		} {
			changed := pending
			mutate(&changed)
			candidate := d
			candidate.PendingRecord, err = json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := archiveDeltaRecord(candidate); err != nil {
				t.Fatalf("%s mutation did not form a valid independent record: %v", name, err)
			}
			if _, err := resolveArchivePendingDelta(candidate, raw, d.BoardPath, d.Namespace, []string{d.ID}); err == nil || !strings.Contains(err.Error(), "preserve exact") {
				t.Fatalf("%s mutation bypassed recorded hashes: %v", name, err)
			}
		}
	}
}

func TestArchiveDeltaPreparationRejectsExistingIdentityRequestAndPending(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"identity", "request", "pending"} {
		t.Run(field, func(t *testing.T) {
			original, pending := deltaHistoryFixture(t)
			if _, err := prepareArchivePendingDelta(original, pending); err != nil {
				t.Fatal(err)
			}
			want := "duplicate"
			switch field {
			case "identity":
				original.Records[0].ID = pending.ID
			case "request":
				original.Records[0].RequestID = pending.RequestID
			case "pending":
				history := &original.Records[0]
				history.State = "pending"
				history.Original = bytes.ReplaceAll(pending.Original, []byte("TASK-001"), []byte("TASK-002"))
				history.Patched = append([]byte(nil), history.Original...)
				history.OriginalSHA256, history.FinalSHA256 = bytesDigest(history.Original), bytesDigest(history.Patched)
				want = "original contains pending"
			}
			// Each source remains a structurally valid journal on its own.
			_ = deltaJournalBytes(t, original)
			if _, err := prepareArchivePendingDelta(original, pending); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("%s conflict err=%v", field, err)
			}
		})
	}
}
