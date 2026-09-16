package taskstore

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSharedBundlePendingBlocksMarkerlessWorktree(t *testing.T) {
	repo, owner, _ := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(t.TempDir(), "new")
	sharedGit(t, repo, "worktree", "add", "--detach", newRoot, "HEAD")
	other := filepath.Join(newRoot, "tasks")
	s, release, err := acquireShared(owner, false)
	if err != nil {
		t.Fatal(err)
	}
	state := *s.state
	state.SchemaVersion, state.BundleProtocol = 2, 1
	state.Reserved = unionIDs(state.Reserved, []string{"TASK-2"})
	state.PendingBundle = &sharedBundlePending{RequestID: strings.Repeat("a", 32), Digest: strings.Repeat("d", 64), BoardID: strings.Repeat("b", 32), Owner: owner, IDs: []string{"TASK-2"}}
	if err := publishSharedState(s.root, state, false); err != nil {
		release()
		t.Fatal(err)
	}
	beforeCommon, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, other)
	claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
	transition := TransitionRequest{ID: "TASK-1", Owner: "worker", Token: testToken, RequestID: strings.Repeat("c", 32), From: "todo", To: "doing"}
	checks := map[string]func() error{
		"init":          func() error { return Init(other) },
		"list":          func() error { _, err := List(other); return err },
		"ready":         func() error { _, err := Ready(other); return err },
		"create":        func() error { _, err := Create(other, CreateRequest{Title: "Blocked"}); return err },
		"reserve":       func() error { _, err := ReserveIDs(other, []string{"TASK-88"}, true); return err },
		"claim":         func() error { _, err := Claim(other, claim); return err },
		"release":       func() error { _, err := Release(other, claim); return err },
		"resume":        func() error { _, err := ClaimResume(other, claim); return err },
		"transition":    func() error { _, err := Transition(other, transition); return err },
		"recover":       func() error { _, err := Recover(other, transition); return err },
		"register":      func() error { _, err := RegisterContext(other, []byte(testContextIntent)); return err },
		"enable-shared": func() error { _, err := EnableShared(other, false); return err },
	}
	for name, check := range checks {
		if err := check(); err == nil || !strings.Contains(err.Error(), "pending bundle") {
			t.Fatalf("%s bypassed common gate: %v", name, err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, other)) {
			t.Fatalf("%s modified markerless board", name)
		}
	}
	s, release, err = acquireSharedForBundle(owner, false, true)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	afterCommon, err := s.root.ReadFile(sharedStateFile)
	if err != nil || string(afterCommon) != string(beforeCommon) {
		t.Fatalf("pending common state changed: %v", err)
	}
}

func TestSharedBundleProtocolRejectsInvalidPendingEvidence(t *testing.T) {
	_, owner, _ := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(owner, false)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	base := *s.state
	base.SchemaVersion, base.BundleProtocol = 2, 1
	base.Reserved = unionIDs(base.Reserved, []string{"TASK-2"})
	pending := sharedBundlePending{RequestID: strings.Repeat("a", 32), Digest: strings.Repeat("d", 64), BoardID: strings.Repeat("b", 32), Owner: owner, IDs: []string{"TASK-2"}}
	base.PendingBundle = &pending
	if err := validateSharedState(base); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*sharedState){
		"legacy":       func(s *sharedState) { s.SchemaVersion = 1 },
		"protocol":     func(s *sharedState) { s.BundleProtocol = 0 },
		"initializing": func(s *sharedState) { s.Phase = "initializing" },
		"request":      func(s *sharedState) { s.PendingBundle.RequestID = "bad" },
		"digest":       func(s *sharedState) { s.PendingBundle.Digest = "bad" },
		"owner":        func(s *sharedState) { s.PendingBundle.Owner = "relative/board" },
		"board":        func(s *sharedState) { s.PendingBundle.BoardID = "bad" },
		"unreserved":   func(s *sharedState) { s.PendingBundle.IDs = []string{"TASK-3"} },
		"alias":        func(s *sharedState) { s.PendingBundle.IDs = []string{"TASK-02"} },
		"empty":        func(s *sharedState) { s.PendingBundle.IDs = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			state, copyPending := base, pending
			state.PendingBundle = &copyPending
			mutate(&state)
			if err := validateSharedState(state); err == nil {
				t.Fatal("invalid pending accepted")
			}
		})
	}
}
