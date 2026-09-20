package taskstore

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type ownerRejoinCloneFixture struct {
	plan     OwnerRejoinPlan
	payload  []byte
	capacity []byte
	source   string
	target   string
	sibling  string
	export   []byte
}

// newOwnerRejoinCloneFixture builds a genuinely independent clone: a separate
// repository with its own common directory, holding the same board.  Nothing of
// the source's common authority reaches it.
func newOwnerRejoinCloneFixture(t *testing.T, policy bool, floors []ReservationFloor, additional []string) ownerRejoinCloneFixture {
	t.Helper()
	repo, source, sibling := protocol5SharedFixture(t)
	var err error
	if source, err = filepath.EvalSymlinks(source); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableShared(source, false); err != nil {
		t.Fatal(err)
	}
	if policy {
		if _, err := ActivatePolicy(source, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err != nil {
			t.Fatal(err)
		}
	}
	cloneRoot := filepath.Join(t.TempDir(), "clone")
	sharedGit(t, t.TempDir(), "clone", "--no-local", repo, cloneRoot)
	sharedGit(t, cloneRoot, "config", "commit.gpgsign", "false")
	target := filepath.Join(cloneRoot, "tasks")
	if target, err = filepath.EvalSymlinks(target); err != nil {
		t.Fatal(err)
	}

	state := c5cSharedState(t, source)
	inventory, err := githistory.InspectWorktrees(context.Background(), repo, state.BoardPath)
	if err != nil {
		t.Fatal(err)
	}
	history, err := githistory.Scan(context.Background(), repo, state.BoardPath)
	if err != nil {
		t.Fatal(err)
	}
	refsRaw, _ := json.Marshal(history.Refs)
	worktreesRaw, _ := json.Marshal(inventory.Worktrees)
	head := ""
	for _, wt := range inventory.Worktrees {
		if filepath.Join(wt.Path, state.BoardPath) == source {
			head = wt.HEAD
		}
	}
	p := OwnerRejoinPlan{
		SchemaVersion: 1, RejoinID: strings.Repeat("7", 32), SourceOwner: source, TargetOwner: target,
		BoardPath: state.BoardPath, SourceHEAD: head, SourceRefsSHA256: bytesDigest(refsRaw), SourceInventorySHA256: bytesDigest(worktreesRaw),
		SourceNamespace: state.NamespaceID, TargetNamespace: strings.Repeat("a", 32), SourceCommonAvailable: false, SourceFencedNonempty: true,
		SourceStorageProtocol: 5, TargetStorageProtocol: 6, ReservationFloors: floors, AdditionalReservedIDs: additional,
	}
	if state.Policy != nil {
		p.SourcePolicyAuthority, p.SourcePolicySHA256 = state.Policy.AuthorityID, state.Policy.Digest
		p.TargetPolicyAuthority, p.TargetPolicySHA256 = strings.Repeat("b", 32), state.Policy.Digest
	}
	roles := sameCommonOwnerRejoinRoles(policy)
	sources := make([]OwnerRejoinSourceFile, 0, len(roles))
	for _, role := range roles {
		path := sameCommonOwnerRejoinPaths[role]
		raw, err := os.ReadFile(filepath.Join(source, path))
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(source, path))
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, OwnerRejoinSourceFile{Role: role, Mode: uint32(info.Mode().Perm()), Raw: raw})
		writeOwnerRejoinFixtureFile(t, filepath.Join(target, path), raw, info.Mode().Perm())
	}
	export, err := os.ReadFile(filepath.Join(sourceCommonNamespaceDir(t, source), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	plan, payload, capacity, err := PrepareIndependentCloneOwnerRejoin(p, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range plan.ArchiveCards {
		raw, err := os.ReadFile(filepath.Join(source, card.Path))
		if err != nil {
			t.Fatal(err)
		}
		writeOwnerRejoinFixtureFile(t, filepath.Join(target, card.Path), raw, os.FileMode(card.Mode))
	}
	mirrorOwnerRejoinCards(t, source, target)
	return ownerRejoinCloneFixture{plan: plan, payload: payload, capacity: capacity, source: source, target: target, sibling: sibling, export: export}
}

func sourceCommonNamespaceDir(t *testing.T, board string) string {
	t.Helper()
	location, err := githistory.LocateBoard(context.Background(), board)
	if err != nil || location == nil {
		t.Fatalf("locate board: %v", err)
	}
	return filepath.Join(location.CommonDirectory, "taskchain-task-manager", "ids", location.NamespaceKey)
}

func TestIndependentCloneOwnerRejoinBootstrapsItsOwnAuthority(t *testing.T) {
	t.Parallel()
	for _, policy := range []bool{false, true} {
		t.Run(map[bool]string{false: "policy-absent", true: "policy-bearing"}[policy], func(t *testing.T) {
			fx := newOwnerRejoinCloneFixture(t, policy, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
			if err := publishCloneCapacityArtifact(t, fx); err != nil {
				t.Fatal(err)
			}
			if err := applyOwnerRejoinIndependentClone(fx.target, fx.plan, fx.payload, nil); err != nil {
				t.Fatal(err)
			}
			state := c5cSharedState(t, fx.target)
			if state.NamespaceID != fx.plan.TargetNamespace || state.NamespaceID == fx.plan.SourceNamespace {
				t.Fatalf("clone did not mint its own namespace: %q", state.NamespaceID)
			}
			if state.StorageProtocol != 6 || state.SchemaVersion != 4 || state.Phase != "active" || state.PendingOwnerRejoin != nil {
				t.Fatalf("clone common state not completed: %+v", state)
			}
			if policy {
				if state.Policy == nil || state.Policy.AuthorityID != fx.plan.TargetPolicyAuthority || state.Policy.AuthorityID == fx.plan.SourcePolicyAuthority {
					t.Fatalf("clone did not mint its own policy authority: %+v", state.Policy)
				}
			} else if state.Policy != nil {
				t.Fatal("policy appeared in a policy-absent clone")
			}
			// The source board must be untouched: an independent clone rejoin
			// is not a distributed transaction and never writes to the source.
			sourceState := c5cSharedState(t, fx.source)
			if sourceState.NamespaceID != fx.plan.SourceNamespace || sourceState.StorageProtocol != 5 {
				t.Fatalf("source common authority was modified: %+v", sourceState)
			}
			if _, err := Ready(fx.target); err != nil {
				t.Fatalf("bootstrapped clone was not admitted by the ordinary runtime: %v", err)
			}
		})
	}
}

func publishCloneCapacityArtifact(t *testing.T, fx ownerRejoinCloneFixture) error {
	t.Helper()
	if len(fx.plan.Artifacts) != 1 {
		t.Fatal("clone plan lacks its capacity artifact")
	}
	writeOwnerRejoinFixtureFile(t, filepath.Join(fx.target, fx.plan.Artifacts[0].Path), fx.capacity, os.FileMode(fx.plan.Artifacts[0].Mode))
	return nil
}

func TestIndependentCloneOwnerRejoinRefusesWithoutReservationEvidence(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	stripped := fx.plan
	stripped.ReservationFloors, stripped.AdditionalReservedIDs = []ReservationFloor{}, []string{}
	if err := applyOwnerRejoinIndependentClone(fx.target, stripped, fx.payload, nil); err == nil {
		t.Fatal("clone bootstrap accepted without reservation evidence")
	}
}

func TestOwnerRejoinCloneReservationEvidenceFromSourceExport(t *testing.T) {
	t.Parallel()
	fx := newOwnerRejoinCloneFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 9}}, []string{})
	ledger, err := os.ReadFile(filepath.Join(fx.source, idsFile))
	if err != nil {
		t.Fatal(err)
	}
	// A board's own ledger cannot show what other worktrees reserved in the
	// common directory, and that gap is exactly what the export closes.  Before
	// the sibling reserves anything the export adds nothing, and the bootstrap
	// is refused rather than guessing a floor.
	if _, _, err := OwnerRejoinCloneReservationEvidence(fx.export, ledger, fx.plan.SourceNamespace); err == nil {
		t.Fatal("an export with no evidence beyond this board's ledger was accepted")
	}
	if _, err := EnableShared(fx.sibling, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(fx.sibling, CreateRequest{Title: "sibling reservation"}); err != nil {
		t.Fatal(err)
	}
	export, err := os.ReadFile(filepath.Join(sourceCommonNamespaceDir(t, fx.source), sharedStateFile))
	if err != nil {
		t.Fatal(err)
	}
	floors, additional, err := OwnerRejoinCloneReservationEvidence(export, ledger, fx.plan.SourceNamespace)
	if err != nil {
		t.Fatalf("source export yielded no evidence: %v", err)
	}
	if len(floors) == 0 && len(additional) == 0 {
		t.Fatal("source export produced empty evidence")
	}
	if _, _, err := OwnerRejoinCloneReservationEvidence(export, ledger, strings.Repeat("c", 32)); err == nil {
		t.Fatal("source export accepted for a foreign namespace")
	}
	if _, _, err := OwnerRejoinCloneReservationEvidence(append(export, ' '), ledger, fx.plan.SourceNamespace); err == nil {
		t.Fatal("tampered source export accepted")
	}
}
