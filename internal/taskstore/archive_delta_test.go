package taskstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func archiveDeltaFixture(t *testing.T) (archiveJournal, archiveRecord, []byte) {
	t.Helper()
	j, pending, _, _ := archiveJournalFixture(t)
	j.Namespace = strings.Repeat("a", 32)
	pending.Namespace = j.Namespace
	pending.State = "pending"
	return archiveJournal{SchemaVersion: 1, BoardPath: j.BoardPath, Namespace: j.Namespace, Records: []archiveRecord{}}, pending, nil
}

func archiveDeltaStates(t *testing.T) (archivePendingDelta, []archiveJournal) {
	t.Helper()
	original, pending, _ := archiveDeltaFixture(t)
	d, err := prepareArchivePendingDelta(original, pending)
	if err != nil {
		t.Fatal(err)
	}
	target := original
	target.Records = []archiveRecord{pending}
	completed := completedArchiveJournal(target)
	return d, []archiveJournal{original, target, completed}
}

func TestArchiveDeltaRoundTripStatesAndEmptyHistory(t *testing.T) {
	t.Parallel()
	d, states := archiveDeltaStates(t)
	raw, err := archivePendingDeltaBytes(d)
	if err != nil {
		t.Fatal("valid delta:", err)
	}
	decoded, err := decodeArchivePendingDelta(raw)
	if err != nil || !bytes.Equal(decoded.PendingRecord, d.PendingRecord) {
		t.Fatalf("round trip decoded=%+v err=%v", decoded, err)
	}
	for i, want := range []string{"original", "pending", "completed"} {
		journalRaw, err := archiveJournalBytes(states[i])
		if err != nil {
			t.Fatal(err)
		}
		got, err := resolveArchivePendingDelta(decoded, journalRaw, d.BoardPath, d.Namespace, []string{d.ID})
		if err != nil || got.State != want || len(got.Target) == 0 || len(got.Completed) == 0 {
			t.Fatalf("state %s got=%+v err=%v", want, got, err)
		}
	}
	if _, err := decodeArchivePendingDelta(append([]byte(" \n"), raw...)); err != nil {
		t.Fatalf("canonical whitespace rejected: %v", err)
	}
	copyDelta := d
	copyDelta.PendingRecord = append([]byte(nil), d.PendingRecord...)
	copyDelta.PendingRecord[0] ^= 1
	if _, err := archivePendingDeltaBytes(copyDelta); err == nil {
		t.Fatal("mutated pending record accepted")
	}
}

func TestArchiveDeltaRejectsPreparationStatesAndHashForgery(t *testing.T) {
	t.Parallel()
	original, pending, _ := archiveDeltaFixture(t)
	if _, err := prepareArchivePendingDelta(archiveJournal{SchemaVersion: 1, BoardPath: original.BoardPath}, pending); err == nil {
		t.Fatal("missing namespace accepted")
	}
	bad := pending
	bad.State = "completed"
	if _, err := prepareArchivePendingDelta(original, bad); err == nil {
		t.Fatal("completed pending record accepted")
	}
	d, _ := archiveDeltaStates(t)
	mutations := map[string]func(*archivePendingDelta){
		"request": func(x *archivePendingDelta) { x.RequestID = strings.Repeat("b", 32) },
		"id":      func(x *archivePendingDelta) { x.ID = "TASK-99" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			candidate := d
			mutate(&candidate)
			if _, err := archivePendingDeltaBytes(candidate); err == nil {
				t.Fatal("forged delta accepted")
			}
		})
	}
	resolved, states := archiveDeltaStates(t)
	originalRaw, _ := archiveJournalBytes(states[0])
	for name, mutate := range map[string]func(*archivePendingDelta){
		"original hash":   func(x *archivePendingDelta) { x.OriginalJournalSHA256 = strings.Repeat("c", 64) },
		"target hash":     func(x *archivePendingDelta) { x.TargetJournalSHA256 = strings.Repeat("c", 64) },
		"completed hash":  func(x *archivePendingDelta) { x.CompletedJournalSHA256 = strings.Repeat("c", 64) },
		"request scope":   func(x *archivePendingDelta) { x.RequestID = strings.Repeat("c", 32) },
		"board scope":     func(x *archivePendingDelta) { x.BoardPath = "/other" },
		"namespace scope": func(x *archivePendingDelta) { x.Namespace = strings.Repeat("c", 32) },
	} {
		t.Run("resolve "+name, func(t *testing.T) {
			candidate := resolved
			mutate(&candidate)
			if _, err := resolveArchivePendingDelta(candidate, originalRaw, candidate.BoardPath, candidate.Namespace, []string{candidate.ID}); err == nil {
				t.Fatal("forged hash resolved")
			}
		})
	}
}

