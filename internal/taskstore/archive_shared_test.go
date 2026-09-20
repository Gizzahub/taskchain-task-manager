package taskstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func sharedArchiveFixture(t *testing.T) (sharedState, sharedRepairPending) {
	t.Helper()
	state := validSharedV3Fixture(t)
	state.BoardPath = "tasks"
	state.NamespaceID = strings.Repeat("a", 32)
	state.StorageProtocol = 3
	j, pending, _, _ := archiveJournalFixture(t)
	j.Namespace = state.NamespaceID
	for i := range j.Records {
		j.Records[i].Namespace = state.NamespaceID
	}
	target, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	base := j
	base.Records = []archiveRecord{}
	original, err := archiveJournalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	p := sharedRepairPending{Owner: j.BoardPath, RequestID: pending.RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: target}
	state.Reserved = []string{"PLAN-2", "TASK-1"}
	state.PendingArchive = &p
	return state, p
}

func TestSharedArchiveReservationValidatesCanonicalScopeAndReceipts(t *testing.T) {
	t.Parallel()
	state, _ := sharedArchiveFixture(t)
	if err := validateSharedState(state); err != nil {
		t.Fatalf("valid archive reservation rejected: %v", err)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSharedShape(raw); err != nil {
		t.Fatalf("archive reservation wire rejected: %v", err)
	}
}

func TestSharedArchiveReservationRejectsNumericPayload(t *testing.T) {
	t.Parallel()
	state, pending := sharedArchiveFixture(t)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSharedShape(raw); err != nil {
		t.Fatal(err)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(root["pendingArchive"], &fields); err != nil {
		t.Fatal(err)
	}
	numbers := make([]int, len(pending.TargetJournal))
	for i, b := range pending.TargetJournal {
		numbers[i] = int(b)
	}
	fields["targetJournal"], err = json.Marshal(numbers)
	if err != nil {
		t.Fatal(err)
	}
	root["pendingArchive"], err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	// Prove the alternative encoding carries otherwise valid semantic data.
	var decoded sharedState
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedState(decoded); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedShape(raw); err == nil || !strings.Contains(err.Error(), "base64 string") {
		t.Fatalf("numeric payload error=%v", err)
	}
}

func TestSharedArchiveReservationRejectsConflictsAndDowngrade(t *testing.T) {
	t.Parallel()
	base, _ := sharedArchiveFixture(t)
	for name, mutate := range map[string]func(*sharedState){
		"protocol downgrade": func(s *sharedState) { s.StorageProtocol = 2 },
		"repair conflict":    func(s *sharedState) { s.PendingRepair = &sharedRepairPending{} },
		"relocation conflict": func(s *sharedState) {
			s.PendingRelocation = &sharedRepairPending{}
		},
		"bundle conflict": func(s *sharedState) {
			s.PendingBundle = &sharedBundlePending{}
		},
		"policy conflict": func(s *sharedState) { s.Policy.Phase = "initializing" },
	} {
		t.Run(name, func(t *testing.T) {
			state := base
			if base.Policy != nil {
				policy := *base.Policy
				state.Policy = &policy
			}
			mutate(&state)
			if err := validateSharedArchive(state); err == nil {
				t.Fatal("conflicting archive reservation accepted")
			}
		})
	}
}

func TestSharedArchiveReservationRejectsScopeIDAndReceiptTampering(t *testing.T) {
	t.Parallel()
	base, _ := sharedArchiveFixture(t)
	for name, mutate := range map[string]func(*sharedState){
		"owner": func(s *sharedState) { p := *s.PendingArchive; p.Owner = "/other/tasks"; s.PendingArchive = &p },
		"request": func(s *sharedState) {
			p := *s.PendingArchive
			p.RequestID = strings.Repeat("b", 32)
			s.PendingArchive = &p
		},
		"namespace": func(s *sharedState) { s.NamespaceID = strings.Repeat("b", 32) },
		"reserved":  func(s *sharedState) { s.Reserved = []string{"PLAN-2"} },
		"receipt hash": func(s *sharedState) {
			p := *s.PendingArchive
			p.OriginalJournalSHA256 = strings.Repeat("0", 64)
			s.PendingArchive = &p
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if err := validateSharedState(state); err == nil {
				t.Fatal("tampered archive reservation accepted")
			}
		})
	}
	badShape := base
	badShape.PendingArchive = nil
	raw, err := json.Marshal(badShape)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("pendingArchive")) {
		t.Fatal("nil pending archive unexpectedly encoded")
	}
}

func TestSharedRelocationReservationAcceptsProtocol3(t *testing.T) {
	t.Parallel()
	namespace := strings.Repeat("a", 32)
	j := relocationJournalFixture(t, "/synthetic/tasks")
	j.Records[0].Namespace = namespace
	target, err := relocationJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	base := j
	base.Records = []relocationRecord{}
	original, err := relocationJournalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	s := sharedState{StorageProtocol: 3, Phase: "active", NamespaceID: namespace, Reserved: []string{"PLAN-1"}, PendingRelocation: &sharedRepairPending{Owner: j.BoardPath, RequestID: j.Records[0].RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: target}}
	if err := validateSharedRelocation(s); err != nil {
		t.Fatalf("protocol 3 relocation reservation rejected: %v", err)
	}
}

func TestAcquireSharedArchiveRecoveryGate(t *testing.T) {
	t.Parallel()
	_, board, other := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, []byte(relocationPolicyFixture), PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.state == nil {
		release()
		t.Fatal("missing active shared state")
	}
	j, pending, _, _ := archiveJournalFixture(t)
	namespace := s.state.NamespaceID
	j.BoardPath = board
	j.Namespace = namespace
	j.Records[0].BoardPath = board
	j.Records[0].Namespace = namespace
	j.Records[0].Completion.BoardPath = board
	target, err := archiveJournalBytes(j)
	if err != nil {
		release()
		t.Fatal(err)
	}
	base := j
	base.Records = []archiveRecord{}
	original, err := archiveJournalBytes(base)
	if err != nil {
		release()
		t.Fatal(err)
	}
	next := *s.state
	next.StorageProtocol = 3
	next.PendingArchive = &sharedRepairPending{Owner: board, RequestID: pending.RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: target}
	if err := publishSharedState(s.root, next, false); err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, blockedRelease, err := acquireShared(board, false); err == nil {
		blockedRelease()
		t.Fatal("default shared acquisition bypassed pending archive")
	}
	recovered, recoveredRelease, err := acquireSharedArchiveOptions(board, false, false, false, false, false, true)
	if err != nil || recovered == nil || recovered.state == nil {
		t.Fatalf("archive recovery acquisition failed: session=%v err=%v", recovered, err)
	}
	if err := recoveredRelease(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, other)
	if _, err := Create(other, CreateRequest{Title: "blocked while archive pending"}); err == nil {
		t.Fatal("other worktree bypassed pending archive")
	}
	if !reflectEqualBoard(before, boardBytes(t, other)) {
		t.Fatal("blocked writer changed other board")
	}
}
