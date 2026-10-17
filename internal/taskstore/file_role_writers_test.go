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

// Synthetic authority setup exercises writers without enabling public activation.
func bindWriterRoleFixture(t *testing.T, dir string, p boardpolicy.Policy) {
	t.Helper()
	state := bindAuthorityFixture(t, dir, policyAuthorityBinding{AuthorityID: strings.Repeat("a", 32), Scope: "local"})
	r, err := openBoard(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	j, err := loadTransitions(r)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := p.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	j.PolicyDigest = bytesDigest(canonical)
	raw, err := encodeTransitionJournal(j, p)
	if err != nil {
		t.Fatal(err)
	}
	state.Canonical, state.Digest = canonical, j.PolicyDigest
	state.Plan.TargetJournal = bytesDigest(raw)
	for name, content := range map[string][]byte{policyFile: canonical, transitionsFile: raw} {
		if err := r.WriteFile(name, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := savePolicyActivation(r, state, false); err != nil {
		t.Fatal(err)
	}
}

func writerRolePolicy(t *testing.T, version int) boardpolicy.Policy {
	t.Helper()
	d := boardpolicy.Declaration{Modules: []string{}}
	var p boardpolicy.Policy
	var err error
	if version == 5 {
		p, err = boardpolicy.NewV5(d, []boardpolicy.FileRoleV5{})
	} else {
		d.FileRolesDeclared = true
		p, err = boardpolicy.New(d)
	}
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFileRoleWritersRejectBeforePublication(t *testing.T) {
	t.Parallel()
	for _, version := range []int{4, 5} {
		for _, name := range []string{"README.md", "INDEX.md", "unexpected.bin"} {
			t.Run(string(rune('0'+version))+"/"+name, func(t *testing.T) {
				dir, req, _, _ := transitionFixture(t)
				bindWriterRoleFixture(t, dir, writerRolePolicy(t, version))
				discoveryFile(t, dir, name, "preserve exactly\r\n")
				for operation, run := range map[string]func() error{
					"create":     func() error { _, err := Create(dir, CreateRequest{Title: "must not publish"}); return err },
					"transition": func() error { _, err := Transition(dir, req); return err },
				} {
					before := boardBytes(t, dir)
					err := run()
					if err == nil || !strings.Contains(err.Error(), "file-role admission") {
						t.Fatalf("%s did not reject file roles: %v", operation, err)
					}
					if !reflect.DeepEqual(before, boardBytes(t, dir)) {
						t.Fatalf("%s changed cards, IDs, claims, journals, or directory entries", operation)
					}
				}
			})
		}
	}
}

func TestFileRoleWritersAcceptUnprojectedBoard(t *testing.T) {
	t.Parallel()
	for _, version := range []int{4, 5} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			dir, req, _, want := transitionFixture(t)
			bindWriterRoleFixture(t, dir, writerRolePolicy(t, version))
			created, err := Create(dir, CreateRequest{Title: "synthetic new card"})
			if err != nil || created.Card.ID != "TASK-2" {
				t.Fatalf("create = %+v, %v", created, err)
			}
			result, err := Transition(dir, req)
			if err != nil || result.Status != "completed" {
				t.Fatalf("transition = %+v, %v", result, err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, result.Path))
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("transition changed unrelated card bytes: %q, %v", raw, err)
			}
		})
	}
}

func TestFileRolePendingRecoveryDoesNotReadmitCurrentBoard(t *testing.T) {
	t.Parallel()
	for _, phase := range []string{"after-journal", "after-target", "after-source", "after-receipt"} {
		for _, recoverOnly := range []bool{false, true} {
			t.Run(phase+"/"+map[bool]string{false: "transition", true: "recover"}[recoverOnly], func(t *testing.T) {
				dir, req, _, want := transitionFixture(t)
				p := writerRolePolicy(t, 5)
				bindWriterRoleFixture(t, dir, p)
				stop := errors.New("synthetic interruption")
				if _, err := transitionWithStep(dir, req, func(at string) error {
					if at == phase {
						return stop
					}
					return nil
				}); !errors.Is(err, stop) {
					t.Fatalf("interruption not reached: %v", err)
				}
				discoveryFile(t, dir, "README.md", "external unassigned file")
				r, err := openBoard(dir)
				if err != nil {
					t.Fatal(err)
				}
				err = admitBoardFiles(r, p)
				if closeErr := r.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
				if err == nil {
					t.Fatal("fresh admission unexpectedly accepts the board")
				}
				result, err := executeTransition(dir, req, recoverOnly, nil)
				if err != nil || result.Status != "completed" {
					t.Fatalf("original request did not recover: %+v, %v", result, err)
				}
				raw, err := os.ReadFile(filepath.Join(dir, result.Path))
				if err != nil || !bytes.Equal(raw, want) {
					t.Fatalf("recovery changed unrelated bytes: %q, %v", raw, err)
				}
				before := boardBytes(t, dir)
				if _, err := Transition(dir, req); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatal("completed replay changed board")
				}
			})
		}
	}
}
