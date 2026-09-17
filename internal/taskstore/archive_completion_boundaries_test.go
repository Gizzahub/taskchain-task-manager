package taskstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestArchiveCompletionExactHistoricalCanonicalBindings(t *testing.T) {
	b, raw, _, _ := archiveCompletionFixture(t)
	for _, tc := range []struct {
		name, want string
		change     func(*archiveCompletionBinding)
	}{
		{"policy digest", "policy binding mismatch", func(b *archiveCompletionBinding) { b.PolicyDigest = strings.Repeat("0", 64) }},
		{"rules digest", "rules binding mismatch", func(b *archiveCompletionBinding) { b.RulesDigest = strings.Repeat("0", 64) }},
		{"policy noncanonical", "policy binding mismatch", func(b *archiveCompletionBinding) {
			b.PolicyCanonical = append(b.PolicyCanonical, '\n')
			b.PolicyDigest = bytesDigest(b.PolicyCanonical)
		}},
		{"rules noncanonical", "rules binding mismatch", func(b *archiveCompletionBinding) {
			b.RulesCanonical = append(b.RulesCanonical, '\n')
			b.RulesDigest = bytesDigest(b.RulesCanonical)
		}},
		{"source not done", "normal workflow-done", func(b *archiveCompletionBinding) {
			b.Source = "review/TASK-001.md"
			b.Target = "_archive/review/TASK-001.md"
		}},
		{"normal assertion", "normal workflow-done", func(b *archiveCompletionBinding) { b.Assertion = "operator asserted" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := b
			tc.change(&copy)
			if err := verifyArchiveCompletion(copy, copy.BoardPath, copy.Target, raw); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wrong refusal: %v", err)
			}
		})
	}
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	b.PolicyCanonical, err = policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	b.PolicyDigest = bytesDigest(b.PolicyCanonical)
	b.Source = "backend/done/auth/TASK-001.md"
	b.Target = "backend/_archive/done/auth/TASK-001.md"
	if err := verifyArchiveCompletion(b, b.BoardPath, b.Target, raw); err != nil {
		t.Fatal(err)
	}
	rules, err := archivepolicy.ParseConfig(b.RulesCanonical)
	if err != nil {
		t.Fatal(err)
	}
	rules.Admission.AcceptedReviews = []string{"different-verdict"}
	b.RulesCanonical, err = rules.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	b.RulesDigest = bytesDigest(b.RulesCanonical)
	if err := verifyArchiveCompletion(b, b.BoardPath, b.Target, raw); err == nil || !strings.Contains(err.Error(), "historical admission") {
		t.Fatalf("rules did not govern admission: %v", err)
	}
}

func TestArchiveCompletionStrictWireBoundaries(t *testing.T) {
	b, _, _, _ := archiveCompletionFixture(t)
	valid := archiveCompletionJSON(t, b)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(valid, &fields); err != nil {
		t.Fatal(err)
	}
	delete(fields, "identity")
	missing, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{
		missing,
		append(append([]byte(nil), valid...), []byte(" {}")...),
		archiveCompletionArrayJSON(t, b, "rulesCanonical", b.RulesCanonical),
		archiveCompletionFieldJSON(t, b, "rulesCanonical", `"!invalid-base64!"`),
		archiveCompletionFieldJSON(t, b, "schemaVersion", `"1"`),
		archiveCompletionFieldJSON(t, b, "schemaVersion", `2`),
		archiveCompletionFieldJSON(t, b, "identity", `"TASK-001"`),
		append(append([]byte(nil), valid...), []byte(strings.Repeat(" ", archiveCompletionLimit+1-len(valid)))...),
	} {
		if _, err := decodeArchiveCompletion(raw); err == nil {
			t.Fatal("accepted invalid completion wire")
		}
	}
}
