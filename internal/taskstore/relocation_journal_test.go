package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func relocationJournalFixture(t *testing.T, board string) relocationJournal {
	t.Helper()
	p, err := boardpolicy.New(boardpolicy.Declaration{Relocations: []boardpolicy.Transition{{From: "plan", To: []string{"todo"}}}})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: PLAN-001\ntitle: Example\n---\n| **Status** | [x] Done |\n")
	patched, changed, err := prepareRelocationPatch(raw, "PLAN-1", "plan/card.md", "todo/card.md", p)
	if err != nil {
		t.Fatal(err)
	}
	return relocationJournal{SchemaVersion: 1, BoardPath: board, Records: []relocationRecord{{
		Kind: "pending", RequestID: strings.Repeat("a", 32), ID: "PLAN-1", Owner: "tester",
		Source: "plan/card.md", Target: "todo/card.md", ExpectedSHA256: bytesDigest(raw), Mode: 0o640,
		Original: raw, Patched: patched, Changed: changed, PolicyCanonical: canonical,
		PolicyDigest: bytesDigest(canonical), BoardPath: board,
	}}}
}

func TestRelocationJournalRoundtripAndPublication(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j := relocationJournalFixture(t, dir)
	if err := saveRelocationJournal(r, j, true); err != nil {
		t.Fatal(err)
	}
	if err := saveRelocationJournal(r, completedRelocationJournal(j), true); err == nil {
		t.Fatal("initial publication overwrote pending journal")
	}
	loaded, err := loadRelocationJournal(r)
	if err != nil || loaded.Records[0].Kind != "pending" {
		t.Fatalf("load=%+v err=%v", loaded, err)
	}
	if err := saveRelocationJournal(r, completedRelocationJournal(j), false); err != nil {
		t.Fatal(err)
	}
	loaded, err = loadRelocationJournal(r)
	if err != nil || loaded.Records[0].Kind != "completed" || len(loaded.Records[0].Original) != 0 || !bytes.Equal(loaded.Records[0].PolicyCanonical, j.Records[0].PolicyCanonical) {
		t.Fatalf("completed load=%+v err=%v", loaded, err)
	}
}

func TestRelocationJournalRejectsInvalidRecords(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*relocationJournal){
		"duplicate": func(j *relocationJournal) { j.Records = append(j.Records, j.Records[0]) },
		"multiple-pending": func(j *relocationJournal) {
			rec := j.Records[0]
			rec.RequestID = strings.Repeat("b", 32)
			j.Records = append(j.Records, rec)
		},
		"board":   func(j *relocationJournal) { j.Records[0].BoardPath += "/other" },
		"id":      func(j *relocationJournal) { j.Records[0].ID = "PLAN-2" },
		"digest":  func(j *relocationJournal) { j.Records[0].ExpectedSHA256 = strings.Repeat("0", 64) },
		"patch":   func(j *relocationJournal) { j.Records[0].Patched = []byte("wrong") },
		"changed": func(j *relocationJournal) { j.Records[0].Changed = false },
		"policy":  func(j *relocationJournal) { j.Records[0].PolicyCanonical = append(j.Records[0].PolicyCanonical, '\n') },
		"target":  func(j *relocationJournal) { j.Records[0].Target = "done/card.md" },
		"payload": func(j *relocationJournal) { j.Records[0].Kind = "completed" },
		"mode":    func(j *relocationJournal) { j.Records[0].Mode = 0o1640 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			j := relocationJournalFixture(t, t.TempDir())
			mutate(&j)
			if _, err := relocationJournalBytes(j); err == nil {
				t.Fatal("invalid record accepted")
			}
		})
	}
}

func TestRelocationJournalStrictWire(t *testing.T) {
	t.Parallel()
	j := relocationJournalFixture(t, t.TempDir())
	raw, err := relocationJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1),
		bytes.Replace(raw, []byte(`"changed":true`), []byte(`"changed":null`), 1),
		bytes.Replace(raw, []byte(`"token":""`), []byte(`"token":"","unknown":true`), 1),
		bytes.Replace(raw, []byte(`"owner":"tester",`), nil, 1),
		bytes.Replace(raw, []byte(`"owner":"tester"`), []byte(`"owner":"\ud800"`), 1),
		bytes.Replace(raw, []byte(`"source":"plan/card.md"`), []byte(`"source":"plan/\udc00.md"`), 1),
		append(append([]byte{}, raw...), []byte("{}")...),
	} {
		if bytes.Equal(bad, raw) {
			t.Fatal("mutation fixture did not change bytes")
		}
		if _, err := decodeRelocationJournal(bad); err == nil {
			t.Fatalf("invalid wire accepted: %s", bad)
		}
	}
	// Keep the decoded payload identical; only its wire type changes. JSON's
	// []byte decoder otherwise accepts both base64 strings and numeric arrays.
	for _, field := range []string{"policyCanonical", "original", "patched"} {
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		var records []map[string]json.RawMessage
		if err := json.Unmarshal(wire["records"], &records); err != nil {
			t.Fatal(err)
		}
		var payload []byte
		if err := json.Unmarshal(records[0][field], &payload); err != nil {
			t.Fatal(err)
		}
		numbers := make([]int, len(payload))
		for i, b := range payload {
			numbers[i] = int(b)
		}
		records[0][field], err = json.Marshal(numbers)
		if err != nil {
			t.Fatal(err)
		}
		wire["records"], err = json.Marshal(records)
		if err != nil {
			t.Fatal(err)
		}
		bad, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeRelocationJournal(bad); err == nil {
			t.Fatalf("numeric array accepted for %s", field)
		}
	}
	completed, err := json.Marshal(completedRelocationJournal(j))
	if err != nil {
		t.Fatal(err)
	}
	bad := bytes.Replace(completed, []byte(`"kind":"completed"`), []byte(`"kind":"completed","original":""`), 1)
	if _, err := decodeRelocationJournal(bad); err == nil {
		t.Fatal("completed empty payload field accepted")
	}
	for _, owner := range []string{`\ud83d\ude00`, `literal\\ud800`} {
		valid := bytes.Replace(raw, []byte(`"owner":"tester"`), []byte(`"owner":"`+owner+`"`), 1)
		if _, err := decodeRelocationJournal(valid); err != nil {
			t.Fatalf("valid escaped owner %s rejected: %v", owner, err)
		}
	}
	exact := append(append([]byte{}, raw...), bytes.Repeat([]byte(" "), maxRepairsBytes-len(raw))...)
	if _, err := decodeRelocationJournal(exact); err != nil {
		t.Fatalf("exact journal limit rejected: %v", err)
	}
	if _, err := decodeRelocationJournal(append(exact, ' ')); err == nil {
		t.Fatal("oversized journal accepted")
	}
}
