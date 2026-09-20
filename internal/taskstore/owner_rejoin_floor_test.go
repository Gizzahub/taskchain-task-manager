package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestCloneOwnerRejoinFloorGovernsAllocation pins the point of a reservation
// floor: the clone was bootstrapped without the source's common directory, so
// the floor is the only thing standing between it and reissuing IDs the source
// already handed out.
func TestCloneOwnerRejoinFloorGovernsAllocation(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(fx.target, CreateRequest{Title: "above the floor"})
	if err != nil || entry.Card.ID != "TASK-10" {
		t.Fatalf("allocation ignored the floor: id=%q err=%v", entry.Card.ID, err)
	}
	if _, err := Create(fx.target, CreateRequest{ID: "TASK-3", Title: "under the floor"}); err == nil {
		t.Fatal("an ID covered by the reservation floor was issued")
	}
}

// TestCloneOwnerRejoinFloorSurvivesAStrippedLedger is the reason the floor is
// cross-checked against the common state rather than read from the local file
// alone.  Allocation reads floors off ids.json, so a board whose ids.json lost
// them would otherwise restart at TASK-1 and hand back IDs the source holds --
// a silent collision, with nothing refusing anything.
func TestCloneOwnerRejoinFloorSurvivesAStrippedLedger(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	stripOwnerRejoinLedgerFloors(t, fx.target)
	entry, err := Create(fx.target, CreateRequest{Title: "after the ledger lost its floor"})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Card.ID == "TASK-1" || entry.Card.ID != "TASK-10" {
		t.Fatalf("a stripped ledger reissued a held ID: %q", entry.Card.ID)
	}
	if _, err := Create(fx.target, CreateRequest{ID: "TASK-4", Title: "under the floor"}); err == nil {
		t.Fatal("a stripped ledger issued an ID covered by the floor")
	}
	// The repair must be durable, not merely applied in memory for one call.
	ledger := readOwnerRejoinLedger(t, fx.target)
	if ledger.SchemaVersion != 4 || len(ledger.ReservationFloors) != 1 || ledger.ReservationFloors[0].Through != 9 {
		t.Fatalf("floor was not restored to the ledger: %+v", ledger)
	}
}

// TestCloneOwnerRejoinFloorIsPublishedToCommonState covers the other direction:
// a floor is only durable once the common directory carries it, because that
// copy is what survives the local file being lost or rewritten.
func TestCloneOwnerRejoinFloorIsPublishedToCommonState(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(fx.target, CreateRequest{Title: "publish a floor"}); err != nil {
		t.Fatal(err)
	}
	state := readOwnerRejoinCommonState(t, fx.target)
	if state.SchemaVersion != 4 || len(state.ReservationFloors) != 1 || state.ReservationFloors[0].Through != 9 {
		t.Fatalf("common state does not carry the floor: %+v", state.ReservationFloors)
	}
}

func stripOwnerRejoinLedgerFloors(t *testing.T, dir string) {
	t.Helper()
	ledger := readOwnerRejoinLedger(t, dir)
	ledger.SchemaVersion, ledger.ReservationFloors = 3, nil
	raw, err := idLedgerBytes(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, idsFile), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeIDs(raw); err != nil {
		t.Fatalf("the stripped ledger is not itself a valid ledger: %v", err)
	}
}

func readOwnerRejoinLedger(t *testing.T, dir string) idLedger {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, idsFile))
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := decodeIDs(raw)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}

func readOwnerRejoinCommonState(t *testing.T, dir string) sharedState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sourceCommonNamespaceDir(t, dir), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	state, err := decodeSharedState(raw)
	if err != nil {
		t.Fatal(err)
	}
	return state
}

// TestCloneOwnerRejoinResumesFromHalfBuiltCommonAuthority pins the one step the
// clone transaction has and the same-common one does not: it mints its own
// common authority partway through, and the state it writes there is phase
// "initializing" with the rejoin still pending.  The cutpoint sweep kills at
// this boundary too, but a passing kill only shows the resume works; this shows
// what it resumes *from*, so a future change that lands "active" here -- a
// board claiming to be finished while the transaction is not -- fails loudly
// instead of quietly satisfying the sweep.
func TestCloneOwnerRejoinResumesFromHalfBuiltCommonAuthority(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	stop := errors.New("stop at the clone's common authority")
	err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, func(point string) error {
		if point == "after-owner-rejoin-common-pending" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("the clone never reached its common-authority cutpoint: %v", err)
	}
	state := readOwnerRejoinCommonState(t, fx.target)
	if state.Phase != "initializing" {
		t.Fatalf("interrupted clone authority is phase %q, not the half-built state the resume expects", state.Phase)
	}
	if state.NamespaceID != fx.plan.TargetNamespace {
		t.Fatalf("the clone minted namespace %q instead of its own %q", state.NamespaceID, fx.plan.TargetNamespace)
	}
	if state.PendingOwnerRejoin == nil {
		t.Fatal("the half-built authority carries no pending rejoin, so nothing marks it as unfinished")
	}
	// The interrupted board must not be readable as a board yet, and the resume
	// must carry it the rest of the way rather than starting over.
	if _, readErr := Ready(fx.target); readErr == nil {
		t.Fatal("a board with a half-built common authority was admitted for reading")
	}
	if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatalf("resume from the half-built authority: %v", err)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatalf("the resumed clone is not readable: %v", err)
	}
	if final := readOwnerRejoinCommonState(t, fx.target); final.Phase != "active" || final.PendingOwnerRejoin != nil {
		t.Fatalf("the resumed authority is phase %q with pending=%v", final.Phase, final.PendingOwnerRejoin != nil)
	}
}
