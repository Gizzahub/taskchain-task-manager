package taskstore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func sameCommonPreparationFixture(t *testing.T, withPolicy bool) (OwnerRejoinPlan, []OwnerRejoinSourceFile) {
	t.Helper()
	namespace := strings.Repeat("e", 32)
	sourceBoard := "/source/tasks"
	p := OwnerRejoinPlan{SchemaVersion: 1, RejoinID: strings.Repeat("a", 32), SourceOwner: sourceBoard, TargetOwner: "/target/tasks", BoardPath: "tasks", SourceHEAD: strings.Repeat("b", 40), SourceRefsSHA256: strings.Repeat("c", 64), SourceInventorySHA256: strings.Repeat("d", 64), SourceNamespace: namespace, TargetNamespace: namespace, SourceCommonAvailable: true, SourceFencedNonempty: true, SourceStorageProtocol: 5, TargetStorageProtocol: 6, ReservationFloors: []ReservationFloor{{Prefix: "TASK", Through: 9}}, AdditionalReservedIDs: []string{"TASK-12"}}

	transition := transitionJournal{SchemaVersion: 1, Records: []transitionRecord{}, StorageProtocol: 5}
	ids := idLedger{SchemaVersion: 3, Reserved: []string{"TASK-1"}, Namespace: namespace}
	repair := repairJournalFixture(t)
	repair.BoardPath, repair.Records[0].BoardPath = sourceBoard, sourceBoard
	repair.Records[0].Kind, repair.Records[0].Original, repair.Records[0].Patched = "completed", nil, nil
	relocation := completedRelocationJournal(relocationJournalFixture(t, sourceBoard))
	archive, _, completion, _ := archiveJournalFixture(t)
	archive.SchemaVersion, archive.BoardPath = 2, sourceBoard
	archive.Records[0].State, archive.Records[0].BoardPath = "completed", sourceBoard
	archive.Records[0].Original, archive.Records[0].Patched = nil, nil
	completion.BoardPath = sourceBoard
	archive.Records[0].Completion = &completion

	transitionRaw, _ := marshalOwnerRejoinJSON(transition, true)
	idsRaw, _ := marshalOwnerRejoinJSON(ids, false)
	repairRaw, _ := repairJournalBytes(repair)
	relocationRaw, _ := relocationJournalBytes(relocation)
	archiveRaw, _ := archiveCapacityJournalBytes(archive)
	capacity := archiveCapacityAdoption{SchemaVersion: 1, Phase: "completed", UpgradeID: strings.Repeat("f", 32), BoardPath: sourceBoard, Namespace: archive.Namespace, SourceJournalSchema: 1, TargetJournalSchema: 2, StorageProtocol: 5, JournalMode: 0640, OriginalLength: 1, OriginalSHA256: strings.Repeat("1", 64), TargetLength: len(archiveRaw), TargetSHA256: bytesDigest(archiveRaw), PayloadSHA256: strings.Repeat("2", 64)}
	capacityRaw, _ := archiveCapacityAdoptionBytes(capacity)
	sources := []OwnerRejoinSourceFile{{"archive", 0640, archiveRaw}, {"archive-capacity", 0600, capacityRaw}, {"ids", 0600, idsRaw}, {"relocations", 0600, relocationRaw}, {"repairs", 0600, repairRaw}, {"transitions", 0600, transitionRaw}}
	if withPolicy {
		policy, _ := boardpolicy.Default().Canonical()
		p.SourcePolicyAuthority, p.TargetPolicyAuthority = strings.Repeat("3", 32), strings.Repeat("3", 32)
		p.SourcePolicySHA256, p.TargetPolicySHA256 = bytesDigest(policy), bytesDigest(policy)
		transition.SchemaVersion, transition.PolicyDigest = 3, p.SourcePolicySHA256
		transition.PolicyAuthority = &policyAuthorityBinding{AuthorityID: p.SourcePolicyAuthority, Scope: "shared", Namespace: namespace}
		transitionRaw, _ = marshalOwnerRejoinJSON(transition, true)
		idsDigest := bytesDigest(idsRaw)
		activation := policyActivationState{SchemaVersion: 1, Phase: "completed", AuthorityID: p.SourcePolicyAuthority, Scope: "shared", Namespace: namespace, Canonical: policy, Digest: p.SourcePolicySHA256, Plan: policyActivationPlan{Root: sourceBoard, HEAD: strings.Repeat("4", 40), Snapshot: strings.Repeat("5", 64), TargetJournal: strings.Repeat("6", 64), OriginalIDs: idsDigest, TargetIDs: idsDigest, IDTarget: []byte{}}}
		activationRaw, err := policyActivationBytes(activation)
		if err != nil {
			t.Fatal(err)
		}
		sources = []OwnerRejoinSourceFile{{"archive", 0640, archiveRaw}, {"archive-capacity", 0600, capacityRaw}, {"ids", 0600, idsRaw}, {"policy", 0600, policy}, {"policy-activation", 0600, activationRaw}, {"relocations", 0600, relocationRaw}, {"repairs", 0600, repairRaw}, {"transitions", 0600, transitionRaw}}
	}
	return p, sources
}

