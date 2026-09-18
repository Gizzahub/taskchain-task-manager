package taskstore

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const (
	ownerRejoinTransformSource = "/synthetic/tasks"
	ownerRejoinTransformTarget = "/synthetic/target"
)

func TestOwnerRejoinTransformsOnlyCompletedBoardBindings(t *testing.T) {
	repair := repairJournalFixture(t)
	repair.BoardPath, repair.Records[0].BoardPath = ownerRejoinTransformSource, ownerRejoinTransformSource
	repair.Records[0].Kind, repair.Records[0].Original, repair.Records[0].Patched = "completed", nil, nil
	repairRaw, _ := repairJournalBytes(repair)
	repairTarget, err := TransformOwnerRejoinRepairJournal(repairRaw, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	repairGot, _ := decodeRepairJournal(repairTarget)
	if repairGot.BoardPath != ownerRejoinTransformTarget || repairGot.Records[0].BoardPath != ownerRejoinTransformTarget {
		t.Fatal("repair board was not rewritten")
	}

	move := completedRelocationJournal(relocationJournalFixture(t, ownerRejoinTransformSource))
	moveRaw, _ := relocationJournalBytes(move)
	moveTarget, err := TransformOwnerRejoinRelocationJournal(moveRaw, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	moveGot, _ := decodeRelocationJournal(moveTarget)
	if got, want := moveGot.Records[0].Owner, move.Records[0].Owner; got != want || moveGot.Records[0].BoardPath != ownerRejoinTransformTarget {
		t.Fatalf("relocation changed evidence or missed board: %#v", moveGot.Records[0])
	}

	for _, schema := range []int{1, 2} {
		t.Run("archive-schema-"+string(rune('0'+schema)), func(t *testing.T) {
			archive, record, completion, _ := archiveJournalFixture(t)
			archive.SchemaVersion = schema
			archive.BoardPath, archive.Records[0].BoardPath, archive.Records[0].State = ownerRejoinTransformSource, ownerRejoinTransformSource, "completed"
			archive.Records[0].Original, archive.Records[0].Patched = nil, nil
			completion.BoardPath = ownerRejoinTransformSource
			archive.Records[0].Completion = &completion
			record = archive.Records[0]
			raw, err := archiveJournalWire(archive)
			if err != nil {
				t.Fatal(err)
			}
			target, err := TransformOwnerRejoinArchiveJournal(raw, ownerRejoinTransformSource, ownerRejoinTransformTarget)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeArchiveCapacityJournal(target)
			if err != nil || got.SchemaVersion != schema || got.Namespace != archive.Namespace || got.Records[0].Completion.BoardPath != ownerRejoinTransformTarget {
				t.Fatalf("archive transform=%#v err=%v", got, err)
			}
			want := record
			want.BoardPath, want.Completion.BoardPath = ownerRejoinTransformTarget, ownerRejoinTransformTarget
			if !reflect.DeepEqual(got.Records[0], want) {
				t.Fatal("archive transformation changed immutable record evidence")
			}
		})
	}
}

func TestOwnerRejoinTransformsRejectPendingAndForeign(t *testing.T) {
	repair := repairJournalFixture(t)
	repair.BoardPath, repair.Records[0].BoardPath = ownerRejoinTransformSource, ownerRejoinTransformSource
	raw, _ := repairJournalBytes(repair)
	if _, err := TransformOwnerRejoinRepairJournal(raw, ownerRejoinTransformSource, ownerRejoinTransformTarget); err == nil {
		t.Fatal("pending repair accepted")
	}
	move := relocationJournalFixture(t, ownerRejoinTransformSource)
	raw, _ = relocationJournalBytes(move)
	if _, err := TransformOwnerRejoinRelocationJournal(raw, ownerRejoinTransformSource, ownerRejoinTransformTarget); err == nil {
		t.Fatal("pending relocation accepted")
	}
	archive, _, _, _ := archiveJournalFixture(t)
	archive.BoardPath, archive.Records[0].BoardPath = ownerRejoinTransformSource, ownerRejoinTransformSource
	raw, _ = archiveJournalBytes(archive)
	if _, err := TransformOwnerRejoinArchiveJournal(raw, ownerRejoinTransformSource, ownerRejoinTransformTarget); err == nil {
		t.Fatal("pending archive accepted")
	}
	if _, err := TransformOwnerRejoinArchiveJournal(raw, "/foreign", ownerRejoinTransformTarget); err == nil {
		t.Fatal("foreign archive accepted")
	}
}

func TestOwnerRejoinCapacityPayloadAndReceiptCodecs(t *testing.T) {
	archive, _, _, _ := archiveJournalFixture(t)
	archive.SchemaVersion = 2
	archive.BoardPath, archive.Records[0].BoardPath, archive.Records[0].State = ownerRejoinTransformSource, ownerRejoinTransformSource, "completed"
	archive.Records[0].Original, archive.Records[0].Patched = nil, nil
	archive.Records[0].Completion.BoardPath = ownerRejoinTransformSource
	original, err := archiveCapacityJournalBytes(archive)
	if err != nil {
		t.Fatal(err)
	}
	target, err := TransformOwnerRejoinArchiveJournal(original, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := ownerRejoinCapacityPayloadBytes(original, target, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := decodeOwnerRejoinCapacityPayload(payload, bytesDigest(payload), ownerRejoinTransformSource, ownerRejoinTransformTarget); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append([]byte(nil), payload[:len(payload)-1]...), append([]byte("TCARCP\x00\x01"), payload[8:]...)} {
		if _, _, _, err := decodeOwnerRejoinCapacityPayload(bad, bytesDigest(bad), ownerRejoinTransformSource, ownerRejoinTransformTarget); err == nil {
			t.Fatal("mutated or old capacity payload accepted")
		}
	}
	third := append([]byte(nil), target...)
	third = append([]byte(" "), third...)
	if _, err := ownerRejoinCapacityPayloadBytes(original, third, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600); err == nil {
		t.Fatal("third capacity target accepted")
	}
	a := archiveCapacityAdoption{SchemaVersion: 1, Phase: "completed", UpgradeID: strings.Repeat("a", 32), BoardPath: ownerRejoinTransformSource, Namespace: archive.Namespace, SourceJournalSchema: 1, TargetJournalSchema: 2, StorageProtocol: 5, JournalMode: 0o600, OriginalLength: 1, OriginalSHA256: strings.Repeat("b", 64), TargetLength: len(original), TargetSHA256: bytesDigest(original), PayloadSHA256: strings.Repeat("d", 64)}
	receipt, _ := archiveCapacityAdoptionBytes(a)
	converted, separate, artifact, err := prepareOwnerRejoinArchiveCapacity(receipt, original, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := decodeOwnerRejoinArchiveCapacityAdoption(converted)
	if got.SchemaVersion != 2 || got.StorageProtocol != 6 || got.UpgradeID != a.UpgradeID || got.OriginalSHA256 != a.TargetSHA256 || got.TargetSHA256 != bytesDigest(target) || got.PayloadSHA256 != bytesDigest(separate) || got.Namespace != a.Namespace || artifact.SHA256 != got.PayloadSHA256 {
		t.Fatal("capacity target receipt binding changed")
	}
	a.Phase = "pending"
	pending, _ := archiveCapacityAdoptionBytes(a)
	if _, _, _, err := prepareOwnerRejoinArchiveCapacity(pending, original, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600); err == nil {
		t.Fatal("pending capacity receipt accepted")
	}
}

func TestOwnerRejoinTransitionAndIDTransforms(t *testing.T) {
	for schema := 1; schema <= 4; schema++ {
		journal := transitionJournal{SchemaVersion: schema, Records: []transitionRecord{}, StorageProtocol: 5}
		if schema >= 2 {
			journal.PolicyDigest = strings.Repeat("a", 64)
		}
		if schema >= 3 {
			journal.PolicyAuthority = &policyAuthorityBinding{AuthorityID: strings.Repeat("b", 32), Scope: "shared", Namespace: strings.Repeat("c", 32)}
		}
		if schema == 4 {
			policy, _ := boardpolicy.Default().Canonical()
			journal.PolicyDigest = bytesDigest(policy)
			journal.PolicyHistory = map[string][]byte{journal.PolicyDigest: policy}
		}
		raw, _ := marshalOwnerRejoinJSON(journal, true)
		target, err := TransformOwnerRejoinTransitions(raw)
		if err != nil {
			t.Fatalf("schema %d: %v", schema, err)
		}
		got, _ := decodeTransitionJournal(target)
		if got.StorageProtocol != 6 || got.SchemaVersion != schema || !reflect.DeepEqual(got.Records, journal.Records) {
			t.Fatalf("schema %d transition evidence changed", schema)
		}
	}
	namespace := strings.Repeat("b", 32)
	ledger := idLedger{SchemaVersion: 3, Namespace: namespace, Reserved: []string{"PLAN-2", "TASK-3"}}
	raw, _ := marshalOwnerRejoinJSON(ledger, false)
	target, err := TransformOwnerRejoinIDs(raw, namespace, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{"PLAN-4", "TASK-3"})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := decodeIDs(target)
	if got.SchemaVersion != 4 || got.Namespace != namespace || !reflect.DeepEqual(got.Reserved, []string{"PLAN-2", "PLAN-4", "TASK-3"}) || !reflect.DeepEqual(got.ReservationFloors, []ReservationFloor{{Prefix: "TASK", Through: 9}}) {
		t.Fatalf("ID transform=%+v", got)
	}
	emptyFloors, err := TransformOwnerRejoinIDs(raw, namespace, []ReservationFloor{}, []string{})
	if err != nil {
		t.Fatal(err)
	}
	emptyGot, _ := decodeIDs(emptyFloors)
	if emptyGot.ReservationFloors == nil || len(emptyGot.ReservationFloors) != 0 {
		t.Fatalf("empty floors=%+v", emptyGot.ReservationFloors)
	}
}

func TestOwnerRejoinTransformsEmptyCompletedJournals(t *testing.T) {
	repairRaw, err := repairJournalBytes(repairJournal{SchemaVersion: 1, BoardPath: ownerRejoinTransformSource, Records: []repairRecord{}})
	if err != nil {
		t.Fatal(err)
	}
	repairTarget, err := TransformOwnerRejoinRepairJournal(repairRaw, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	repairGot, err := decodeRepairJournal(repairTarget)
	if err != nil || repairGot.BoardPath != ownerRejoinTransformTarget || repairGot.Records == nil || len(repairGot.Records) != 0 {
		t.Fatalf("empty repair=%+v err=%v", repairGot, err)
	}
	moveRaw, err := relocationJournalBytes(relocationJournal{SchemaVersion: 1, BoardPath: ownerRejoinTransformSource, Records: []relocationRecord{}})
	if err != nil {
		t.Fatal(err)
	}
	moveTarget, err := TransformOwnerRejoinRelocationJournal(moveRaw, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	moveGot, err := decodeRelocationJournal(moveTarget)
	if err != nil || moveGot.BoardPath != ownerRejoinTransformTarget || moveGot.Records == nil || len(moveGot.Records) != 0 {
		t.Fatalf("empty relocation=%+v err=%v", moveGot, err)
	}
}

func TestOwnerRejoinCapacityPayloadAcceptsLargeSchemaTwoTarget(t *testing.T) {
	journal, base, _, _ := archiveJournalFixture(t)
	journal.SchemaVersion, journal.BoardPath = 2, ownerRejoinTransformSource
	journal.Records = make([]archiveRecord, 1000)
	for i := range journal.Records {
		record := base
		record.State, record.Operation, record.Assertion = "completed", "force", strings.Repeat("<", 4096)
		record.Original, record.Patched, record.Completion = nil, nil, nil
		record.ID, record.RequestID = fmt.Sprintf("TASK-%d", i+1), fmt.Sprintf("%032x", i+1)
		record.Source, record.Target = "done/"+record.ID+".md", "_archive/done/"+record.ID+".md"
		record.BoardPath = ownerRejoinTransformSource
		journal.Records[i] = record
	}
	original, err := archiveCapacityJournalBytes(journal)
	if err != nil {
		t.Fatal(err)
	}
	target, err := TransformOwnerRejoinArchiveJournal(original, ownerRejoinTransformSource, ownerRejoinTransformTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(target) <= 8<<20 {
		t.Fatal("schema2 target did not cross the legacy 8 MiB boundary")
	}
	payload, err := ownerRejoinCapacityPayloadBytes(original, target, ownerRejoinTransformSource, ownerRejoinTransformTarget, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, got, _, err := decodeOwnerRejoinCapacityPayload(payload, bytesDigest(payload), ownerRejoinTransformSource, ownerRejoinTransformTarget); err != nil || !reflect.DeepEqual(got, target) {
		t.Fatalf("large schema2 target rejected: %v", err)
	}
}
