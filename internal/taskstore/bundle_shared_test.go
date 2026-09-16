package taskstore

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func sharedBundleRecordFixture(t *testing.T, s *sharedSession, reserved []string) bundleRecord {
	t.Helper()
	req, intent := bundleFixture(t)
	doc := parsedBundle(t, req)
	base := idLedger{SchemaVersion: 3, Namespace: s.state.NamespaceID, Reserved: reserved}
	prepared, err := prepareTaskBundle(doc, nil, base, intent)
	if err != nil {
		t.Fatal(err)
	}
	request, _ := doc.Canonical()
	intentRaw, _ := intent.Canonical()
	baseRaw, _ := ledgerBytes(base)
	targetRaw, _ := ledgerBytes(prepared.Ledger)
	batchRaw, _ := prepared.Batch.Canonical()
	policyDigest, _ := currentPolicy().Digest()
	owner := filepath.Clean(filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board)))
	record := bundleRecord{Status: "pending", RequestID: req.RequestID, RequestDigest: prepared.RequestDigest, Request: request, Intent: intentRaw, OriginalIDs: baseRaw, BaseIDs: baseRaw, TargetIDs: targetRaw, Batch: batchRaw, PolicyDigest: policyDigest, Namespace: s.state.NamespaceID, Owner: owner}
	for i, item := range prepared.Cards {
		record.Cards = append(record.Cards, bundleCard{Key: req.Tasks[i].Key, ID: item.Entry.Card.ID, Path: item.Entry.Path, Raw: item.Raw})
	}
	if err := validateBundleRecord(record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSharedBundleReserveFinishAndIdempotentCompletion(t *testing.T) {
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.SchemaVersion, state.BundleProtocol = 2, 1
	if err := publishSharedState(s.root, state, false); err != nil {
		release()
		t.Fatal(err)
	}
	s.state = &state
	r, err := openBoard(board)
	if err != nil {
		release()
		t.Fatal(err)
	}
	defer r.Close()
	record := sharedBundleRecordFixture(t, s, []string{"TASK-1"})
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("b", 32), Records: []bundleRecord{record}}
	if err := s.reserveBundle(r, journal, record); err != nil {
		release()
		t.Fatal(err)
	}
	if s.state.PendingBundle == nil || !sameStringSlice(s.state.PendingBundle.IDs, []string{"TASK-2"}) {
		release()
		t.Fatalf("pending=%+v", s.state.PendingBundle)
	}
	beforeCommon, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		release()
		t.Fatal(err)
	}
	badJournal := journal
	badJournal.BoardID = strings.Repeat("c", 32)
	if _, err := s.prepareBundleReservation(r, badJournal, record); err == nil {
		t.Fatal("board identity mismatch accepted")
	}
	badRecord := record
	badRecord.Namespace = strings.Repeat("d", 32)
	if _, err := s.prepareBundleReservation(r, journal, badRecord); err == nil {
		t.Fatal("namespace mismatch accepted")
	}
	afterCommon, err := s.root.ReadFile(sharedStateFile)
	if err != nil || !bytes.Equal(beforeCommon, afterCommon) {
		release()
		t.Fatalf("failed preflight changed common state: %v", err)
	}
	if err := s.finishBundle(r, journal, record); err == nil {
		release()
		t.Fatal("pending record cleared shared reservation")
	}
	completed := record
	completed.Status = "completed"
	if err := s.finishBundle(r, journal, completed); err != nil {
		release()
		t.Fatal(err)
	}
	if s.state.PendingBundle != nil {
		release()
		t.Fatal("finish did not clear pending bundle")
	}
	if err := s.finishBundle(r, journal, completed); err != nil {
		release()
		t.Fatalf("completed retry=%v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestSharedBundleCopiedOwnerAndCollisionRejected(t *testing.T) {
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.SchemaVersion, state.BundleProtocol = 2, 1
	if err := publishSharedState(s.root, state, false); err != nil {
		release()
		t.Fatal(err)
	}
	s.state = &state
	r, err := openBoard(board)
	if err != nil {
		release()
		t.Fatal(err)
	}
	record := sharedBundleRecordFixture(t, s, []string{"TASK-1"})
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("c", 32), Records: []bundleRecord{record}}
	collision := sharedBundleRecordFixture(t, s, []string{})
	if err := s.reserveBundle(r, journal, collision); err == nil || !strings.Contains(err.Error(), "already reserved") {
		r.Close()
		release()
		t.Fatalf("reserved collision accepted: %v", err)
	}
	if err := s.reserveBundle(r, journal, record); err != nil {
		r.Close()
		release()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	s2, release2, err := acquireSharedForBundle(other, false, true)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := openBoard(other)
	if err != nil {
		release2()
		t.Fatal(err)
	}
	defer r2.Close()
	if err := s2.verifyBundleOwner(r2, journal, record); err == nil || !strings.Contains(err.Error(), "owner") {
		release2()
		t.Fatalf("copied owner accepted: %v", err)
	}
	if err := release2(); err != nil {
		t.Fatal(err)
	}

}

func TestSharedBundleCopiedOwnerRejectedBeforePending(t *testing.T) {
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.SchemaVersion, state.BundleProtocol = 2, 1
	if err := publishSharedState(s.root, state, false); err != nil {
		release()
		t.Fatal(err)
	}
	s.state = &state
	r, err := openBoard(board)
	if err != nil {
		release()
		t.Fatal(err)
	}
	record := sharedBundleRecordFixture(t, s, []string{"TASK-1"})
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("a", 32), Records: []bundleRecord{record}}
	if err := r.Close(); err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	s2, release2, err := acquireSharedForBundle(other, false, true)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := openBoard(other)
	if err != nil {
		release2()
		t.Fatal(err)
	}
	defer r2.Close()
	before, err := s2.root.ReadFile(sharedStateFile)
	if err != nil {
		release2()
		t.Fatal(err)
	}
	if _, err := s2.prepareBundleReservation(r2, journal, record); err == nil || !strings.Contains(err.Error(), "owner") {
		release2()
		t.Fatalf("copied owner accepted before pending: %v", err)
	}
	if err := s2.reserveBundle(r2, journal, record); err == nil || !strings.Contains(err.Error(), "owner") {
		release2()
		t.Fatalf("copied owner reserved before pending: %v", err)
	}
	after, err := s2.root.ReadFile(sharedStateFile)
	if err != nil || !bytes.Equal(before, after) {
		release2()
		t.Fatalf("copied owner changed common state: %v", err)
	}
	if err := release2(); err != nil {
		t.Fatal(err)
	}
}