func TestPrepareSameCommonOwnerRejoinPolicyStates(t *testing.T) {
	t.Parallel()
	for _, withPolicy := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[withPolicy], func(t *testing.T) {
			base, sources := sameCommonPreparationFixture(t, withPolicy)
			plan, outer, capacity, err := PrepareSameCommonOwnerRejoin(base, sources)
			if err != nil {
				t.Fatal(err)
			}
			files, err := DecodeOwnerRejoinPayload(outer, plan)
			if err != nil || validateSameCommonOwnerRejoinPlan(plan, files) != nil {
				t.Fatalf("prepared plan rejected: %v", err)
			}
			if len(plan.Artifacts) != 1 || plan.Artifacts[0].SHA256 != bytesDigest(capacity) || plan.Artifacts[0].Length != len(capacity) {
				t.Fatal("capacity artifact was not separately inventoried")
			}
			for _, file := range files {
				if file.Role == "archive-capacity-payload" {
					t.Fatal("capacity artifact was inlined in owner payload")
				}
				if file.Role == "policy-activation" {
					var before, after policyActivationState
					if err := decodeExact(file.Original, &before); err != nil {
						t.Fatal(err)
					}
					if err := decodeExact(file.Target, &after); err != nil {
						t.Fatal(err)
					}
					if before.Plan.Root != base.SourceOwner || after.Plan.Root != base.TargetOwner {
						t.Fatal("policy activation root was not rebound to the returning board")
					}
					before.Plan.Root = after.Plan.Root
					rebound, err := policyActivationBytes(before)
					if err != nil || !bytes.Equal(rebound, file.Target) {
						t.Fatal("policy activation changed beyond its board identity")
					}
				}
			}
		})
	}
}

func TestSameCommonOwnerRejoinRejectsInventoryAndTargetTampering(t *testing.T) {
	t.Parallel()
	base, sources := sameCommonPreparationFixture(t, false)
	plan, outer, _, err := PrepareSameCommonOwnerRejoin(base, sources)
	if err != nil {
		t.Fatal(err)
	}
	files, _ := DecodeOwnerRejoinPayload(outer, plan)
	mutated := append([]OwnerRejoinFileBytes(nil), files...)
	mutated[0].Target = append(append([]byte(nil), mutated[0].Target...), ' ')
	if err := validateSameCommonOwnerRejoinPlan(plan, mutated); err == nil {
		t.Fatal("arbitrary target accepted")
	}
	omitted := plan
	omitted.Files = omitted.Files[:len(omitted.Files)-1]
	if err := validateSameCommonOwnerRejoinPlan(omitted, files[:len(files)-1]); err == nil {
		t.Fatal("omitted canonical role accepted")
	}
	duplicate := plan
	duplicate.Files = append([]OwnerRejoinFile(nil), plan.Files...)
	duplicate.Files[1] = duplicate.Files[0]
	if err := validateSameCommonOwnerRejoinPlan(duplicate, files); err == nil {
		t.Fatal("duplicate role accepted")
	}
	pending := append([]OwnerRejoinSourceFile(nil), sources...)
	journal, _ := decodeRepairJournal(pending[4].Raw)
	journal.Records[0].Kind = "pending"
	journal.Records[0].Original = []byte("invalid")
	pending[4].Raw, _ = repairJournalBytes(journal)
	if _, _, _, err := PrepareSameCommonOwnerRejoin(base, pending); err == nil {
		t.Fatal("pending source accepted")
	}
	foreign := base
	foreign.SourceOwner = "/foreign"
	if _, _, _, err := PrepareSameCommonOwnerRejoin(foreign, sources); err == nil {
		t.Fatal("foreign source scope accepted")
	}
}

func TestOwnerRejoinArtifactInventoryMaximum(t *testing.T) {
	t.Parallel()
	p, _ := ownerRejoinFixture(t)
	p.Artifacts = []OwnerRejoinArtifact{{Role: "archive-capacity-payload", Path: ownerRejoinCapacityArtifactPath(strings.Repeat("a", 64)), Mode: 0600, Length: maxOwnerRejoinCapacityArtifactBytes, SHA256: strings.Repeat("a", 64)}}
	if err := validateOwnerRejoinPlan(p, true); err != nil {
		t.Fatalf("exact artifact maximum rejected: %v", err)
	}
	p.Artifacts[0].Length++
	if err := validateOwnerRejoinPlan(p, true); err == nil {
		t.Fatal("artifact above maximum accepted")
	}
}
