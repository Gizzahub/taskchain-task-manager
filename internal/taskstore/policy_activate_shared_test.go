package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func defaultPolicyBytes(t *testing.T) []byte {
	t.Helper()
	raw, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestActivateSharedPolicyFreshReplayAndMarkerlessJoin(t *testing.T) {
	t.Parallel()
	repo, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	first, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true})
	if err != nil || first.Boards != 2 || first.Replayed {
		t.Fatalf("activate=%+v %v", first, err)
	}
	for _, dir := range []string{a, b} {
		if _, err := List(dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Create(b, CreateRequest{Title: "after activation"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(b, ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, b)
	replay, err := ActivatePolicy(b, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true})
	if err != nil || !replay.Replayed || replay.AuthorityID != first.AuthorityID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, b)) {
		t.Fatal("completed replay changed evolved board")
	}
	newRoot := filepath.Join(t.TempDir(), "new")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	newBoard := filepath.Join(newRoot, "tasks")
	if _, err := List(newBoard); err == nil {
		t.Fatal("markerless board joined implicitly")
	}
	joined, err := ActivatePolicy(newBoard, raw, PolicyActivationOptions{AllWorktrees: true})
	if err != nil || joined.Boards != 1 || joined.Replayed || joined.AuthorityID != first.AuthorityID {
		t.Fatalf("join=%+v %v", joined, err)
	}
	r, err := openBoard(newBoard)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := loadIDs(r)
	if err != nil || ledger.SchemaVersion != 3 {
		t.Fatalf("join IDs=%+v %v", ledger, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(newBoard, CreateRequest{Title: "joined"}); err != nil {
		t.Fatal(err)
	}
}

func TestActivateSharedPolicyResumesEveryPublicationBoundary(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-common-policy-pending", "board-0/after-local-pending", "board-0/after-policy", "board-0/after-policy-ids", "board-0/after-policy-journal", "board-0/after-local-completed", "after-policy-board-0", "after-policy-board-1", "after-common-policy-active"} {
		t.Run(phase, func(t *testing.T) {
			_, a, b := sharedFixture(t)
			if _, err := EnableShared(a, false); err != nil {
				t.Fatal(err)
			}
			raw := defaultPolicyBytes(t)
			stop := errors.New("synthetic shared stop")
			if _, err := activatePolicyWithStep(a, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			if phase != "after-common-policy-active" {
				if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "resume") {
					t.Fatalf("implicit resume: %v", err)
				}
				if _, err := List(b); err == nil {
					t.Fatal("partial shared activation admitted other board")
				}
			}
			result, err := ActivatePolicy(a, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true})
			if err != nil || result.Status != "completed" {
				t.Fatalf("resume=%+v %v", result, err)
			}
			for _, dir := range []string{a, b} {
				if _, err := List(dir); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestActivateSharedPolicyJoinResumesIDsAndPreservesMode(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-common-policy-pending", "board-0/after-policy-ids", "board-0/after-policy-journal"} {
		t.Run(phase, func(t *testing.T) {
			repo, a, _ := sharedFixture(t)
			if _, err := EnableShared(a, false); err != nil {
				t.Fatal(err)
			}
			raw := defaultPolicyBytes(t)
			if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
				t.Fatal(err)
			}
			newRoot := filepath.Join(t.TempDir(), "new")
			sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
			dir := filepath.Join(newRoot, "tasks")
			if err := os.Chmod(filepath.Join(dir, idsFile), 0o640); err != nil {
				t.Fatal(err)
			}
			stop := errors.New("join stop")
			if _, err := activatePolicyWithStep(dir, raw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true}); err == nil {
				t.Fatal("join resumed from wrong owner")
			}
			if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{Resume: true, AllWorktrees: true}); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(dir, idsFile))
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("ID mode changed: %v %v", info, err)
			}
			if _, err := List(dir); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActivateSharedPolicyCopiedCompletedReceiptNeedsExplicitJoin(t *testing.T) {
	t.Parallel()
	repo, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	raw := defaultPolicyBytes(t)
	if _, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatal(err)
	}
	sharedGit(t, repo, "add", "tasks")
	sharedGit(t, repo, "commit", "-m", "synthetic completed policy")
	newRoot := filepath.Join(t.TempDir(), "copy")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	dir := filepath.Join(newRoot, "tasks")
	if _, err := List(dir); err == nil {
		t.Fatal("copied receipt bypassed admission")
	}
	if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
		t.Fatalf("explicit copied join: %v", err)
	}
	if _, err := List(dir); err != nil {
		t.Fatal(err)
	}
}

func TestActivateSharedPolicyMigratesSameLocalPolicy(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	policy := filepathPolicy(t)
	raw, err := policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{a, b} {
		if _, err := ActivatePolicy(dir, raw, PolicyActivationOptions{AllWorktrees: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	result, err := ActivatePolicy(a, raw, PolicyActivationOptions{AllWorktrees: true})
	if err != nil || result.Scope != "shared" {
		t.Fatalf("migrate=%+v %v", result, err)
	}
	for _, dir := range []string{a, b} {
		if _, err := List(dir); err != nil {
			t.Fatal(err)
		}
	}
}

func TestActivateLocalPolicyLockFailureIsVisible(t *testing.T) {
	t.Parallel()
	dir := claimBoard(t)
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := ActivatePolicy(dir, defaultPolicyBytes(t), PolicyActivationOptions{AllWorktrees: true}); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("lock error lost: %v", err)
	}
}
