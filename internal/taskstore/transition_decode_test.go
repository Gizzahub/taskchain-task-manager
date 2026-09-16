package taskstore

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestTransitionDecodeSeparatesWireAndPolicyValidation(t *testing.T) {
	j := journalFixture(t)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeTransitionJournal(raw)
	if err != nil || !reflect.DeepEqual(j, decoded) {
		t.Fatalf("decode = %+v, %v", decoded, err)
	}
	if err := validateTransitionRecords(decoded, boardpolicy.Default()); err != nil {
		t.Fatal(err)
	}
	// A wire-valid record is not permission to replay it. Semantic validation
	// must still reject payload tampering after recovery selects the policy.
	j.Records[0].Patched = []byte("forged")
	raw, err = json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeTransitionJournal(raw)
	if err != nil {
		t.Fatalf("wire decoding unexpectedly consulted semantics: %v", err)
	}
	if err := validateTransitionRecords(decoded, boardpolicy.Default()); err == nil {
		t.Fatal("forged transition passed semantic validation")
	}
}

func TestTransitionPublishRejectsInvalidRecordCollection(t *testing.T) {
	for _, duplicateID := range []bool{false, true} {
		t.Run(map[bool]string{false: "multiple_pending", true: "duplicate_id"}[duplicateID], func(t *testing.T) {
			dir := t.TempDir()
			r, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			j := journalFixture(t)
			if err := publishTransitionJournal(r, j); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}
			before := boardBytes(t, dir)
			record := j.Records[0]
			if !duplicateID {
				record.RequestID = strings.Repeat("c", 32)
			}
			j.Records = append(j.Records, record)
			if err := publishTransitionJournal(r, j); err == nil {
				t.Fatal("invalid collection published")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejected publication changed files")
			}
		})
	}
}

func TestTransitionTargetEncodingIsExactAndPolicyBound(t *testing.T) {
	p := boardpolicy.Default()
	digest, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}
	j := transitionJournal{SchemaVersion: 2, PolicyDigest: digest, Records: []transitionRecord{}}
	raw, err := encodeTransitionJournal(j, p)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(append(want, '\n')) {
		t.Fatal("target differs from existing journal serialization")
	}
	j.PolicyDigest = strings.Repeat("f", 64)
	if _, err := encodeTransitionJournal(j, p); err == nil {
		t.Fatal("target accepted a different policy digest")
	}
	j.SchemaVersion = 99
	if _, err := encodeTransitionJournal(j, p); err == nil {
		t.Fatal("target accepted unsupported version")
	}
}

func TestTransitionLegacyEncodingDoesNotAdoptCustomSemantics(t *testing.T) {
	p, err := boardpolicy.New(boardpolicy.Declaration{Transitions: []boardpolicy.Transition{{From: "todo", To: []string{"done"}}}})
	if err != nil {
		t.Fatal(err)
	}
	j := journalFixture(t)
	rec := &j.Records[0]
	rec.Kind, rec.Status = "completed", "completed"
	rec.Original, rec.Patched = nil, nil
	rec.To, rec.Target = "done", "done/TASK-1.md"
	if err := validateTransitionRecordWithPolicy(*rec, p); err != nil {
		t.Fatalf("custom-policy baseline invalid: %v", err)
	}
	if _, err := encodeTransitionJournal(j, p); err == nil {
		t.Fatal("legacy journal adopted new policy semantics")
	}
	rec.To, rec.Target = "doing", "doing/TASK-1.md"
	raw, err := encodeTransitionJournal(j, p)
	if err != nil {
		t.Fatalf("legacy receipt rejected under new target policy: %v", err)
	}
	dir := t.TempDir()
	writeJournalFixture(t, dir, raw)
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := loadTransitions(r); err != nil {
		t.Fatalf("encoded legacy receipt cannot reload: %v", err)
	}
}
