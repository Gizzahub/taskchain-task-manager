package taskstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func archiveJournalFixture(t *testing.T) (archiveJournal, archiveRecord, archiveCompletionBinding, []byte) {
	t.Helper()
	b, raw, _, _ := archiveCompletionFixture(t)
	r := archiveRecord{
		State: "pending", Operation: "archive", RequestID: b.RequestID, ID: b.ID, Owner: "worker", BoardPath: b.BoardPath,
		Source: b.Source, Target: b.Target, OriginalSHA256: bytesDigest(raw), FinalSHA256: bytesDigest(raw), Mode: 0644,
		PolicyCanonical: append([]byte(nil), b.PolicyCanonical...), PolicyDigest: b.PolicyDigest,
		RulesCanonical: append([]byte(nil), b.RulesCanonical...), RulesDigest: b.RulesDigest,
		Original: append([]byte(nil), raw...), Patched: append([]byte(nil), raw...), Completion: &b,
	}
	return archiveJournal{SchemaVersion: 1, BoardPath: b.BoardPath, Records: []archiveRecord{r}}, r, b, raw
}

func TestArchiveJournalValidPendingAndCompletedBinding(t *testing.T) {
	t.Parallel()
	j, pending, b, cardRaw := archiveJournalFixture(t)
	raw, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal("valid pending journal:", err)
	}
	decoded, err := decodeArchiveJournal(raw)
	if err != nil || len(decoded.Records) != 1 || decoded.Records[0].State != "pending" {
		t.Fatalf("decoded pending=%+v err=%v", decoded, err)
	}
	if _, err := archiveCompletedBindings(decoded, j.BoardPath, j.Namespace); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("pending journal exposed completion: %v", err)
	}

	completed := pending
	completed.State, completed.Original, completed.Patched = "completed", nil, nil
	finished := archiveJournal{SchemaVersion: 1, BoardPath: j.BoardPath, Records: []archiveRecord{completed}}
	finishedRaw, err := archiveJournalBytes(finished)
	if err != nil {
		t.Fatal("valid completed journal:", err)
	}
	decoded, err = decodeArchiveJournal(finishedRaw)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := archiveCompletedBindings(decoded, j.BoardPath, j.Namespace)
	if err != nil || len(bindings) != 1 || !bytes.Equal(bindings[b.Identity].PolicyCanonical, b.PolicyCanonical) {
		t.Fatalf("completed bindings=%+v err=%v", bindings, err)
	}
	raw2 := bytes.ReplaceAll(cardRaw, []byte("TASK-001"), []byte("TASK-002"))
	b2 := b
	b2.ID, b2.Identity, b2.RequestID, b2.Source, b2.Target, b2.FinalSHA256 = "TASK-002", "TASK-2", strings.Repeat("b", 32), "done/TASK-002.md", "_archive/done/TASK-002.md", bytesDigest(raw2)
	r2 := pending
	r2.ID, r2.RequestID, r2.Source, r2.Target, r2.OriginalSHA256, r2.FinalSHA256 = b2.ID, b2.RequestID, b2.Source, b2.Target, b2.FinalSHA256, b2.FinalSHA256
	r2.Original, r2.Patched, r2.Completion = raw2, raw2, &b2
	completedFirst := completed
	partial := archiveJournal{SchemaVersion: 1, BoardPath: j.BoardPath, Records: []archiveRecord{completedFirst, r2}}
	partialRaw, err := archiveJournalBytes(partial)
	if err != nil {
		t.Fatal("valid completed-plus-pending journal:", err)
	}
	partialDecoded, err := decodeArchiveJournal(partialRaw)
	if err != nil {
		t.Fatal(err)
	}
	if bindings, err := archiveCompletedBindings(partialDecoded, j.BoardPath, j.Namespace); err == nil || bindings != nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("partial journal exposed bindings=%v err=%v", bindings, err)
	}
}

