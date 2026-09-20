package taskstore

import (
	"bytes"
	"testing"
)

func TestPolicyHistorySurvivesBundleAdoption(t *testing.T) {
	t.Parallel()
	board := configuredFixture(t)
	if _, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	history := promoteHistoryFixture(t, board)
	raw := publicationRequest(t, board)
	if _, err := PublishBundle(board, raw, BundleOptions{Adopt: true}); err != nil {
		t.Fatal(err)
	}
	assertHistoryFixture(t, board, history)
}

func TestPolicyHistorySurvivesRepairAdoption(t *testing.T) {
	t.Parallel()
	board, req, _, _ := statusRepairFixture(t)
	if _, err := ActivatePolicy(board, defaultPolicyBytes(t), PolicyActivationOptions{}); err != nil {
		t.Fatal(err)
	}
	history := promoteHistoryFixture(t, board)
	if _, err := RepairStatus(board, req, true); err != nil {
		t.Fatal(err)
	}
	assertHistoryFixture(t, board, history)
}

func TestPolicyHistorySurvivesRelocationAdoption(t *testing.T) {
	t.Parallel()
	board, req := relocationBoardFixture(t)
	history := promoteHistoryFixture(t, board)
	if _, err := Relocate(board, req, true); err != nil {
		t.Fatal(err)
	}
	assertHistoryFixture(t, board, history)
}

func TestPolicyHistoryCanonicalBytesAndJournalAccounting(t *testing.T) {
	t.Parallel()
	j, current := historyJournalFixture(t)
	oldDigest := j.Records[0].PolicyDigest
	noncanonical := append(bytes.Clone(j.PolicyHistory[oldDigest]), '\n')
	delete(j.PolicyHistory, oldDigest)
	j.Records[0].PolicyDigest = bytesDigest(noncanonical)
	j.PolicyHistory[bytesDigest(noncanonical)] = noncanonical
	if _, err := encodeTransitionJournal(j, current); err == nil {
		t.Fatal("valid but noncanonical history bytes accepted")
	}

	base := journalFixture(t)
	if err := validateTransitionCapacity(base); err != nil {
		t.Fatalf("small journal rejected: %v", err)
	}
	key := bytes.Repeat([]byte("a"), 64)
	firstRejected := maxTransitionBytes
	lo := 0
	hi := maxTransitionBytes
	for lo < hi {
		mid := lo + (hi-lo)/2
		base.PolicyHistory = map[string][]byte{string(key): bytes.Repeat([]byte("x"), mid)}
		if err := validateTransitionCapacity(base); err != nil {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	firstRejected = lo
	if firstRejected == 0 || firstRejected >= maxTransitionBytes {
		t.Fatalf("did not find journal capacity boundary: %d", firstRejected)
	}
	base.PolicyHistory = map[string][]byte{string(key): bytes.Repeat([]byte("x"), firstRejected-1)}
	if err := validateTransitionCapacity(base); err != nil {
		t.Fatalf("journal below 8 MiB boundary rejected: %v", err)
	}
	base.PolicyHistory = map[string][]byte{string(key): bytes.Repeat([]byte("x"), firstRejected)}
	if err := validateTransitionCapacity(base); err == nil {
		t.Fatal("journal over 8 MiB boundary accepted")
	}
}
