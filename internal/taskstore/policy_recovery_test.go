package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestBoundTransitionRecoveryRequiresExactPolicy(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-journal", "after-target", "after-source", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			dir, req, _, want := transitionFixture(t)
			canonical := bindRuntimeFixture(t, dir, boardpolicy.Declaration{})
			stop := errors.New("synthetic interruption")
			_, err := transitionWithStep(dir, req, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			writeBoundPolicy(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}})
			before := boardBytes(t, dir)
			if _, err := Recover(dir, req); err == nil {
				t.Fatal("recovery accepted changed policy")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("failed recovery changed board")
			}
			if err := os.WriteFile(filepath.Join(dir, policyFile), canonical, 0o600); err != nil {
				t.Fatal(err)
			}
			result, err := Recover(dir, req)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, result.Path))
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("recovered bytes=%q err=%v", raw, err)
			}
		})
	}
}

func TestBoundPolicyChangeAfterTargetPreservesSource(t *testing.T) {
	t.Parallel()
	dir, req, original, _ := transitionFixture(t)
	canonical := bindRuntimeFixture(t, dir, boardpolicy.Declaration{})
	_, err := transitionWithStep(dir, req, func(at string) error {
		if at == "after-target" {
			writeBoundPolicy(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}})
		}
		return nil
	})
	if err == nil {
		t.Fatal("transition ignored policy change")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "todo/custom-name.md"))
	if err != nil || !bytes.Equal(raw, original) {
		t.Fatalf("original lost: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, policyFile), canonical, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(dir, req); err != nil {
		t.Fatal(err)
	}
}

func TestParkedTransitionCrashRecovery(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-journal", "after-target", "after-source", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			dir, req, original, _ := transitionFixture(t)
			bindRuntimeFixture(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}, Transitions: []boardpolicy.Transition{{From: "manual", To: []string{"todo"}}}})
			if err := os.Mkdir(filepath.Join(dir, "manual"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, "todo/custom-name.md"), filepath.Join(dir, "manual/custom-name.md")); err != nil {
				t.Fatal(err)
			}
			req.From, req.To = "manual", "todo"
			stop := errors.New("synthetic parking interruption")
			_, err := transitionWithStep(dir, req, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("phase not reached: %v", err)
			}
			result, err := Recover(dir, req)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, result.Path))
			if err != nil || !bytes.Equal(raw, original) {
				t.Fatalf("parking recovery lost bytes: %v", err)
			}
		})
	}
}

func TestBrokenPolicyBindingBlocksBoardOperations(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"orphan", "legacy-journal", "missing", "changed", "noncanonical", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			canonical := bindRuntimeFixture(t, dir, boardpolicy.Declaration{})
			path := filepath.Join(dir, policyFile)
			switch kind {
			case "orphan":
				if err := os.Remove(filepath.Join(dir, transitionsFile)); err != nil {
					t.Fatal(err)
				}
			case "legacy-journal":
				if err := os.WriteFile(filepath.Join(dir, transitionsFile), []byte(`{"schemaVersion":1,"records":[]}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed":
				writeBoundPolicy(t, dir, boardpolicy.Declaration{Zones: []string{"manual"}})
			case "noncanonical":
				if err := os.WriteFile(path, append(canonical, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(policyFile+".backup", path); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			claim := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
			checks := map[string]func() error{
				"init":       func() error { return Init(dir) },
				"list":       func() error { _, e := List(dir); return e },
				"ready":      func() error { _, e := Ready(dir); return e },
				"create":     func() error { _, e := Create(dir, CreateRequest{Title: "blocked"}); return e },
				"claim":      func() error { _, e := Claim(dir, claim); return e },
				"release":    func() error { _, e := Release(dir, claim); return e },
				"resume":     func() error { _, e := ClaimResume(dir, claim); return e },
				"transition": func() error { _, e := Transition(dir, req); return e },
				"recover":    func() error { _, e := Recover(dir, req); return e },
				"reserve":    func() error { _, e := ReserveIDs(dir, []string{"TASK-9"}, false); return e },
			}
			for name, check := range checks {
				if err := check(); err == nil {
					t.Fatalf("%s ignored broken binding", name)
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatalf("%s changed board", name)
				}
			}
		})
	}
}
