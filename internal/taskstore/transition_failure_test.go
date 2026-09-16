package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func transitionFixture(t *testing.T) (string, TransitionRequest, []byte, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\r\nid: TASK-1\r\nstatus: pending\r\nunknown:\r\n  nested: keep\r\n---\r\n# Synthetic\r\n```md\r\n| **Status** | [ ] Pending |\r\n```\r\n| **Status** | [ ] Pending — reason |\r\nTail without newline")
	if err := os.WriteFile(filepath.Join(dir, "todo", "custom-name.md"), raw, 0o640); err != nil {
		t.Fatal(err)
	}
	req := TransitionRequest{ID: "TASK-1", Owner: "tester", Token: testToken, RequestID: strings.Repeat("1", 32), From: "todo", To: "doing"}
	if _, err := Claim(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(raw, []byte("[ ] Pending — reason"), []byte("[~] In Progress — reason"), 1)
	return dir, req, raw, want
}

func boardBytes(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "symlink:" + target
		} else if info.Mode().IsRegular() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = info.Mode().String() + string(raw)
		} else {
			out[rel] = info.Mode().String()
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTransitionCrashPhasesAndPendingGates(t *testing.T) {
	for _, phase := range []string{"after-journal", "after-target", "after-source", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			dir, req, _, want := transitionFixture(t)
			stop := errors.New("synthetic interruption")
			_, err := transitionWithStep(dir, req, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatalf("phase %s not reached: %v", phase, err)
			}
			if phase != "after-receipt" {
				before := boardBytes(t, dir)
				claimReq := ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}
				checks := []func() error{
					func() error { return Init(dir) },
					func() error { _, e := List(dir); return e },
					func() error { _, e := Ready(dir); return e },
					func() error { _, e := Create(dir, CreateRequest{Title: "not yet"}); return e },
					func() error { _, e := Claim(dir, claimReq); return e },
					func() error { _, e := Release(dir, claimReq); return e },
					func() error { _, e := ClaimResume(dir, claimReq); return e },
				}
				for i, check := range checks {
					if err := check(); err == nil || !strings.Contains(err.Error(), "pending") {
						t.Fatalf("gate %d error=%v", i, err)
					}
				}
				wrong := req
				wrong.RequestID = strings.Repeat("2", 32)
				if _, err := Transition(dir, wrong); err == nil {
					t.Fatal("new transition accepted during pending")
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatal("blocked operations modified board")
				}
			}
			result, err := Recover(dir, req)
			if err != nil || result.Path != "doing/custom-name.md" || result.Status != "completed" {
				t.Fatalf("recover=%+v error=%v", result, err)
			}
			after, err := os.ReadFile(filepath.Join(dir, result.Path))
			if err != nil || !bytes.Equal(after, want) {
				t.Fatalf("non-status bytes changed: %q %v", after, err)
			}
			info, err := os.Stat(filepath.Join(dir, result.Path))
			if err != nil || info.Mode().Perm() != 0o640 {
				t.Fatalf("mode not preserved: %v %v", info, err)
			}
			if _, err := os.Lstat(filepath.Join(dir, "todo", "custom-name.md")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("source still exists: %v", err)
			}
			if _, err := List(dir); err != nil {
				t.Fatalf("board remains blocked: %v", err)
			}
		})
	}
}

func TestTransitionRecoveryConflictsNeverModifyBoard(t *testing.T) {
	for _, kind := range []string{"source-content", "target-content", "source-mode", "target-mode", "source-symlink", "target-symlink", "both-missing"} {
		t.Run(kind, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			phase := "after-journal"
			if strings.HasPrefix(kind, "target-") {
				phase = "after-target"
			}
			stop := errors.New("synthetic stop")
			if _, err := transitionWithStep(dir, req, func(at string) error {
				if at == phase {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "todo", "custom-name.md")
			if strings.HasPrefix(kind, "target-") {
				path = filepath.Join(dir, "doing", "custom-name.md")
			}
			switch {
			case strings.HasSuffix(kind, "content"):
				if err := os.WriteFile(path, []byte("external edit: preserve"), 0o640); err != nil {
					t.Fatal(err)
				}
			case strings.HasSuffix(kind, "mode"):
				if err := os.Chmod(path, 0o600); err != nil {
					t.Fatal(err)
				}
			case strings.HasSuffix(kind, "symlink"):
				if err := os.Rename(path, path+".backup"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(path)+".backup", path); err != nil {
					t.Fatal(err)
				}
			case kind == "both-missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			if _, err := Recover(dir, req); err == nil {
				t.Fatal("conflicting recovery accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("recovery changed conflicting board")
			}
		})
	}
}
