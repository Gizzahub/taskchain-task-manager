package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func emptyBundleJournal() bundleJournal {
	return bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{}}
}

func TestBundleJournalRoundTripAndAtomicInitialUpdate(t *testing.T) {
	t.Parallel()
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := emptyBundleJournal()
	if err := saveBundles(r, want, true); err != nil {
		t.Fatal(err)
	}
	got, err := loadBundles(r)
	if err != nil || got.SchemaVersion != 1 || got.BoardID != want.BoardID || got.Records == nil {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	before, err := r.ReadFile(bundlesFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBundles(r, want, true); err == nil {
		t.Fatal("initial save overwrote existing journal")
	}
	after, _ := r.ReadFile(bundlesFile)
	if !bytes.Equal(before, after) {
		t.Fatal("failed initial save changed journal")
	}
	want.BoardID = strings.Repeat("b", 32)
	if err := saveBundles(r, want, false); err != nil {
		t.Fatal(err)
	}
	updated, err := loadBundles(r)
	if err != nil || updated.BoardID != want.BoardID {
		t.Fatalf("updated=%+v err=%v", updated, err)
	}
}

func TestBundleJournalMissingAndRejectsMalformedShape(t *testing.T) {
	t.Parallel()
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := loadBundles(r); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing err=%v", err)
	}
	valid := `{"schemaVersion":1,"boardId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","records":[]}`
	cases := map[string]string{
		"unknown":      strings.Replace(valid, `"records":[]`, `"records":[],"extra":1`, 1),
		"duplicate":    strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		"null records": strings.Replace(valid, `"records":[]`, `"records":null`, 1),
		"null field":   strings.Replace(valid, `"boardId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"boardId":null`, 1),
		"wrong schema": strings.Replace(valid, `"schemaVersion":1`, `"schemaVersion":"1"`, 1),
		"trailing":     valid + " {}",
		"bad board":    strings.Replace(valid, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if err := r.WriteFile(bundlesFile, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadBundles(r); err == nil {
				t.Fatal("malformed journal accepted")
			}
		})
	}
}

func TestBundleJournalRejectsRecordAndCardShape(t *testing.T) {
	t.Parallel()
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	journal := emptyBundleJournal()
	journal.Records = append(journal.Records, bundleRecordFixture(t))
	base := string(mustBundleJournalBytes(t, journal))
	if err := r.WriteFile(bundlesFile, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBundles(r); err != nil {
		t.Fatalf("invalid baseline: %v", err)
	}
	cases := []string{
		strings.Replace(base, `"path":"todo/TASK-1.md"`, `"path":"../TASK-1.md"`, 1),
		strings.Replace(base, `"status":"pending"`, `"status":"unknown"`, 1),
		strings.Replace(base, `"requestId":"`+journal.Records[0].RequestID+`"`, `"requestId":"bad"`, 1),
		strings.Replace(base, `"requestDigest":"`+journal.Records[0].RequestDigest+`"`, `"requestDigest":null`, 1),
		strings.Replace(base, `"namespace":""`, `"namespace":1`, 1),
		strings.Replace(base, `"owner":""`, `"owner":null`, 1),
	}
	for _, raw := range cases {
		if raw == base {
			t.Fatal("mutation did not apply")
		}
		if err := r.WriteFile(bundlesFile, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadBundles(r); err == nil {
			t.Fatal("invalid record accepted")
		}
	}
}

func TestBundleJournalSymlinkOversizeAndPublicationConflict(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := os.Symlink(filepath.Join(t.TempDir(), "target"), filepath.Join(root, bundlesFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBundles(r); err == nil {
		t.Fatal("symlink journal accepted")
	}
	if err := os.Remove(filepath.Join(root, bundlesFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, bundlesFile), bytes.Repeat([]byte("x"), maxBundleJournalBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBundles(r); err == nil {
		t.Fatal("oversized journal accepted")
	}
	if err := os.Remove(filepath.Join(root, bundlesFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, bundlesFile), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, bundlesFile, "sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := saveBundles(r, emptyBundleJournal(), false); err == nil {
		t.Fatal("rename over nonempty destination succeeded")
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "preserve" {
		t.Fatalf("destination changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != bundlesFile {
		t.Fatalf("staging leak: %v %v", entries, err)
	}
}

func TestBundleJournalBytesRejectsInvalidEmptyJournalAndIsJSON(t *testing.T) {
	t.Parallel()
	if _, err := bundleJournalBytes(bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32)}); err == nil {
		t.Fatal("nil records accepted")
	}
	raw, err := bundleJournalBytes(emptyBundleJournal())
	if err != nil || !json.Valid(bytes.TrimSpace(raw)) || !bytes.HasSuffix(raw, []byte{'\n'}) {
		t.Fatalf("bytes=%s err=%v", raw, err)
	}
}

func TestBundleJournalRoundTripsValidPendingAndCompletedRecords(t *testing.T) {
	t.Parallel()
	record := bundleRecordFixture(t)
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record}}
	raw, err := bundleJournalBytes(journal)
	if err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.WriteFile(bundlesFile, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadBundles(r)
	if err != nil || len(got.Records) != 1 || got.Records[0].RequestID != record.RequestID {
		t.Fatalf("pending=%+v err=%v", got, err)
	}
	record.Status = "completed"
	completed := bundleJournal{SchemaVersion: 1, BoardID: journal.BoardID, Records: []bundleRecord{record}}
	if err := r.WriteFile(bundlesFile, mustBundleJournalBytes(t, completed), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = loadBundles(r)
	if err != nil || got.Records[0].Status != "completed" {
		t.Fatalf("completed=%+v err=%v", got, err)
	}
}

func TestBundleJournalRejectsDuplicateRequestAndMultiplePendingValidRecords(t *testing.T) {
	t.Parallel()
	record := bundleRecordFixture(t)
	duplicate := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record, record}}
	if _, err := bundleJournalBytes(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate request accepted: %v", err)
	}
	second := bundleRecordFixture(t)
	parsed, err := intentdoc.ParseBundle(record.Request)
	if err != nil {
		t.Fatal(err)
	}
	request, err := parsed.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	request.RequestID = strings.Repeat("b", 32)
	requestRaw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = intentdoc.ParseBundle(requestRaw)
	if err != nil {
		t.Fatal(err)
	}
	second.RequestID = request.RequestID
	second.Request, _ = parsed.Canonical()
	second.RequestDigest, _ = parsed.Digest()
	multiple := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record, second}}
	if _, err := bundleJournalBytes(multiple); err == nil || !strings.Contains(err.Error(), "multiple pending") {
		t.Fatalf("multiple pending accepted: %v", err)
	}
}

func TestBundleJournalByteFieldsRequireJSONStrings(t *testing.T) {
	t.Parallel()
	record := bundleRecordFixture(t)
	raw, err := json.Marshal(bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record}})
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	fields := []string{"request", "intent", "originalIds", "baseIds", "targetIds", "batch"}
	for _, field := range fields {
		t.Run(field, func(t *testing.T) {
			copyRoot := cloneJSONMap(root)
			copyRecords := copyRoot["records"].([]any)
			copyRecord := copyRecords[0].(map[string]any)
			copyRecord[field] = []any{1}
			mutated, err := json.Marshal(copyRoot)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateBundleJournalShape(mutated); err == nil {
				t.Fatal("numeric array accepted for byte field")
			}
		})
	}
	copyRoot := cloneJSONMap(root)
	copyRecords := copyRoot["records"].([]any)
	copyRecord := copyRecords[0].(map[string]any)
	card := copyRecord["cards"].([]any)[0].(map[string]any)
	card["raw"] = []any{1}
	mutated, err := json.Marshal(copyRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateBundleJournalShape(mutated); err == nil {
		t.Fatal("numeric array accepted for card raw")
	}
}

func mustBundleJournalBytes(t *testing.T, journal bundleJournal) []byte {
	t.Helper()
	raw, err := bundleJournalBytes(journal)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func cloneJSONMap(input map[string]any) map[string]any {
	raw, _ := json.Marshal(input)
	var output map[string]any
	_ = json.Unmarshal(raw, &output)
	return output
}
