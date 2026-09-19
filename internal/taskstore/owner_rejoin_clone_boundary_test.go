package taskstore

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIndependentCloneOwnerRejoinHeaderBoundaries walks the header invariants
// one at a time.  Each row is a plan that differs from a good plan in exactly
// one field, so a row that stops failing names the invariant that was lost
// rather than merely reporting that something broke.
func TestIndependentCloneOwnerRejoinHeaderBoundaries(t *testing.T) {
	fx := newOwnerRejoinCloneFixture(t, true, []ReservationFloor{{Prefix: "TASK", Through: 64}}, []string{})
	sources := cloneBoundarySources(t, fx.source, true)
	for _, row := range []struct {
		name string
		edit func(*OwnerRejoinPlan)
	}{
		{"own-namespace", func(p *OwnerRejoinPlan) { p.TargetNamespace = p.SourceNamespace }},
		{"own-policy-authority", func(p *OwnerRejoinPlan) { p.TargetPolicyAuthority = p.SourcePolicyAuthority }},
		{"policy-presence", func(p *OwnerRejoinPlan) { p.TargetPolicyAuthority = "" }},
		{"policy-digest", func(p *OwnerRejoinPlan) { p.TargetPolicySHA256 = strings.Repeat("c", 64) }},
		{"source-common-available", func(p *OwnerRejoinPlan) { p.SourceCommonAvailable = true }},
		{"unfenced-source", func(p *OwnerRejoinPlan) { p.SourceFencedNonempty = false }},
		{"source-protocol", func(p *OwnerRejoinPlan) { p.SourceStorageProtocol = 4 }},
		{"target-protocol", func(p *OwnerRejoinPlan) { p.TargetStorageProtocol = 5 }},
		{"same-owner", func(p *OwnerRejoinPlan) { p.TargetOwner = p.SourceOwner }},
		{"no-reservation-evidence", func(p *OwnerRejoinPlan) {
			p.ReservationFloors, p.AdditionalReservedIDs = []ReservationFloor{}, []string{}
		}},
		{"unsorted-reserved-ids", func(p *OwnerRejoinPlan) {
			p.AdditionalReservedIDs = []string{"TASK-9", "TASK-2"}
		}},
		{"zero-floor-bound", func(p *OwnerRejoinPlan) {
			p.ReservationFloors = []ReservationFloor{{Prefix: "TASK", Through: 0}}
		}},
		{"unsorted-floors", func(p *OwnerRejoinPlan) {
			p.ReservationFloors = []ReservationFloor{{Prefix: "TASK", Through: 2}, {Prefix: "BUG", Through: 2}}
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			p := fx.plan
			p.Files = nil
			p.PayloadSHA256 = ""
			row.edit(&p)
			if _, _, _, err := PrepareIndependentCloneOwnerRejoin(p, sources); err == nil {
				t.Fatalf("clone rejoin accepted a plan with %s", row.name)
			}
		})
	}
}

// TestIndependentCloneOwnerRejoinRefusesMalformedPayloadWithoutMutation drives
// the malformed frames through the apply path rather than the codec, because
// the contract being checked is not only that the bytes are rejected but that
// the board they were aimed at is left exactly as it was.
func TestIndependentCloneOwnerRejoinRefusesMalformedPayloadWithoutMutation(t *testing.T) {
	for _, row := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"empty", func([]byte) []byte { return nil }},
		{"truncated-header", func(p []byte) []byte { return p[:4] }},
		{"truncated-body", func(p []byte) []byte { return p[:len(p)-1] }},
		{"wrong-magic", func(p []byte) []byte {
			out := append([]byte(nil), p...)
			out[2] = 'X'
			return out
		}},
		{"trailing-byte", func(p []byte) []byte { return append(append([]byte(nil), p...), 0) }},
		{"flipped-body-byte", func(p []byte) []byte {
			out := append([]byte(nil), p...)
			out[len(out)/2] ^= 0xff
			return out
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 64}}, []string{})
			before := boardBytes(t, fx.target)
			if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, row.edit(fx.payload), nil); err == nil {
				t.Fatalf("clone rejoin accepted a %s payload", row.name)
			}
			if !reflectEqualBoard(before, boardBytes(t, fx.target)) {
				t.Fatalf("a refused %s payload still changed the board", row.name)
			}
		})
	}
}

// TestCloneOwnerRejoinCapacityPayloadCrossesLegacyEightMiBBoundary is the clone
// counterpart of the same-common scale test.  It matters separately because a
// clone rebinds the journal into a namespace of its own, so it exercises the
// namespace-changing transform rather than the namespace-preserving one, and
// the legacy 8 MiB repairs ceiling must not reappear on that path.
func TestCloneOwnerRejoinCapacityPayloadCrossesLegacyEightMiBBoundary(t *testing.T) {
	journal, base, _, _ := archiveJournalFixture(t)
	sourceNS, targetNS := strings.Repeat("a", 32), strings.Repeat("b", 32)
	journal.SchemaVersion, journal.BoardPath, journal.Namespace = 2, ownerRejoinTransformSource, sourceNS
	journal.Records = make([]archiveRecord, 1000)
	for i := range journal.Records {
		record := base
		record.State, record.Operation, record.Assertion = "completed", "force", strings.Repeat("<", 4096)
		record.Original, record.Patched, record.Completion = nil, nil, nil
		record.ID, record.RequestID = fmt.Sprintf("TASK-%d", i+1), fmt.Sprintf("%032x", i+1)
		record.Source, record.Target = "done/"+record.ID+".md", "_archive/done/"+record.ID+".md"
		record.BoardPath, record.Namespace = ownerRejoinTransformSource, sourceNS
		journal.Records[i] = record
	}
	original, err := archiveCapacityJournalBytes(journal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := transformOwnerRejoinArchiveJournal(original, ownerRejoinTransformSource, ownerRejoinTransformTarget, sourceNS, targetNS)
	if err != nil {
		t.Fatal(err)
	}
	if len(target) <= 8<<20 {
		t.Fatal("rebound clone target did not cross the legacy 8 MiB boundary")
	}
	payload, err := ownerRejoinCapacityPayloadBytes(original, target, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, got, _, err := decodeOwnerRejoinCapacityPayload(payload, bytesDigest(payload), ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil || !bytes.Equal(got, target) {
		t.Fatalf("large rebound clone target rejected: %v", err)
	}
	// The rebinding is what the clone is entitled to, and it must have actually
	// happened rather than the size check passing on unchanged bytes.
	decoded, err := decodeArchiveCapacityJournal(got)
	if err != nil || decoded.Namespace != targetNS {
		t.Fatalf("clone target namespace=%q err=%v", decoded.Namespace, err)
	}
}

func cloneBoundarySources(t *testing.T, source string, policy bool) []OwnerRejoinSourceFile {
	t.Helper()
	roles := sameCommonOwnerRejoinRoles(policy)
	sources := make([]OwnerRejoinSourceFile, 0, len(roles))
	for _, role := range roles {
		path := filepath.Join(source, sameCommonOwnerRejoinPaths[role])
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, OwnerRejoinSourceFile{Role: role, Mode: uint32(info.Mode().Perm()), Raw: raw})
	}
	return sources
}
