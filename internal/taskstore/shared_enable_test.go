package taskstore

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func sharedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func sharedFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo := t.TempDir()
	sharedGit(t, repo, "init", "-b", "fixture")
	sharedGit(t, repo, "config", "commit.gpgsign", "false")
	board := filepath.Join(repo, "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	sharedGit(t, repo, "add", "tasks")
	sharedGit(t, repo, "commit", "-m", "synthetic initial board")
	other := filepath.Join(t.TempDir(), "linked")
	sharedGit(t, repo, "worktree", "add", "--detach", other, "HEAD")
	return repo, board, filepath.Join(other, "tasks")
}

func TestSharedEnableBothWorktreesAndNewMarkerlessWorktree(t *testing.T) {
	t.Parallel()
	repo, a, b := sharedFixture(t)
	if _, err := Create(b, CreateRequest{ID: "TASK-090", Title: "uncommitted other"}); err != nil {
		t.Fatal(err)
	}
	result, err := EnableShared(a, false)
	if err != nil || result.Phase != "active" || result.Worktrees != 2 {
		t.Fatalf("enable=%+v %v", result, err)
	}
	for _, board := range []string{a, b} {
		r, err := openBoard(board)
		if err != nil {
			t.Fatal(err)
		}
		ledger, err := loadIDs(r)
		r.Close()
		if err != nil || ledger.SchemaVersion != 3 || ledger.Namespace != result.NamespaceID {
			t.Fatalf("binding=%+v %v", ledger, err)
		}
	}
	one, err := Create(a, CreateRequest{Title: "after shared"})
	if err != nil || one.Card.ID != "TASK-91" {
		t.Fatalf("first=%+v %v", one, err)
	}
	two, err := Create(b, CreateRequest{Title: "other shared"})
	if err != nil || two.Card.ID != "TASK-92" {
		t.Fatalf("second=%+v %v", two, err)
	}
	if _, err := Create(a, CreateRequest{ID: "TASK-92", Title: "duplicate"}); err == nil {
		t.Fatal("shared ID reused")
	}
	if _, err := Create(a, CreateRequest{ID: "TASK-40", Title: "unreserved hole"}); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "new")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	next, err := Create(filepath.Join(newRoot, "tasks"), CreateRequest{Title: "markerless"})
	if err != nil || next.Card.ID != "TASK-93" {
		t.Fatalf("new WT=%+v %v", next, err)
	}
	if _, err := ReserveIDs(b, []string{"PLAN-20"}, false); err != nil {
		t.Fatal(err)
	}
	plan, err := Create(a, CreateRequest{Kind: "plan", Title: "next plan"})
	if err != nil || plan.Card.ID != "PLAN-21" {
		t.Fatalf("plan=%+v %v", plan, err)
	}
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
}

func TestSharedActivationInterruptedResume(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-initializing", "after-local-0", "after-local-1", "after-active"} {
		t.Run(phase, func(t *testing.T) {
			_, a, b := sharedFixture(t)
			stop := errors.New("synthetic crash")
			_, err := enableSharedStep(a, false, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			if phase != "after-active" {
				before := boardBytes(t, b)
				if _, err := Create(b, CreateRequest{Title: "must block"}); err == nil || !strings.Contains(err.Error(), "initializing") {
					t.Fatalf("writer not blocked: %v", err)
				}
				if err := Init(b); err == nil || !strings.Contains(err.Error(), "initializing") {
					t.Fatalf("init not blocked: %v", err)
				}
				if _, err := ReserveIDs(b, []string{"TASK-999"}, true); err == nil || !strings.Contains(err.Error(), "initializing") {
					t.Fatalf("reserve not blocked: %v", err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, b)) {
					t.Fatal("blocked writer changed board")
				}
				if _, err := EnableShared(a, false); err == nil {
					t.Fatal("implicit resume accepted")
				}
			}
			if _, err := EnableShared(a, true); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(b, CreateRequest{Title: "after resume"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSharedBurnAndMissingCommonState(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop after shared")
	if _, err := createWithStep(a, CreateRequest{Title: "burn"}, func(at string) error {
		if at == "after-shared-reservation" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	entry, err := Create(b, CreateRequest{Title: "skip burned"})
	if err != nil || entry.Card.ID != "TASK-3" {
		t.Fatalf("burn=%+v %v", entry, err)
	}
	s, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.root.Rename(sharedStateFile, "saved-state.json"); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(a, CreateRequest{Title: "no fallback"}); err == nil || !strings.Contains(err.Error(), "no common state") {
		t.Fatalf("missing common state fallback: %v", err)
	}
}
