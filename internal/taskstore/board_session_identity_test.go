package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBoardSessionRejectsLostSharedIdentityIncludingReplay(t *testing.T) {
	for _, kind := range []string{"missing", "mismatch", "non-git"} {
		t.Run(kind, func(t *testing.T) {
			_, a, b := sharedFixture(t)
			if _, err := EnableShared(a, false); err != nil {
				t.Fatal(err)
			}
			claim := ClaimRequest{ID: "TASK-1", Owner: "worker", Token: testToken}
			req := TransitionRequest{ID: claim.ID, Owner: claim.Owner, Token: claim.Token, RequestID: strings.Repeat("9", 32), From: "todo", To: "doing"}
			if _, err := Claim(b, claim); err != nil {
				t.Fatal(err)
			}
			if _, err := Transition(b, req); err != nil {
				t.Fatal(err)
			}
			s, release, err := acquireShared(a, false)
			if err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(s.location.CommonDirectory, "taskchain-task-manager", "ids", s.location.NamespaceKey, sharedStateFile)
			original, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := release(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				if err := os.Remove(statePath); err != nil {
					t.Fatal(err)
				}
			case "mismatch":
				state := *s.state
				state.NamespaceID = strings.Repeat("e", 32)
				raw, err := json.Marshal(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statePath, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			case "non-git":
				target := filepath.Join(t.TempDir(), "moved-board")
				if err := os.Rename(b, target); err != nil {
					t.Fatal(err)
				}
				b = target
			}
			before := boardBytes(t, b)
			checks := []func() error{
				func() error { _, e := List(b); return e },
				func() error { _, e := Claim(b, claim); return e },
				func() error { _, e := Release(b, claim); return e },
				func() error { _, e := Transition(b, req); return e },
				func() error { _, e := Recover(b, req); return e },
			}
			for _, check := range checks {
				if err := check(); err == nil || !strings.Contains(err.Error(), "binding") {
					t.Fatalf("missing binding error: %v", err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, b)) {
					t.Fatal("rejection changed board")
				}
			}
			if kind != "non-git" {
				if err := os.WriteFile(statePath, original, 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Recover(b, req); err != nil {
					t.Fatalf("restored receipt replay: %v", err)
				}
			}
		})
	}
}

func TestBoardSessionLegacyWithoutLedgerAndFailureCleanup(t *testing.T) {
	_, a, _ := sharedFixture(t)
	if err := os.Remove(filepath.Join(a, idsFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := List(a); err != nil {
		t.Fatalf("legacy read: %v", err)
	}
	if err := os.WriteFile(filepath.Join(a, idsFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, a)
	if _, err := List(a); err == nil {
		t.Fatal("malformed local ledger accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, a)) {
		t.Fatal("validation error changed board")
	}
	_, release, err := acquireShared(a, false)
	if err != nil {
		t.Fatalf("validation failure leaked common lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
