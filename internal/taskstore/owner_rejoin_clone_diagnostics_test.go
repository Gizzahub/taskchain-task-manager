package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRejoinBoardPrepareReadsTheSourceProtocolRatherThanAssumingIt covers the
// hidden default that used to sit in the plan header.  A protocol-5-era common
// state omits storageProtocol entirely, so prepare saw a zero and substituted
// 5 -- recording a protocol assertion the operator never made.  The value now
// comes off the source's transition journal, which records it explicitly.
func TestRejoinBoardPrepareReadsTheSourceProtocolRatherThanAssumingIt(t *testing.T) {
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	state := readOwnerRejoinCommonState(t, fx.source)
	if state.StorageProtocol == 0 {
		t.Fatal("the source records no protocol, so this asserts nothing about substitution")
	}
	export, _ := cloneRejoinExportWithSiblingReservation(t, fx)
	opts := cloneRejoinPrepareOptions(fx)
	opts.SourceExport = export
	result, _, _, _, err := PrepareRejoinBoard(opts)
	if err != nil {
		t.Fatal(err)
	}
	// The assertion is that prepare reports what the source recorded, not that
	// the number is 5.  A literal here would be the same substitution wearing
	// a different hat: it would still pass if the header stopped reading the
	// source at all.
	if result.SourceStorageProtocol != state.StorageProtocol {
		t.Fatalf("prepare recorded protocol %d, but the source common state records %d", result.SourceStorageProtocol, state.StorageProtocol)
	}
}

// TestCloneOwnerRejoinDistinguishesForeignCommonAuthority pins the three states
// that used to share one message naming no recovery action.  They are reached
// when a clone already carries common authority that is not the one this rejoin
// would have written, and the operator's next move differs in each case.
func TestCloneOwnerRejoinDistinguishesForeignCommonAuthority(t *testing.T) {
	// Only two of the three branches can be provoked through a decodable
	// common state.  validateSharedShape already refuses a schema 4 state whose
	// storageProtocol is not 6 (shared_ledger.go:442), so the protocol branch
	// is unreachable defensive code -- it is still split out and given its own
	// message, because a defensive branch that cannot say what it caught is
	// worth no more than the one it replaced.
	for _, row := range []struct {
		name  string
		edit  func(*sharedState)
		want  string
		avoid string
	}{
		{"foreign-namespace", func(s *sharedState) { s.NamespaceID = strings.Repeat("b", 32) }, "not the planned target namespace", "schema"},
		{"older-schema", func(s *sharedState) {
			// An earlier copy of the common state restored over the one this
			// rejoin wrote: schema 3, phase active, no pending rejoin.  That is
			// the shape the message's advice addresses.
			s.SchemaVersion, s.Phase, s.PendingOwnerRejoin = 3, "active", nil
			s.ReservationFloors, s.StorageProtocol = nil, 5
		}, "is schema 3, not the schema 4", "namespace"},
	} {
		t.Run(row.name, func(t *testing.T) {
			fx := newOwnerRejoinCloneFixture(t, true, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
			// Stopping at this cutpoint is what creates the clone's own common
			// authority; there is no other way to reach these branches, because
			// they only apply to a board that already has one.
			stop := errors.New("stop")
			if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, func(point string) error {
				if point == "after-owner-rejoin-common-pending" {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			path := filepath.Join(sourceCommonNamespaceDir(t, fx.target), sharedStateFile)
			state := readOwnerRejoinCommonState(t, fx.target)
			row.edit(&state)
			raw, err := sharedStateCanonicalBytes(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			err = applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil)
			if err == nil {
				t.Fatalf("%s was accepted", row.name)
			}
			if !strings.Contains(err.Error(), row.want) {
				t.Fatalf("%s: %v", row.name, err)
			}
			// The messages must actually be distinct, not three wordings that
			// all reduce to the same undiagnosable refusal.
			if strings.Contains(err.Error(), row.avoid) {
				t.Fatalf("%s names an unrelated cause too: %v", row.name, err)
			}
		})
	}
}
