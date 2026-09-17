package taskstore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestArchiveJournalIndependentDuplicateKeys(t *testing.T) {
	j, first, b, raw := archiveJournalFixture(t)
	first.State, first.Original, first.Patched = "completed", nil, nil
	for _, same := range []string{"request", "identity"} {
		t.Run(same, func(t *testing.T) {
			second := j.Records[0]
			binding := b
			if same == "request" {
				second.ID = "TASK-002"
				second.Source, second.Target = "done/TASK-002.md", "_archive/done/TASK-002.md"
				second.Original = bytes.ReplaceAll(raw, []byte("TASK-001"), []byte("TASK-002"))
				second.Patched = append([]byte(nil), second.Original...)
				second.OriginalSHA256, second.FinalSHA256 = bytesDigest(second.Original), bytesDigest(second.Patched)
				binding.ID, binding.Identity, binding.Source, binding.Target, binding.FinalSHA256 = second.ID, "TASK-2", second.Source, second.Target, second.FinalSHA256
			} else {
				second.RequestID = strings.Repeat("b", 32)
				binding.RequestID = second.RequestID
			}
			second.Completion = &binding
			if err := validateArchiveRecord(second); err != nil {
				t.Fatalf("second record must be valid alone: %v", err)
			}
			candidate := j
			candidate.Records = []archiveRecord{first, second}
			if err := validateArchiveJournal(candidate); err == nil || !strings.Contains(err.Error(), "duplicate archive") {
				t.Fatalf("wrong duplicate refusal: %v", err)
			}
		})
	}
}

func TestArchiveJournalCrossBindingWithIndependentlyValidPolicies(t *testing.T) {
	_, r, b, raw := archiveJournalFixture(t)
	for _, which := range []string{"policy", "rules"} {
		t.Run(which, func(t *testing.T) {
			changed := b
			if which == "policy" {
				p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
				if err != nil {
					t.Fatal(err)
				}
				changed.PolicyCanonical, err = p.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				changed.PolicyDigest = bytesDigest(changed.PolicyCanonical)
			} else {
				cfg, err := archivepolicy.ParseConfig(b.RulesCanonical)
				if err != nil {
					t.Fatal(err)
				}
				cfg.Admission.AcceptedReviews = []string{"pass", "waived"}
				changed.RulesCanonical, err = cfg.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				changed.RulesDigest = bytesDigest(changed.RulesCanonical)
			}
			if err := verifyArchiveCompletion(changed, changed.BoardPath, changed.Target, raw); err != nil {
				t.Fatalf("binding must be valid alone: %v", err)
			}
			candidate := r
			candidate.Completion = &changed
			if err := validateArchiveRecordCompletion(candidate); err == nil || !strings.Contains(err.Error(), "receipt and completion binding disagree") {
				t.Fatalf("wrong cross-binding refusal: %v", err)
			}
		})
	}
}
