package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func assertLocked(t *testing.T, err error) {
	t.Helper()
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("want namespace lock error, got %v", err)
	}
}

func boardSessionRequest() (ClaimRequest, TransitionRequest) {
	claim := ClaimRequest{ID: "TASK-1", Owner: "session-test", Token: testToken}
	transition := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("7", 32), From: "todo", To: "doing"}
	return claim, transition
}

func TestSharedNamespaceLockBlocksBoardSessions(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	claim, transition := boardSessionRequest()
	if _, err := Claim(b, claim); err != nil {
		t.Fatal(err)
	}

	_, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, b)

	if _, err := List(b); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("List unexpectedly bypassed namespace lock")
	}
	if _, err := Ready(b); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("Ready unexpectedly bypassed namespace lock")
	}
	if _, err := Claim(b, claim); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("Claim unexpectedly bypassed namespace lock")
	}
	if _, err := Release(b, claim); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("Release unexpectedly bypassed namespace lock")
	}
	if _, err := ClaimResume(b, claim); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("ClaimResume unexpectedly bypassed namespace lock")
	}
	if _, err := Transition(b, transition); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("Transition unexpectedly bypassed namespace lock")
	}
	if _, err := Recover(b, transition); err != nil {
		assertLocked(t, err)
	} else {
		t.Fatal("Recover unexpectedly bypassed namespace lock")
	}
	if !reflect.DeepEqual(before, boardBytes(t, b)) {
		t.Fatal("namespace-locked operations changed the board")
	}

	if err := release(); err != nil {
		t.Fatal(err)
	}
	// This is a valid replay, proving the preceding failures were from the
	// common lock and not from an invalid claim fixture.
	replayed, err := Claim(b, claim)
	if err != nil || replayed.Status != "held" {
		t.Fatalf("claim replay after release = %+v, %v", replayed, err)
	}
}

func TestCompletedTransitionReplayAfterNamespaceLockRelease(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	claim, req := boardSessionRequest()
	if _, err := Claim(b, claim); err != nil {
		t.Fatal(err)
	}
	want, err := Transition(b, req)
	if err != nil {
		t.Fatal(err)
	}
	_, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Transition(b, req); err == nil {
		t.Fatal("transition bypassed held namespace lock")
	} else {
		assertLocked(t, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	got, err := Transition(b, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("completed replay = %+v, want %+v", got, want)
	}
}

func TestSharedInitializingNamespaceBlocksBoardSessions(t *testing.T) {
	_, a, b := sharedFixture(t)
	claim, transition := boardSessionRequest()
	if _, err := Claim(b, claim); err != nil {
		t.Fatal(err)
	}
	want, err := Transition(b, transition)
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("synthetic initialization stop")
	if _, err := enableSharedStep(a, false, func(phase string) error {
		if phase == "after-initializing" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("activation did not stop after initialization: %v", err)
	}
	before := boardBytes(t, b)
	checks := []struct {
		name string
		call func() error
	}{
		{"list", func() error { _, err := List(b); return err }},
		{"ready", func() error { _, err := Ready(b); return err }},
		{"claim", func() error { _, err := Claim(b, claim); return err }},
		{"release", func() error { _, err := Release(b, claim); return err }},
		{"resume", func() error { _, err := ClaimResume(b, claim); return err }},
		{"transition", func() error { _, err := Transition(b, transition); return err }},
		{"recover", func() error { _, err := Recover(b, transition); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			err := check.call()
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), "initializing") {
				t.Fatalf("want initializing gate, got %v", err)
			}
		})
	}
	if !reflect.DeepEqual(before, boardBytes(t, b)) {
		t.Fatal("initializing namespace changed the board")
	}
	if _, err := EnableShared(a, true); err != nil {
		t.Fatal(err)
	}
	got, err := Recover(b, transition)
	if err != nil || got != want {
		t.Fatalf("completed replay after initialization = %+v, %v", got, err)
	}
}

func TestBoardLockFailureReleasesNamespaceLock(t *testing.T) {
	_, a, _ := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(a)
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(r)
	if err != nil {
		r.Close()
		t.Fatal(err)
	}
	if _, err := List(a); err == nil {
		t.Fatal("board lock was bypassed")
	} else if !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("unexpected board lock error: %v", err)
	}
	if err := unlock(); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	// If List leaked the common lock while failing on the board lock, this
	// acquisition would fail even though the board lock is now gone.
	s, release, err := acquireShared(a, false)
	if err != nil || s == nil {
		t.Fatalf("common lock was not released: session=%v err=%v", s, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedSharedStateBlocksNonIDBoardOperations(t *testing.T) {
	_, a, b := sharedFixture(t)
	if _, err := EnableShared(a, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, sharedStateFile)
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		_ = release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, b)
	if _, err := List(b); err == nil {
		t.Fatal("malformed common state was ignored")
	} else if strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("malformed state reported only a lock error: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, b)) {
		t.Fatal("malformed shared state changed the board")
	}
}

func TestNonGitBoardSessionRemainsLocal(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{Title: "local"}); err != nil {
		t.Fatal(err)
	}
	entries, err := List(board)
	if err != nil || len(entries) != 1 {
		t.Fatalf("local List = %d, %v", len(entries), err)
	}
}