func TestArchiveDeltaStrictWireAndResolutionScope(t *testing.T) {
	t.Parallel()
	d, states := archiveDeltaStates(t)
	raw, err := archivePendingDeltaBytes(d)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(map[string]json.RawMessage){
		"unknown":     func(m map[string]json.RawMessage) { m["extra"] = json.RawMessage("1") },
		"null record": func(m map[string]json.RawMessage) { m["pendingRecord"] = json.RawMessage("null") },
	} {
		t.Run(name, func(t *testing.T) {
			c := make(map[string]json.RawMessage, len(fields))
			for k, v := range fields {
				c[k] = append(json.RawMessage(nil), v...)
			}
			change(c)
			candidate, err := json.Marshal(c)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeArchivePendingDelta(candidate); err == nil {
				t.Fatal("malformed wire accepted")
			}
		})
	}
	numericRecord := make([]int, len(d.PendingRecord))
	for i, b := range d.PendingRecord {
		numericRecord[i] = int(b)
	}
	arrayRecord, err := json.Marshal(numericRecord)
	if err != nil {
		t.Fatal(err)
	}
	fields["pendingRecord"] = arrayRecord
	arrayWire, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArchivePendingDelta(arrayWire); err == nil || !strings.Contains(err.Error(), "must be a base64 string") {
		t.Fatalf("valid record bytes as numeric array err=%v", err)
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	duplicate := append([]byte(nil), raw...)
	idx := bytes.Index(duplicate, []byte("\"owner\""))
	duplicate = append(duplicate[:idx], append([]byte("\"owner\":\"other\","), duplicate[idx:]...)...)
	if _, err := decodeArchivePendingDelta(duplicate); err == nil {
		t.Fatal("duplicate owner accepted")
	}
	journalRaw, err := archiveJournalBytes(states[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePendingDelta(d, journalRaw, d.BoardPath, d.Namespace, nil); err == nil || !strings.Contains(err.Error(), "not reserved") {
		t.Fatalf("unreserved ID err=%v", err)
	}
	if _, err := resolveArchivePendingDelta(d, journalRaw, "/other", d.Namespace, []string{d.ID}); err == nil || !strings.Contains(err.Error(), "board") {
		t.Fatalf("board scope err=%v", err)
	}
	if _, err := resolveArchivePendingDelta(d, journalRaw, d.BoardPath, strings.Repeat("b", 32), []string{d.ID}); err == nil || !strings.Contains(err.Error(), "board or namespace") {
		t.Fatalf("namespace scope err=%v", err)
	}
	if _, err := decodeArchivePendingDelta(append(append([]byte(nil), raw...), '{')); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := decodeArchivePendingDelta(append(append([]byte(nil), raw...), 0xff)); err == nil || !strings.Contains(err.Error(), "size invalid") {
		t.Fatalf("invalid UTF-8 err=%v", err)
	}
	if _, err := decodeArchivePendingDelta(bytes.Replace(raw, []byte(`"owner":"worker"`), []byte(`"owner":"\ud800"`), 1)); err == nil || !strings.Contains(err.Error(), "unpaired JSON high surrogate") {
		t.Fatalf("unpaired surrogate err=%v", err)
	}
	missing := make(map[string]json.RawMessage, len(fields))
	for k, v := range fields {
		missing[k] = v
	}
	delete(missing, "targetJournalSha256")
	missingRaw, err := json.Marshal(missing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArchivePendingDelta(missingRaw); err == nil || !strings.Contains(err.Error(), "targetJournalSha256") {
		t.Fatalf("missing required field err=%v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, d.PendingRecord, "", "  "); err != nil {
		t.Fatal(err)
	}
	noncanonical := d
	noncanonical.PendingRecord = pretty.Bytes()
	if _, err := archivePendingDeltaBytes(noncanonical); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("noncanonical pending record err=%v", err)
	}
	ownerMismatch := d
	ownerMismatch.Owner = "other-owner"
	if _, err := archivePendingDeltaBytes(ownerMismatch); err == nil || !strings.Contains(err.Error(), "differs from envelope") {
		t.Fatalf("owner mismatch err=%v", err)
	}
	if _, err := resolveArchivePendingDelta(d, []byte("{}"), d.BoardPath, d.Namespace, []string{d.ID}); err == nil || !strings.Contains(err.Error(), "missing schemaVersion") {
		t.Fatalf("missing current journal err=%v", err)
	}
	third := states[1]
	third.Records[0].Owner = "different-owner"
	thirdRaw, err := archiveJournalBytes(third)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolveArchivePendingDelta(d, thirdRaw, d.BoardPath, d.Namespace, []string{d.ID}); err == nil || !strings.Contains(err.Error(), "third state") {
		t.Fatalf("third current state err=%v", err)
	}
}
