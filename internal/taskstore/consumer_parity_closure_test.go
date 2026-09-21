package taskstore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// The consumer parity contract's divergences from the upstream tool are almost
// all already pinned: the undeclared edge and the new parking target by
// TestBoundParkingResumeAndMoveOutOnly, the kind destination by
// TestRelocationPolicyDoesNotExpandExecutionTransitions, supersede end to end
// by TestArchiveWriterMovesNormalCardAndReplays, and the archive admission
// refusal of a superseded card by TestArchiveAdmissionRejectsMismatchedSource.
// What follows covers the two cases that survived a search for gaps.

// The same-zone refusal is not enforced where it reads as if it were.
// validateTransitionForPolicy and validateTransitionRecordWithPolicy both spell
// out From == To, but neither comparison can decide anything: boardpolicy.New
// refuses a declaration whose transition targets itself, the only decode path
// funnels through New, and a zero-value Policy has no edges — so Allows(x, x)
// is false for every policy that can exist, and the Allows clause has already
// rejected the move before the comparison is read. Deleting both comparisons
// leaves every existing same-zone test green, which is how this was found.
//
// So the invariant is pinned where it actually holds. A future change that
// relaxes the constructor — to let a board declare a self-edge for status sync,
// the behaviour the upstream tool has — would silently reopen the same-zone
// path, and the two comparisons left behind are too weak to notice it: they
// never ran in the first place.
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

// Three layers refuse a superseded card the ordinary archive path: admission,
// record validation, and the completion grant. Admission and the completion
// grant each have a test; record validation has none, so nothing notices if it
// is removed. It is the layer that matters when the other two are bypassed —
// a journal published by an older binary, or a record replayed during recovery,
// reaches validation without passing admission again.
//
// The guard is scoped to Operation "archive" on purpose: supersede stamps the
// same status itself a few lines later. Pinning both halves keeps a future
// simplification from collapsing them into one unconditional check, which would
// make superseding impossible rather than exclusive.
func TestArchiveRecordRefusesSupersededCardUnderOrdinaryOperation(t *testing.T) {
	t.Parallel()
	_, pending, _, raw := archiveJournalFixture(t)
	// The fixture card declares no status, so the stamp is an insertion rather
	// than a replacement. Either way the equality check keeps fixture drift from
	// turning this into a test of an unstamped card.
	superseded := bytes.Replace(raw, []byte("title: archived\n"), []byte("title: archived\nstatus: superseded\n"), 1)
	if bytes.Equal(superseded, raw) {
		t.Fatal("fixture card no longer carries the line the stamp anchors to, so this test proves nothing")
	}
	rec := pending
	rec.Original, rec.OriginalSHA256 = superseded, bytesDigest(superseded)
	rec.Patched, rec.FinalSHA256 = superseded, bytesDigest(superseded)
	// The completion binding carries its own hash of the final card. Rebinding
	// it is what makes this record consistent everywhere except the status, so
	// the guard under test is the only thing left that can refuse it.
	binding := *pending.Completion
	binding.FinalSHA256 = bytesDigest(superseded)
	rec.Completion = &binding
	err := validateArchiveRecord(rec)
	if err == nil {
		t.Fatal("superseded card accepted as an ordinary archive record")
	}
	if !strings.Contains(err.Error(), "separate archive operation") {
		t.Fatalf("error = %v, want one naming the separate operation rather than a hash or identity failure", err)
	}
	// The same record under the operation that owns this status must still be
	// accepted, or the guard above would be refusing the wrong thing.
	rec.Operation, rec.Completion = "supersede", nil
	if err := validateArchiveRecord(rec); err != nil {
		t.Fatalf("supersede record rejected for a card already stamped superseded: %v", err)
	}
}
