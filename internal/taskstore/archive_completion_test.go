package taskstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const archiveCompletionRulesFixture = `schema-version: 1
archive-admission:
  fields:
    review: review-result
    evidence: review-proof
    resolution: disposition
    promoted-to: promoted
    children: child-ids
  accepted-reviews: [pass, conditional, waived]
`

func archiveCompletionFixture(t *testing.T) (archiveCompletionBinding, []byte, []byte, []byte) {
	t.Helper()
	policy, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	rules, err := archivepolicy.ParseConfig([]byte(archiveCompletionRulesFixture))
	if err != nil {
		t.Fatal(err)
	}
	rulesRaw, err := rules.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	card := []byte("---\nid: TASK-001\ntitle: archived\nreview-result: pass\nreview-proof: independent review\n---\n\n# archived\n")
	b := archiveCompletionBinding{
		SchemaVersion: 1, Provenance: "workflow-done", RequestID: strings.Repeat("a", 32), BoardPath: "/synthetic/tasks",
		ID: "TASK-001", Identity: "TASK-1", Source: "done/TASK-001.md", Target: "_archive/done/TASK-001.md", FinalSHA256: bytesDigest(card),
		PolicyCanonical: policy, PolicyDigest: bytesDigest(policy), RulesCanonical: rulesRaw, RulesDigest: bytesDigest(rulesRaw),
	}
	return b, card, policy, rulesRaw
}

func archiveCompletionJSON(t *testing.T, b archiveCompletionBinding) []byte {
	t.Helper()
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func archiveCompletionFieldJSON(t *testing.T, b archiveCompletionBinding, field, value string) []byte {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(archiveCompletionJSON(t, b), &fields); err != nil {
		t.Fatal(err)
	}
	fields[field] = json.RawMessage(value)
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestArchiveCompletionWorkflowBindingAndCurrentCard(t *testing.T) {
	b, card, _, _ := archiveCompletionFixture(t)
	raw, err := archiveCompletionBytes(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeArchiveCompletion(raw)
	if err != nil || got.Identity != "TASK-1" {
		t.Fatalf("decoded=%+v err=%v", got, err)
	}
	if err := verifyArchiveCompletion(got, b.BoardPath, b.Target, card); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveCompletionDecodeRejectsStrictWireShapes(t *testing.T) {
	b, _, _, _ := archiveCompletionFixture(t)
	valid := archiveCompletionJSON(t, b)
	legacy := b
	legacy.Provenance = "legacy-completion"
	legacy.Source, legacy.Target = "_archive/TASK-001.md", "_archive/TASK-001.md"
	legacy.Assertion = "operator attestation"
	if _, err := archiveCompletionBytes(legacy); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"unknown field":   append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"extra":1}`)...),
		"duplicate field": append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"id":"TASK-001"}`)...),
		"null canonical":  archiveCompletionFieldJSON(t, b, "policyCanonical", "null"),
		"array canonical": archiveCompletionArrayJSON(t, b, "policyCanonical", b.PolicyCanonical),
		"surrogate":       archiveCompletionFieldJSON(t, legacy, "assertion", `"\ud800"`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeArchiveCompletion(raw); err == nil {
				t.Fatal("malformed completion accepted")
			}
		})
	}
}

func archiveCompletionArrayJSON(t *testing.T, b archiveCompletionBinding, field string, payload []byte) []byte {
	t.Helper()
	values := make([]int, len(payload))
	for i, v := range payload {
		values[i] = int(v)
	}
	raw, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return archiveCompletionFieldJSON(t, b, field, string(raw))
}

