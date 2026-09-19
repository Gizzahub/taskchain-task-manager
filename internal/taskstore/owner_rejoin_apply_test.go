package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type ownerRejoinApplyFixture struct {
	plan    OwnerRejoinPlan
	payload []byte
	source  string
	target  string
}

func newOwnerRejoinApplyFixture(t *testing.T, policy bool, floors []ReservationFloor) ownerRejoinApplyFixture {
	t.Helper()
	repo, source, _ := protocol5SharedFixture(t)
	var err error
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
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
	targetRoot := filepath.Join(t.TempDir(), "returning")
	sharedGit(t, repo, "worktree", "add", "--detach", targetRoot, "HEAD")
	target := filepath.Join(targetRoot, "tasks")
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
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
		SchemaVersion: 1, RejoinID: strings.Repeat("9", 32), SourceOwner: source, TargetOwner: target,
		BoardPath: state.BoardPath, SourceHEAD: head, SourceRefsSHA256: bytesDigest(refsRaw), SourceInventorySHA256: bytesDigest(worktreesRaw),
		SourceNamespace: state.NamespaceID, TargetNamespace: state.NamespaceID, SourceCommonAvailable: true, SourceFencedNonempty: true,
		SourceStorageProtocol: 5, TargetStorageProtocol: 6, ReservationFloors: floors, AdditionalReservedIDs: []string{},
	}
	if state.Policy != nil {
		p.SourcePolicyAuthority, p.TargetPolicyAuthority = state.Policy.AuthorityID, state.Policy.AuthorityID
		p.SourcePolicySHA256, p.TargetPolicySHA256 = state.Policy.Digest, state.Policy.Digest
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
	p, payload, _, err := PrepareSameCommonOwnerRejoin(p, sources)
	if err != nil {
		t.Fatal(err)
	}
	for _, card := range p.ArchiveCards {
		raw, err := os.ReadFile(filepath.Join(source, card.Path))
		if err != nil {
			t.Fatal(err)
		}
		writeOwnerRejoinFixtureFile(t, filepath.Join(target, card.Path), raw, os.FileMode(card.Mode))
	}
	mirrorOwnerRejoinCards(t, source, target)
	return ownerRejoinApplyFixture{plan: p, payload: payload, source: source, target: target}
}

// mirrorOwnerRejoinCards makes the returning worktree a faithful copy of the
// source board's card tree.  A worktree checked out at HEAD still holds cards
// the source board has since relocated or archived, and leaving those behind
// would make the rejoined board fail ordinary validation for reasons that have
// nothing to do with the transaction under test.
func mirrorOwnerRejoinCards(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(target, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(target, name)
		if relErr != nil {
			return relErr
		}
		if strings.HasPrefix(filepath.Base(rel), ".task-manager") {
			return nil
		}
		if _, statErr := os.Lstat(filepath.Join(source, rel)); errors.Is(statErr, fs.ErrNotExist) {
			return os.Remove(name)
		} else if statErr != nil {
			return statErr
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func ownerRejoinCommonBytes(t *testing.T, board string) []byte {
	t.Helper()
	s, release, err := acquireSharedOwnerRejoin(board)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.root.ReadFile(sharedStateFile)
	if closeErr := release(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeOwnerRejoinFixtureFile(t *testing.T, name string, raw []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}

func TestSameCommonOwnerRejoinResumesEveryPhaseAndClass(t *testing.T) {
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 1}})
	points := []string{
		"after-owner-rejoin-plan", "after-owner-rejoin-payload", "after-owner-rejoin-local-pending",
		"after-owner-rejoin-common-pending", "after-owner-rejoin-local-protocol", "after-owner-rejoin-class-transition",
		"after-owner-rejoin-class-ids", "after-owner-rejoin-class-repair", "after-owner-rejoin-class-relocation",
		"after-owner-rejoin-class-archive", "after-owner-rejoin-capacity-artifact", "after-owner-rejoin-class-capacity",
		"after-owner-rejoin-full-revalidate", "after-owner-rejoin-local-completed", "after-owner-rejoin-common-clear",
	}
	for _, point := range points {
		err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, func(got string) error {
			if got == point {
				return errors.New("stop at " + point)
			}
			return nil
		})
		if err == nil || !strings.Contains(err.Error(), "stop at "+point) {
			t.Fatalf("cutpoint %s: %v", point, err)
		}
	}
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	state := c5cSharedState(t, fx.target)
	if state.Phase != "active" || state.PendingOwnerRejoin != nil || len(state.CompletedOwnerRejoins) != 1 {
		t.Fatalf("completion state=%+v", state)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatalf("completed rejoin was not admitted by the ordinary runtime: %v", err)
	}
}

func TestSameCommonOwnerRejoinPolicyAndEmptyReservationFloors(t *testing.T) {
	fx := newOwnerRejoinApplyFixture(t, true, []ReservationFloor{})
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	state := c5cSharedState(t, fx.target)
	if state.Policy == nil || state.ReservationFloors == nil || len(state.ReservationFloors) != 0 {
		t.Fatalf("policy or empty floors lost: %+v", state)
	}
	// The activation receipt is the board's own identity check.  A rejoined
	// policy-bearing board that still named the source root would refuse
	// itself, so this is the assertion that keeps the rebinding honest.
	activation, err := loadPolicyActivation(mustOwnerRejoinRoot(t, fx.target))
	if err != nil {
		t.Fatal(err)
	}
	if activation.Plan.Root != fx.target || activation.AuthorityID != fx.plan.TargetPolicyAuthority {
		t.Fatalf("activation root or authority wrong: %+v", activation.Plan.Root)
	}
	if _, err := Ready(fx.target); err != nil {
		t.Fatalf("completed policy-bearing rejoin was not admitted by the ordinary runtime: %v", err)
	}
}

func mustOwnerRejoinRoot(t *testing.T, dir string) *os.Root {
	t.Helper()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestSameCommonOwnerRejoinTamperStopsBeforeFurtherMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(string) error
	}{
		{name: "third-bytes", mutate: func(name string) error { return os.WriteFile(name, []byte("third"), 0o600) }},
		{name: "missing", mutate: os.Remove},
		{name: "symlink", mutate: func(name string) error {
			if err := os.Remove(name); err != nil {
				return err
			}
			return os.Symlink("elsewhere", name)
		}},
		{name: "mode", mutate: func(name string) error { return os.Chmod(name, 0o644) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{{Prefix: "TASK", Through: 1}})
			stop := "after-owner-rejoin-class-ids"
			_ = applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, func(point string) error {
				if point == stop {
					return errors.New("stop")
				}
				return nil
			})
			if err := tc.mutate(filepath.Join(fx.target, transitionsFile)); err != nil {
				t.Fatal(err)
			}
			beforeBoard, beforeCommon := boardBytes(t, fx.target), ownerRejoinCommonBytes(t, fx.target)
			if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err == nil {
				t.Fatal("tamper accepted")
			}
			if !reflectEqualBoard(beforeBoard, boardBytes(t, fx.target)) || !bytes.Equal(beforeCommon, ownerRejoinCommonBytes(t, fx.target)) {
				t.Fatal("rejected resume mutated state")
			}
		})
	}
}
