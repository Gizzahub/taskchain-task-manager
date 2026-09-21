package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// The consumer parity contract turns on one expression,
// validateTransitionForPolicy's guard in transitions.go: a source that is a
// workflow or parked zone, a destination that is a workflow zone, a source and
// destination that differ, and an edge the policy names. Four of the contract's
// six divergences from the upstream tool are clauses of that single condition,
// and each is already pinned elsewhere: the undeclared edge and the new parking
// target by TestBoundParkingResumeAndMoveOutOnly, the same-zone move by
// TestTransitionInvalidAuthorityPreservesBoard and
// TestKindConsumerSameZoneRequiresRepair, the kind destination by
// TestRelocationPolicyDoesNotExpandExecutionTransitions.
//
// The tests here close what those leave open. They are not a second copy of the
// contract; each one covers an input no existing test constructs.

// The same-zone refusal is not enforced where it reads as if it were.
// validateTransitionForPolicy and validateTransitionRecordWithPolicy both spell
// out req.From == req.To, but neither comparison can decide anything:
// boardpolicy.New refuses a declaration whose transition targets itself, so
// Allows(x, x) is false for every policy that can be constructed, and the
// Allows clause has already rejected the move by the time the comparison is
// read. Deleting both comparisons leaves every existing same-zone test green.
//
// That makes the real invariant a cross-package one, and this pins it there:
// the constructor is what makes a same-zone move unrepresentable, and the
// transition guards inherit the refusal rather than producing it. A future
// change that relaxes the constructor — to let a board declare a self-edge for
// status sync, the behaviour the upstream tool has — would silently reopen the
// same-zone path, and the two comparisons left behind are too weak to notice:
// they never ran in the first place.
func TestSameZoneMoveIsUnrepresentableInAnyPolicy(t *testing.T) {
	t.Parallel()
	// Two different guards cover the two kinds of zone, and both are load
	// bearing: a workflow self-edge is refused as a self-edge, while a parked
	// self-edge is refused earlier for naming a parked destination at all. A
	// same-zone move is unrepresentable either way, but for parked zones that
	// holds only as long as parking stays a non-workflow destination.
	for zone, want := range map[string]string{
		"todo":   "targets itself",
		"doing":  "targets itself",
		"done":   "targets itself",
		"manual": "non-workflow zone",
	} {
		d := boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: zone, To: []string{zone}}}}
		p, err := boardpolicy.New(d)
		if err == nil {
			t.Fatalf("policy declaring %q -> %q was constructed; Allows=%v", zone, zone, p.Allows(zone, zone))
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("zone %q rejected for an unrelated reason: %v, want one naming %q", zone, err, want)
		}
	}
}

// A parked source reaches the transition guard through Parked rather than
// Workflow, so it is the one source kind whose same-zone move no existing test
// constructs. It is refused, but by the Allows clause above rather than by the
// comparison that names the case.
func TestParkedSourceCannotMoveToItself(t *testing.T) {

	t.Parallel()
	dir := claimBoard(t)
	bindRuntimeFixture(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: "manual", To: []string{"todo"}}}})
	if err := os.Mkdir(filepath.Join(dir, "manual"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "todo/TASK-1.md"), filepath.Join(dir, "manual/TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	if _, err := ClaimResume(dir, claim); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	req := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("3", 32), From: "manual", To: "manual"}
	if _, err := Transition(dir, req); err == nil {
		t.Fatal("parked card moved to its own zone; a no-op move is a repair wearing a transition's clothes")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("rejected same-zone parking move changed the board")
	}
}

// supersededFixture stamps the archive fixture's done card as superseded and
// returns a request bound to the stamped bytes, so the request is well formed
// and only the card's status distinguishes it.
func supersededFixture(t *testing.T) (string, ArchiveRequest) {
	t.Helper()
	dir, req, raw := archiveWriterFixture(t)
	stamped := bytes.Replace(raw, []byte("status: pending"), []byte("status: superseded"), 1)
	if bytes.Equal(stamped, raw) {
		t.Fatal("fixture card did not carry the status the stamp replaces")
	}
	if err := os.WriteFile(filepath.Join(dir, "done", "TASK-1.md"), stamped, 0o644); err != nil {
		t.Fatal(err)
	}
	req.ExpectedSHA256 = bytesDigest(stamped)
	return dir, req
}

// Superseding is a separate operation, not an outcome the ordinary archive path
// can produce. Three independent layers say so — archive admission, archive
// record validation, and the completion grant — and disabling any two of them
// still leaves this test green. That redundancy is the point rather than a
// weakness in the assertion: this pins the contract, not a particular guard,
// because a consumer cares that a card set aside cannot collect the completion
// receipt of a card that was finished, not which layer said no. Only the
// completion-grant layer has a test of its own today, so the other two rest on
// this one plus each other.
func TestSupersededCardIsRefusedByOrdinaryArchive(t *testing.T) {
	t.Parallel()
	dir, req := supersededFixture(t)
	before := boardBytes(t, dir)
	_, err := Archive(dir, req, true)
	if err == nil {
		t.Fatal("superseded card entered normal archive admission")
	}
	if !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("error = %v, want one naming the superseded status rather than a generic rejection", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("refused archive changed the board")
	}
}

// The supersede operation itself has no end-to-end coverage: every existing test
// validates a hand-built record instead of driving Archive. This runs the real
// path and pins the two things that separate it from an ordinary archive — the
// card is stamped superseded on the way out, and the result carries no
// completion eligibility. A superseded card was set aside, not finished.
func TestSupersedeStampsAndDeniesCompletionEligibility(t *testing.T) {
	t.Parallel()
	dir, req, raw := archiveWriterFixture(t)
	req.Operation = "supersede"
	result, err := Archive(dir, req, true)
	if err != nil {
		t.Fatalf("supersede refused: %v", err)
	}
	if result.CompletionEligible {
		t.Fatal("supersede reported completion eligibility; being set aside is not being finished")
	}
	if _, err := os.Stat(filepath.Join(dir, "done", "TASK-1.md")); !os.IsNotExist(err) {
		t.Fatalf("source survived supersede: %v", err)
	}
	moved, err := os.ReadFile(filepath.Join(dir, result.Target))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(moved, []byte("status: superseded")) {
		t.Fatalf("target was not stamped superseded:\n%s", moved)
	}
	if bytes.Contains(raw, []byte("status: superseded")) {
		t.Fatal("fixture was already superseded, so the stamp assertion proves nothing")
	}
}