func TestArchiveCompletionRejectsMalformedBindingsAndAdmissionLoss(t *testing.T) {
	b, card, policy, rules := archiveCompletionFixture(t)
	cases := []struct {
		name   string
		mutate func(*archiveCompletionBinding, *[]byte)
	}{
		{"malformed policy", func(x *archiveCompletionBinding, _ *[]byte) {
			x.PolicyCanonical = []byte("not policy")
			x.PolicyDigest = strings.Repeat("0", 64)
		}},
		{"malformed rules", func(x *archiveCompletionBinding, _ *[]byte) {
			x.RulesCanonical = []byte("not rules")
			x.RulesDigest = strings.Repeat("0", 64)
		}},
		{"superseded provenance", func(x *archiveCompletionBinding, _ *[]byte) { x.Provenance = "superseded" }},
		{"forced provenance", func(x *archiveCompletionBinding, _ *[]byte) { x.Provenance = "forced" }},
		{"non TASK", func(x *archiveCompletionBinding, _ *[]byte) { x.ID = "ISSUE-1"; x.Identity = "ISSUE-1" }},
		{"lost review", func(x *archiveCompletionBinding, raw *[]byte) {
			*raw = []byte("---\nid: TASK-001\ntitle: archived\n---\n")
			x.FinalSHA256 = bytesDigest(*raw)
		}},
		{"lost evidence", func(x *archiveCompletionBinding, raw *[]byte) {
			*raw = []byte("---\nid: TASK-001\ntitle: archived\nreview-result: pass\n---\n")
			x.FinalSHA256 = bytesDigest(*raw)
		}},
		{"identity spelling mismatch", func(x *archiveCompletionBinding, _ *[]byte) { x.ID = "TASK-01"; x.Identity = "TASK-1" }},
		{"superseded current card", func(x *archiveCompletionBinding, raw *[]byte) {
			*raw = []byte("---\nid: TASK-001\ntitle: archived\nstatus: superseded\nreview-result: pass\nreview-proof: independent review\n---\n")
			x.FinalSHA256 = bytesDigest(*raw)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			copyBinding := b
			copyBinding.PolicyCanonical = append([]byte(nil), policy...)
			copyBinding.RulesCanonical = append([]byte(nil), rules...)
			current := append([]byte(nil), card...)
			tc.mutate(&copyBinding, &current)
			if err := verifyArchiveCompletion(copyBinding, b.BoardPath, copyBinding.Target, current); err == nil {
				t.Fatal("invalid completion binding accepted")
			}
		})
	}
}

func TestArchiveCompletionRequiresExactCurrentBoardPathAndBytes(t *testing.T) {
	b, card, _, _ := archiveCompletionFixture(t)
	cases := []struct {
		name  string
		board string
		path  string
		raw   []byte
	}{
		{"board", "/other/tasks", b.Target, card},
		{"path", b.BoardPath, "_archive/done/TASK-002.md", card},
		{"hash", b.BoardPath, b.Target, append(append([]byte(nil), card...), '\n')},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyArchiveCompletion(b, tc.board, tc.path, tc.raw); err == nil || !strings.Contains(err.Error(), "current card or board mismatch") {
				t.Fatalf("mismatch accepted or wrong error: %v", err)
			}
		})
	}
	spelling := b
	spelling.ID = "TASK-01"
	spelling.Identity = "TASK-1"
	if err := verifyArchiveCompletion(spelling, b.BoardPath, b.Target, card); err == nil || !strings.Contains(err.Error(), "raw identity mismatch") {
		t.Fatalf("normalized spelling mismatch accepted or wrong error: %v", err)
	}
}

func TestArchiveCompletionLegacyAndArchivedPathRules(t *testing.T) {
	b, card, _, _ := archiveCompletionFixture(t)
	b.Provenance = "legacy-completion"
	b.Source = "_archive/TASK-001.md"
	b.Target = b.Source
	b.Assertion = "legacy archive evidence"
	if _, err := archiveCompletionBytes(b); err != nil {
		t.Fatal("valid legacy binding rejected:", err)
	}
	if err := verifyArchiveCompletion(b, b.BoardPath, b.Target, card); err != nil {
		t.Fatal("valid legacy verification rejected:", err)
	}
	superseded := []byte("---\nid: TASK-001\ntitle: archived\nstatus: superseded\n---\n")
	b.FinalSHA256 = bytesDigest(superseded)
	if err := verifyArchiveCompletion(b, b.BoardPath, b.Target, superseded); err == nil {
		t.Fatal("superseded legacy card accepted")
	}
	for name, mutate := range map[string]func(*archiveCompletionBinding){
		"missing assertion": func(x *archiveCompletionBinding) { x.Assertion = "" },
		"nonarchive target": func(x *archiveCompletionBinding) { x.Target = "todo/TASK-001.md"; x.Source = x.Target },
	} {
		t.Run(name, func(t *testing.T) {
			copyBinding := b
			mutate(&copyBinding)
			if err := validateArchiveCompletion(copyBinding); err == nil {
				t.Fatal("invalid legacy binding accepted")
			}
		})
	}
}
