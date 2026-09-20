package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSharedExternalAncestorAliasCannotBypass(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	sharedGit(t, repo, "init", "-b", "fixture")
	sharedGit(t, repo, "config", "commit.gpgsign", "false")
	board := filepath.Join(repo, "sub", "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "first"}); err != nil {
		t.Fatal(err)
	}
	sharedGit(t, repo, "add", "sub")
	sharedGit(t, repo, "commit", "-m", "synthetic")
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "shared second"}); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "linked")
	sharedGit(t, repo, "worktree", "add", "--detach", other, "HEAD")
	alias := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Join(other, "sub"), alias); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(filepath.Join(alias, "tasks"), CreateRequest{Title: "must see common reservation"})
	if err != nil || entry.Card.ID != "TASK-3" {
		t.Fatalf("alias=%+v %v", entry, err)
	}
}

func TestSharedActivationBoardReplacementRejected(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	_, err := enableSharedStep(a, false, func(at string) error {
		if at != "after-initializing" {
			return nil
		}
		if err := os.Rename(b, b+"-old"); err != nil {
			return err
		}
		return os.CopyFS(b, os.DirFS(b+"-old"))
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("replacement accepted: %v", err)
	}
	r, err := openBoard(b)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ledger, err := loadIDs(r)
	if err != nil || ledger.SchemaVersion != 2 {
		t.Fatalf("replacement board modified: %+v %v", ledger, err)
	}
}

func TestSharedResumeCorruptTargetCannotReduceReservations(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	if _, err := ReserveIDs(b, []string{"TASK-90"}, false); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	if _, err := enableSharedStep(a, false, func(at string) error {
		if at == "after-initializing" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatal(err)
	}
	s, release, err := acquireShared(a, true)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.Reserved = []string{"TASK-1"}
	if err := publishSharedState(s.root, state, false); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	beforeA, beforeB := boardBytes(t, a), boardBytes(t, b)
	if _, err := EnableShared(a, true); err == nil {
		t.Fatal("corrupt plan accepted")
	}
	if !reflect.DeepEqual(beforeA, boardBytes(t, a)) || !reflect.DeepEqual(beforeB, boardBytes(t, b)) {
		t.Fatal("failed resume modified local reservations")
	}
}

func TestSharedActivationSymlinkReplacementRejected(t *testing.T) {
	t.Parallel()
	_, a, b := sharedFixture(t)
	_, err := enableSharedStep(a, false, func(at string) error {
		if at != "after-initializing" {
			return nil
		}
		if err := os.Rename(b, b+"-old"); err != nil {
			return err
		}
		return os.Symlink(b+"-old", b)
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink replacement accepted: %v", err)
	}
}

func TestSharedActivationTopologyChangeDoesNotBecomeActive(t *testing.T) {
	t.Parallel()
	repo, a, _ := sharedFixture(t)
	_, err := enableSharedStep(a, false, func(at string) error {
		if at == "after-local-0" {
			sharedGit(t, repo, "worktree", "add", "--detach", filepath.Join(t.TempDir(), "new"), "HEAD")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "inventory changed") {
		t.Fatalf("new WT accepted: %v", err)
	}
	s, release, err := acquireShared(a, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if s.state == nil || s.state.Phase != "initializing" {
		t.Fatal("invalid activation became active")
	}
}
