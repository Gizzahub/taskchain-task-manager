package taskstore

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func ownerRejoinFixture(t *testing.T) (OwnerRejoinPlan, []OwnerRejoinFileBytes) {
	t.Helper()
	original, target := []byte(`{"schemaVersion":1}`), []byte(`{"schemaVersion":2}`)
	p := OwnerRejoinPlan{SchemaVersion: 1, RejoinID: strings.Repeat("a", 32), SourceOwner: "/source", TargetOwner: "/target", BoardPath: "tasks", SourceHEAD: strings.Repeat("b", 40), SourceRefsSHA256: strings.Repeat("c", 64), SourceInventorySHA256: strings.Repeat("d", 64), SourceNamespace: strings.Repeat("e", 32), TargetNamespace: strings.Repeat("f", 32), SourceFencedNonempty: true, SourceStorageProtocol: 5, TargetStorageProtocol: 6, ReservationFloors: []ReservationFloor{{Prefix: "TASK", Through: 9}}, AdditionalReservedIDs: []string{"TASK-9"}, Files: []OwnerRejoinFile{{Role: "ledger", Path: ".task-manager-ids.json", OriginalPresent: true, TargetPresent: true, Mode: 0600, OriginalLength: len(original), OriginalSHA256: bytesDigest(original), TargetLength: len(target), TargetSHA256: bytesDigest(target)}}, ArchiveCards: []ArchiveNamespaceCardBinding{}}
	bound, raw, err := BindOwnerRejoinPayload(p, []OwnerRejoinFileBytes{{Role: "ledger", Original: original, Target: target}})
	if err != nil {
		t.Fatal(err)
	}
	if bytesDigest(raw) != bound.PayloadSHA256 {
		t.Fatal("payload not bound")
	}
	return bound, []OwnerRejoinFileBytes{{Role: "ledger", Original: original, Target: target}}
}