func TestLocalBundleSharedPrimitivesDoNotMutateOrAcceptNamespace(t *testing.T) {
	var s *sharedSession
	record := bundleRecordFixture(t)
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("d", 32), Records: []bundleRecord{record}}
	if err := s.verifyBundleOwner(nil, journal, record); err != nil {
		t.Fatal(err)
	}
	if err := s.reserveBundle(nil, journal, record); err != nil {
		t.Fatal(err)
	}
	completed := record
	completed.Status = "completed"
	if err := s.finishBundle(nil, journal, completed); err != nil {
		t.Fatal(err)
	}
	record.Namespace = strings.Repeat("e", 32)
	if err := s.verifyBundleOwner(nil, journal, record); err == nil {
		t.Fatal("local bundle accepted shared namespace")
	}
}

func TestPrepareBundleReservationIsPureAndUpgradesOnlyReturnedState(t *testing.T) {
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	r, err := openBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state := *s.state
	state.SchemaVersion, state.BundleProtocol = 2, 1
	s.state = &state
	record := sharedBundleRecordFixture(t, s, []string{"TASK-1"})
	journal := bundleJournal{SchemaVersion: 1, BoardID: strings.Repeat("f", 32), Records: []bundleRecord{record}}
	before := *s.state
	next, err := s.prepareBundleReservation(r, journal, record)
	if err != nil || next == nil {
		t.Fatalf("prepared=%+v err=%v", next, err)
	}
	if s.state.PendingBundle != nil || !sameStringSlice(s.state.Reserved, before.Reserved) {
		t.Fatal("prepare mutated shared state")
	}
	if next.PendingBundle == nil || !sameStringSlice(next.PendingBundle.IDs, []string{"TASK-2"}) {
		t.Fatalf("next=%+v", next)
	}
	legacy := before
	legacy.SchemaVersion, legacy.BundleProtocol = 1, 0
	s.state = &legacy
	next, err = s.prepareBundleReservation(r, journal, record)
	if err != nil || next == nil || next.SchemaVersion != 2 || next.BundleProtocol != 1 {
		t.Fatalf("legacy prepare=%+v err=%v", next, err)
	}
	if err := s.reserveBundle(r, journal, record); err == nil || !strings.Contains(err.Error(), "explicit adoption") {
		t.Fatalf("legacy reserve accepted: %v", err)
	}
}
