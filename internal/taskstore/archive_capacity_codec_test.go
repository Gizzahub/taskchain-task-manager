package taskstore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestArchiveCapacityCodecAcceptsCanonicalExpansionOfValidV1(t *testing.T) {
	j, base, _, _ := archiveJournalFixture(t)
	j.Records = make([]archiveRecord, 1000)
	for i := range j.Records {
		r := base
		r.State, r.Operation, r.Assertion = "completed", "force", strings.Repeat("<", 4096)
		r.Original, r.Patched, r.Completion = nil, nil, nil
		r.ID, r.RequestID = fmt.Sprintf("TASK-%d", i+1), fmt.Sprintf("%032x", i+1)
		r.Source, r.Target = "done/"+r.ID+".md", "_archive/done/"+r.ID+".md"
		j.Records[i] = r
	}
	var source bytes.Buffer
	encoder := json.NewEncoder(&source)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(j); err != nil {
		t.Fatal(err)
	}
	if source.Len() >= maxRepairsBytes {
		t.Fatal("fixture exceeds original schema1 limit")
	}
	decoded, err := decodeArchiveJournal(source.Bytes())
	if err != nil {
		t.Fatal("valid schema1 source:", err)
	}
	if _, err := archiveJournalBytes(decoded); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("fixture must expose old canonical encoding limit: %v", err)
	}
	decoded.SchemaVersion = 2
	expanded, err := archiveCapacityJournalBytes(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(expanded) <= maxRepairsBytes || len(expanded) > 6*source.Len()+1 {
		t.Fatalf("expansion outside expected bounds: source=%d target=%d", source.Len(), len(expanded))
	}
	restored, err := decodeArchiveCapacityJournal(expanded)
	if err != nil || !reflect.DeepEqual(restored, decoded) {
		t.Fatalf("expanded receipt history rejected: %v", err)
	}
}

func TestArchiveCapacityCodecVersionSpecificBounds(t *testing.T) {
	for _, schema := range []int{1, 2} {
		j := archiveJournal{SchemaVersion: schema, BoardPath: "/tasks", Records: []archiveRecord{}}
		raw, err := archiveCapacityJournalBytes(j)
		if err != nil {
			t.Fatal(err)
		}
		limit := archiveJournalVersionLimit(schema)
		padded := append(bytes.Repeat([]byte(" "), limit-len(raw)), raw...)
		got, err := decodeArchiveCapacityJournal(padded)
		if err != nil || got.SchemaVersion != schema {
			t.Fatalf("schema %d exact limit rejected: %v", schema, err)
		}
		if _, err := decodeArchiveCapacityJournal(append(padded, ' ')); err == nil || !strings.Contains(err.Error(), "size") {
			t.Fatalf("schema %d oversized journal err=%v", schema, err)
		}
	}
}

func TestArchiveCapacityCodecPreservesStrictRecords(t *testing.T) {
	j, _, _, _ := archiveJournalFixture(t)
	j.SchemaVersion = 2
	raw, err := archiveCapacityJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeArchiveCapacityJournal(raw)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := archiveCapacityJournalBytes(got)
	if err != nil || !bytes.Equal(raw, encoded) {
		t.Fatalf("schema2 roundtrip changed receipt: %v", err)
	}
	for _, candidate := range [][]byte{
		bytes.Replace(raw, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":3`), 1),
		bytes.Replace(raw, []byte(`"schemaVersion":2`), []byte(`"schemaVersion":2,"schemaVersion":2`), 1),
		bytes.Replace(raw, []byte(`"state":"pending"`), []byte(`"state":"unknown"`), 1),
		append(append([]byte(nil), raw...), '{'),
	} {
		if bytes.Equal(candidate, raw) {
			t.Fatal("fixture mutation did not change input")
		}
		if _, err := decodeArchiveCapacityJournal(candidate); err == nil {
			t.Fatal("malformed capacity journal accepted")
		}
	}
}

func TestArchiveCapacityCodecDoesNotEnableRuntimeSchema2(t *testing.T) {
	j := archiveJournal{SchemaVersion: 2, BoardPath: "/tasks", Records: []archiveRecord{}}
	raw, err := archiveCapacityJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeArchiveJournal(raw); err == nil {
		t.Fatal("ordinary reader accepted unadopted schema2")
	}
	if _, err := archiveJournalBytes(j); err == nil {
		t.Fatal("ordinary writer encoded unadopted schema2")
	}
	if err := validateArchiveJournal(j); err == nil {
		t.Fatal("ordinary binding validator accepted schema2")
	}
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := saveArchiveJournal(r, j, true); err == nil {
		t.Fatal("ordinary writer published unadopted schema2")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("refused writer changed filesystem: %v", err)
	}
}