func TestOwnerRejoinStrictCodecs(t *testing.T) {
	p, files := ownerRejoinFixture(t)
	praw, err := OwnerRejoinPlanBytes(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOwnerRejoinPlan(praw); err != nil {
		t.Fatal(err)
	}
	payload, err := OwnerRejoinPayloadBytes(p, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOwnerRejoinPayload(payload, p); err != nil {
		t.Fatal(err)
	}
	r := OwnerRejoinReceipt{SchemaVersion: 1, Phase: "completed", RejoinID: p.RejoinID, PlanSHA256: bytesDigest(praw), PayloadSHA256: p.PayloadSHA256, SourceAggregateSHA256: ownerRejoinAggregate(p.Files, false), TargetAggregateSHA256: ownerRejoinAggregate(p.Files, true)}
	raw, err := OwnerRejoinReceiptBytes(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOwnerRejoinReceipt(raw, p); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeOwnerRejoinReceipt(append([]byte(" "), raw...), p); err == nil {
		t.Fatal("non-canonical receipt accepted")
	}
	badSame := p
	badSame.SourceCommonAvailable = true
	if _, err := OwnerRejoinPlanBytes(badSame); err == nil {
		t.Fatal("same-common plan accepted a new namespace")
	}
	badClone := p
	badClone.ReservationFloors, badClone.AdditionalReservedIDs = nil, nil
	if _, err := OwnerRejoinPlanBytes(badClone); err == nil {
		t.Fatal("independent clone accepted without reservation evidence")
	}
	badPolicy := p
	badPolicy.TargetPolicyAuthority, badPolicy.TargetPolicySHA256 = strings.Repeat("1", 32), strings.Repeat("2", 64)
	if _, err := OwnerRejoinPlanBytes(badPolicy); err == nil {
		t.Fatal("owner rejoin invented a target policy")
	}
	for name, mutate := range map[string]func([]byte) []byte{
		"trailing": func(b []byte) []byte { return append(b, 'x') },
		"unknown": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"x":1`), 1)
		},
		"duplicate": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1)
		},
		"version": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":2`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeOwnerRejoinPlan(mutate(praw)); err == nil {
				t.Fatal("invalid plan accepted")
			}
		})
	}
	payload[len(payload)-1] ^= 1
	if _, err := DecodeOwnerRejoinPayload(payload, p); err == nil {
		t.Fatal("mutated payload accepted")
	}
}

func TestReservationFloorAndProtocolSixBarrier(t *testing.T) {
	ledger := idLedger{SchemaVersion: 4, Namespace: strings.Repeat("a", 32), Reserved: []string{"TASK-2"}, ReservationFloors: []ReservationFloor{{Prefix: "TASK", Through: 9}}}
	if got, err := allocateID(ledger, "", "TASK"); err != nil || got != "TASK-10" {
		t.Fatalf("floor allocation=%q %v", got, err)
	}
	if _, err := allocateID(ledger, "TASK-9", "TASK"); err == nil {
		t.Fatal("requested ID at reservation floor accepted")
	}
	if got, err := unionReservationFloors(ledger.ReservationFloors, []ReservationFloor{{Prefix: "TASK", Through: 3}, {Prefix: "PLAN", Through: 2}}); err != nil || len(got) != 2 || got[1].Through != 9 {
		t.Fatalf("floor union=%v %v", got, err)
	}
	if _, err := decodeIDs([]byte(`{"schemaVersion":1,"reserved":["TASK-1"]}`)); err != nil {
		t.Fatalf("legacy ledger rejected: %v", err)
	}
	if _, err := decodeIDs([]byte(`{"schemaVersion":1,"reserved":["TASK-1"],"extra":true}`)); err == nil {
		t.Fatal("legacy ledger with unknown field accepted")
	}
	if _, err := decodeIDs([]byte(`{"schemaVersion":4,"reserved":["TASK-10"],"namespace":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","reservationFloors":[{"prefix":"TASK","through":9}]}`)); err != nil {
		t.Fatalf("owner-rejoin ledger rejected: %v", err)
	}
	common := validSharedFixture()
	common.SchemaVersion = 4
	common.StorageProtocol = 6
	common.ReservationFloors = []ReservationFloor{{Prefix: "TASK", Through: 9}}
	common.CompletedOwnerRejoins = []sharedOwnerRejoinDone{{RejoinID: strings.Repeat("b", 32), Owner: "/owner", PlanSHA256: strings.Repeat("c", 64), PayloadSHA256: strings.Repeat("d", 64)}}
	commonRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer commonRoot.Close()
	if err := publishSharedState(commonRoot, common, true); err != nil {
		t.Fatalf("owner-rejoin common state rejected: %v", err)
	}
	if got, err := loadSharedState(commonRoot); err != nil || got.SchemaVersion != 4 || got.StorageProtocol != 6 {
		t.Fatalf("owner-rejoin common state round trip failed: %#v %v", got, err)
	}
	j, err := decodeTransitionJournal([]byte(`{"schemaVersion":1,"records":[],"storageProtocol":6}`))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := checkStorageGateWithoutArchive(root, j); err == nil {
		t.Fatal("protocol 6 ordinary runtime accepted")
	}
	if err := publishTransitionJournal(root, j); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTransitionsForStorage(root); err == nil {
		t.Fatal("protocol 6 storage bypass accepted")
	}
}

func TestOwnerRejoinPayloadAcceptsSchemaTwoScaleTarget(t *testing.T) {
	p, files := ownerRejoinFixture(t)
	target := bytes.Repeat([]byte("x"), (8<<20)+1)
	p.Files[0].TargetLength = len(target)
	p.Files[0].TargetSHA256 = bytesDigest(target)
	files[0].Target = target
	bound, payload, err := BindOwnerRejoinPayload(p, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) <= 8<<20 {
		t.Fatal("fixture did not cross legacy 8 MiB boundary")
	}
	decoded, err := DecodeOwnerRejoinPayload(payload, bound)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 1 || !bytes.Equal(decoded[0].Target, target) {
		t.Fatal("large target did not round trip exactly")
	}
}
