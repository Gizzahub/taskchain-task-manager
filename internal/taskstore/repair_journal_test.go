package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func repairJournalFixture(t *testing.T) repairJournal {
	t.Helper()
	original := []byte("---\nid: TASK-1\ntitle: x\n---\n\n| **Status** | [x] Done |\n")
	doc, err := parseRepairFixture(original)
	if err != nil {
		t.Fatal(err)
	}
	patched, changed, err := doc.SetStatusCell("pending")
	if err != nil || !changed {
		t.Fatalf("patch: %v changed=%v", err, changed)
	}
	return repairJournal{SchemaVersion: 1, BoardPath: "/repo/board", Records: []repairRecord{{
		Kind: "pending", RequestID: strings.Repeat("a", 32), ID: "TASK-1", Owner: "worker", Token: "",
		Path: "todo/TASK-1.md", ExpectedSHA256: bytesDigest(original), Mode: 0644, Original: original, Patched: patched,
		PolicyDigest: strings.Repeat("b", 64), BoardPath: "/repo/board", CanonicalStatus: "pending", Changed: changed,
	}}}
}

func parseRepairFixture(raw []byte) (*card.Document, error) { return card.Parse(raw) }

func TestDecodeRepairJournalRoundTripAndMissing(t *testing.T) {
	t.Parallel()
	j := repairJournalFixture(t)
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeRepairJournal(append(raw, '\n'))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.BoardPath != j.BoardPath {
		t.Fatalf("decoded=%+v", got)
	}
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := loadRepairJournal(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing journal err=%v", err)
	}
}

func TestDecodeRepairJournalRejectsWireAndPayloadTampering(t *testing.T) {
	t.Parallel()
	base := repairJournalFixture(t)
	valid, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unknown", func(m map[string]any) { m["extra"] = true }},
		{"null-records", func(m map[string]any) { m["records"] = nil }},
		{"bad-board", func(m map[string]any) { m["boardPath"] = "relative" }},
		{"bad-duplicate-pending", func(m map[string]any) {
			first := m["records"].([]any)[0].(map[string]any)
			second := map[string]any{}
			for key, value := range first {
				second[key] = value
			}
			second["requestId"] = strings.Repeat("b", 32)
			m["records"] = []any{first, second}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]any
			if err := json.Unmarshal(valid, &m); err != nil {
				t.Fatal(err)
			}
			tc.mutate(m)
			raw, _ := json.Marshal(m)
			if _, err := decodeRepairJournal(raw); err == nil {
				t.Fatal("tampered journal accepted")
			}
		})
	}
	mut := base
	mut.Records = append([]repairRecord(nil), base.Records...)
	mut.Records[0].Original = []byte("tampered")
	raw, _ := json.Marshal(mut)
	if _, err := decodeRepairJournal(raw); err == nil {
		t.Fatal("tampered payload accepted")
	}
}

func TestRepairJournalCompletionCapacityAndCompletedShape(t *testing.T) {
	t.Parallel()
	j := repairJournalFixture(t)
	j.Records[0].Kind = "completed"
	j.Records[0].Original = nil
	j.Records[0].Patched = nil
	raw, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRepairJournal(raw); err != nil {
		t.Fatal(err)
	}
	j.Records[0].Mode = 0
	if err := validateRepairRecord(j.Records[0]); err == nil {
		t.Fatal("zero mode accepted")
	}
}

func completedRepairRecord(board, id string, n int) repairRecord {
	return repairRecord{
		Kind: "completed", RequestID: fmt.Sprintf("%032x", n), ID: id, Owner: "worker",
		Path: "todo/" + id + ".md", ExpectedSHA256: strings.Repeat("a", 64), Mode: 0644,
		PolicyDigest: strings.Repeat("b", 64), BoardPath: board, CanonicalStatus: "pending",
	}
}

func TestRepairJournalSizeLimitPreservesExistingJournal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	board, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	base := repairJournalFixture(t)
	base.BoardPath = board
	base.Records[0].Kind = "completed"
	base.Records[0].Original = nil
	base.Records[0].Patched = nil
	base.Records[0].BoardPath = board
	if err := saveRepairJournal(root, base, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	// Whitespace is valid JSON: exercise the exact inclusive wire-size limit.
	atLimit := append(append([]byte(nil), before...), bytes.Repeat([]byte(" "), maxRepairsBytes-len(before))...)
	if _, err := decodeRepairJournal(atLimit); err != nil {
		t.Fatalf("exactly 8 MiB rejected: %v", err)
	}
	if _, err := decodeRepairJournal(append(atLimit, ' ')); err == nil {
		t.Fatal("8 MiB plus one byte accepted")
	}

	tooLarge := repairJournal{SchemaVersion: 1, BoardPath: board}
	for i := 1; i <= 30000; i++ {
		tooLarge.Records = append(tooLarge.Records, completedRepairRecord(board, fmt.Sprintf("TASK-%d", i+1), i))
	}
	if raw, err := json.Marshal(tooLarge); err != nil || len(raw)+1 <= maxRepairsBytes {
		t.Fatalf("fixture did not exceed limit: bytes=%d err=%v", len(raw)+1, err)
	}
	if err := saveRepairJournal(root, tooLarge, false); err == nil {
		t.Fatal("oversized repair journal accepted")
	}
	after, err := os.ReadFile(filepath.Join(dir, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("oversized journal changed existing journal")
	}
}
