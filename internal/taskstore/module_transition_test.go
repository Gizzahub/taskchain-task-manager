package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// This is a fresh synthetic board with declared scope, not evidence of adopting
// an existing module board or revising its ID ledger.
func moduleTransitionFixture(t *testing.T) (string, TransitionRequest, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend"}})
	if err != nil {
		t.Fatal(err)
	}
	rawPolicy, err := policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(dir, rawPolicy, PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(dir, "backend/todo/auth/session/TASK-1.md")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ntitle: synthetic module task\nstatus: pending\n---\n# Keep original body\n| **Status** | [ ] Pending |\n")
	if err := os.WriteFile(name, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveIDs(dir, nil, false); err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: "TASK-1", Owner: "tester", Token: testToken, RequestID: strings.Repeat("1", 32), From: "todo", To: "doing"}
	if _, err := Claim(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
		t.Fatal(err)
	}
	return dir, req, raw
}

func TestModuleTransitionPreservesPrefixAndRecovers(t *testing.T) {
	for _, phase := range []string{"after-journal", "after-target", "after-source", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			dir, req, raw := moduleTransitionFixture(t)
			stop := errors.New("module transition interrupted")
			_, err := transitionWithStep(dir, req, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("boundary %s not reached: %v", phase, err)
			}
			result, err := Recover(dir, req)
			if err != nil || result.Path != "backend/doing/auth/session/TASK-1.md" {
				t.Fatalf("recover=%+v: %v", result, err)
			}
			got, err := os.ReadFile(filepath.Join(dir, result.Path))
			want := bytes.Replace(raw, []byte("[ ] Pending"), []byte("[~] In Progress"), 1)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("non-status source bytes changed: %s, %v", got, err)
			}
			info, err := os.Stat(filepath.Join(dir, result.Path))
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatal("mode not preserved", err)
			}
			if _, err := Release(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
				t.Fatal(err)
			}
			if _, err := ClaimResume(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: strings.Repeat("2", 32)}); err != nil {
				t.Fatal("module resume rejected", err)
			}
		})
	}
}

func TestModuleTransitionRejectsNestedAncestorSymlinkDuringRecovery(t *testing.T) {
	for _, source := range []bool{true, false} {
		dir, req, _ := moduleTransitionFixture(t)
		stop := errors.New("stop before files")
		_, err := transitionWithStep(dir, req, func(at string) error {
			if at == "after-journal" {
				return stop
			}
			return nil
		})
		if !errors.Is(err, stop) {
			t.Fatal("pending boundary not reached", err)
		}
		if source {
			if err := os.Rename(filepath.Join(dir, "backend/todo/auth"), filepath.Join(dir, "backend/todo/moved-auth")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("moved-auth", filepath.Join(dir, "backend/todo/auth")); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Symlink("todo", filepath.Join(dir, "backend/doing")); err != nil {
			t.Fatal(err)
		}
		before := boardBytes(t, dir)
		if _, err := Recover(dir, req); err == nil || !strings.Contains(err.Error(), "not a real directory") {
			t.Fatalf("ancestor symlink accepted: %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, dir)) {
			t.Fatal("failed recovery changed board")
		}
	}
}