func TestArchiveJournalScopeAndBindingMismatches(t *testing.T) {
	t.Parallel()
	j, pending, b, _ := archiveJournalFixture(t)
	if _, err := archiveCompletedBindings(j, "/other/tasks", ""); err == nil || !strings.Contains(err.Error(), "current board") {
		t.Fatalf("board mismatch accepted: %v", err)
	}
	if _, err := archiveCompletedBindings(j, j.BoardPath, "012345678901234567890123456789ab"); err == nil || !strings.Contains(err.Error(), "current board") {
		t.Fatalf("namespace mismatch accepted: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*archiveCompletionBinding)
	}{
		{"request", func(x *archiveCompletionBinding) { x.RequestID = strings.Repeat("b", 32) }},
		{"id", func(x *archiveCompletionBinding) { x.ID, x.Identity = "TASK-002", "TASK-2" }},
		{"source", func(x *archiveCompletionBinding) {
			x.Source, x.Target = "done/TASK-002.md", "_archive/done/TASK-002.md"
		}},
		{"target", func(x *archiveCompletionBinding) { x.Target = "_archive/done/TASK-002.md" }},
		{"hash", func(x *archiveCompletionBinding) { x.FinalSHA256 = strings.Repeat("0", 64) }},
		{"policy digest", func(x *archiveCompletionBinding) { x.PolicyDigest = strings.Repeat("0", 64) }},
		{"rules digest", func(x *archiveCompletionBinding) { x.RulesDigest = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copyRecord := pending
			copyBinding := b
			tc.mutate(&copyBinding)
			copyRecord.Completion = &copyBinding
			if err := validateArchiveRecordCompletion(copyRecord); err == nil {
				t.Fatal("record/binding mismatch accepted")
			}
		})
	}
}

func TestArchiveJournalRejectsOrderingDuplicatesAndCompletedPayload(t *testing.T) {
	t.Parallel()
	j, pending, _, _ := archiveJournalFixture(t)
	notLast := j
	notLast.Records = append([]archiveRecord{pending}, pending)
	if err := validateArchiveJournal(notLast); err == nil || !strings.Contains(err.Error(), "final record") {
		t.Fatalf("pending non-final accepted: %v", err)
	}
	completedPayload := pending
	completedPayload.State = "completed"
	if err := validateArchiveRecord(completedPayload); err == nil || !strings.Contains(err.Error(), "retains source payload") {
		t.Fatalf("completed payload accepted: %v", err)
	}
	duplicate := j
	duplicate.Records = append(append([]archiveRecord(nil), j.Records...), pending)
	duplicate.Records[0].State = "completed"
	duplicate.Records[0].Original, duplicate.Records[0].Patched = nil, nil
	if err := validateArchiveJournal(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate request or identity accepted: %v", err)
	}
}

func TestArchiveJournalForceSupersedeAndStatusPatchContracts(t *testing.T) {
	t.Parallel()
	_, pending, _, raw := archiveJournalFixture(t)
	force := pending
	force.Operation, force.Completion, force.Assertion = "force", nil, ""
	if err := validateArchiveRecord(force); err == nil || !strings.Contains(err.Error(), "explicit assertion") {
		t.Fatalf("force without assertion accepted: %v", err)
	}
	force.Assertion = "operator override"
	if err := validateArchiveRecord(force); err != nil {
		t.Fatal("valid forced record rejected:", err)
	}
	for _, operation := range []string{"force", "supersede"} {
		t.Run(operation+" binding", func(t *testing.T) {
			copyRecord := pending
			copyRecord.Operation = operation
			if operation == "force" {
				copyRecord.Assertion = "operator override"
			}
			if err := validateArchiveRecord(copyRecord); err == nil || !strings.Contains(err.Error(), "cannot publish") {
				t.Fatalf("completion binding accepted for %s: %v", operation, err)
			}
		})
	}
	doc, err := card.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	patched, changed, err := doc.SetFrontmatterStatus("superseded")
	if err != nil || !changed {
		t.Fatalf("supersede patch=%v changed=%v", err, changed)
	}
	supersede := pending
	supersede.Operation, supersede.Completion = "supersede", nil
	supersede.FinalSHA256 = bytesDigest(patched)
	supersede.Patched = patched
	if err := validateArchiveRecord(supersede); err != nil {
		t.Fatal("valid supersede record rejected:", err)
	}
}

func TestArchiveJournalStrictWireFailuresAreSingleCause(t *testing.T) {
	t.Parallel()
	j, _, _, _ := archiveJournalFixture(t)
	valid, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(valid, &root); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"unknown root":   append(append([]byte(nil), bytes.TrimSpace(valid)[:len(bytes.TrimSpace(valid))-1]...), []byte(`,"extra":1}`)...),
		"duplicate root": append(append([]byte(nil), bytes.TrimSpace(valid)[:len(bytes.TrimSpace(valid))-1]...), []byte(`,"boardPath":"/other/tasks"}`)...),
	}
	records := root["records"]
	var list []json.RawMessage
	if err := json.Unmarshal(records, &list); err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(list[0], &record); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"null policy": "null", "array rules": "[]"} {
		copyRecord := map[string]json.RawMessage{}
		for key, item := range record {
			copyRecord[key] = item
		}
		if strings.HasPrefix(name, "null") {
			copyRecord["policyCanonical"] = json.RawMessage(value)
		} else {
			numbers := make([]int, len(j.Records[0].RulesCanonical))
			for i, item := range j.Records[0].RulesCanonical {
				numbers[i] = int(item)
			}
			array, _ := json.Marshal(numbers)
			copyRecord["rulesCanonical"] = array
		}
		item, _ := json.Marshal(copyRecord)
		copyList, _ := json.Marshal([]json.RawMessage{item})
		copyRoot := map[string]json.RawMessage{}
		for key, raw := range root {
			copyRoot[key] = raw
		}
		copyRoot["records"] = copyList
		cases[name], _ = json.Marshal(copyRoot)
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeArchiveJournal(raw); err == nil {
				t.Fatal("invalid archive journal accepted")
			}
		})
	}
}
