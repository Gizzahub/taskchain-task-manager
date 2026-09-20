package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cloneRejoinExportWithSiblingReservation makes the export say something this
// board's own ledger cannot: the sibling worktree reserves an ID through the
// common directory, so that reservation exists only in the common state.  That
// gap is the whole reason the export is an accepted form of evidence.
func cloneRejoinExportWithSiblingReservation(t *testing.T, fx ownerRejoinCloneFixture) ([]byte, string) {
	t.Helper()
	if _, err := EnableShared(fx.sibling, false); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(fx.sibling, CreateRequest{Title: "sibling reservation"})
	if err != nil {
		t.Fatal(err)
	}
	export, err := os.ReadFile(filepath.Join(sourceCommonNamespaceDir(t, fx.source), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	return export, entry.Card.ID
}

func cloneRejoinPrepareOptions(fx ownerRejoinCloneFixture) RejoinBoardOptions {
	return RejoinBoardOptions{
		Dir: fx.target, SourceOwner: fx.source, Clone: true,
		RejoinID:        strings.Repeat("d", 32),
		TargetNamespace: strings.Repeat("a", 32), SourceFencedNonempty: true,
	}
}

// TestRejoinBoardPrepareAcceptsASourceExportAsEvidence covers the case the CLI
// usage advertises: an export alone is sufficient evidence.  Refusing it while
// naming the export as the remedy would tell the operator to do the thing they
// had already done.
func TestRejoinBoardPrepareAcceptsASourceExportAsEvidence(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	export, reserved := cloneRejoinExportWithSiblingReservation(t, fx)
	opts := cloneRejoinPrepareOptions(fx)
	opts.SourceExport = export
	result, _, _, _, err := PrepareRejoinBoard(opts)
	if err != nil {
		t.Fatalf("an export-only prepare was refused: %v", err)
	}
	if len(result.ReservationFloors) == 0 && len(result.AdditionalReservedIDs) == 0 {
		t.Fatal("prepare recorded no reservation evidence from the export")
	}
	if !containsOwnerRejoinID(result.AdditionalReservedIDs, reserved) {
		t.Fatalf("the sibling's common-only reservation %q is missing from %v", reserved, result.AdditionalReservedIDs)
	}
}

// TestRejoinBoardPrepareKeepsExportEvidenceAlongsideOperatorFlags is the worse
// half of the same defect.  With a floor supplied, prepare used to succeed
// while quietly dropping the export's reservations, so the clone would reissue
// IDs the source's other worktrees already hold -- a silent collision rather
// than a refusal.
func TestRejoinBoardPrepareKeepsExportEvidenceAlongsideOperatorFlags(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	export, reserved := cloneRejoinExportWithSiblingReservation(t, fx)
	opts := cloneRejoinPrepareOptions(fx)
	opts.SourceExport = export
	opts.ReservationFloors = []ReservationFloor{{Prefix: "TASK", Through: 2}}
	result, _, _, _, err := PrepareRejoinBoard(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !containsOwnerRejoinID(result.AdditionalReservedIDs, reserved) {
		t.Fatalf("an operator floor suppressed the export's reservation %q: %v", reserved, result.AdditionalReservedIDs)
	}
	// Merging must be monotonic in the operator's favour too: a floor the
	// operator asserts is never lowered by evidence that knows less.
	for _, floor := range result.ReservationFloors {
		if floor.Prefix == "TASK" && floor.Through < 2 {
			t.Fatalf("the operator's floor was lowered to %d", floor.Through)
		}
	}
}

// TestRejoinBoardPrepareRefusesACloneWithNoEvidenceAtAll keeps the refusal that
// the wiring above must not weaken.
func TestRejoinBoardPrepareRefusesACloneWithNoEvidenceAtAll(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	if _, _, _, _, err := PrepareRejoinBoard(cloneRejoinPrepareOptions(fx)); err == nil {
		t.Fatal("a clone bootstrap with neither an export nor explicit evidence was accepted")
	}
}

func containsOwnerRejoinID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
