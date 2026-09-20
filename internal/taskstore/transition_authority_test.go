package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTransitionRechecksDependencies(t *testing.T) {
	t.Parallel()
	for _, edge := range [][2]string{{"todo", "doing"}, {"review", "done"}} {
		t.Run(edge[0]+"-"+edge[1], func(t *testing.T) {
			dir, req, raw, _ := transitionFixture(t)
			if edge[0] == "review" {
				if err := os.Mkdir(filepath.Join(dir, "review"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(dir, "todo/custom-name.md"), filepath.Join(dir, "review/custom-name.md")); err != nil {
					t.Fatal(err)
				}
			}
			req.From, req.To = edge[0], edge[1]
			raw = []byte(strings.Replace(string(raw), "id: TASK-1", "id: TASK-1\r\ndepends-on: [TASK-2]", 1))
			if err := os.WriteFile(filepath.Join(dir, edge[0], "custom-name.md"), raw, 0o640); err != nil {
				t.Fatal(err)
			}
			dep := []byte("---\nid: TASK-2\ntitle: dependency\n---\n# Dependency\n")
			if err := os.WriteFile(filepath.Join(dir, "todo/dep.md"), dep, 0o644); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := Transition(dir, req); err == nil || !strings.Contains(err.Error(), "dependency is not done") {
				t.Fatalf("dependency not enforced: %v", err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejection modified board")
			}
			if err := os.Mkdir(filepath.Join(dir, "done"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, "todo/dep.md"), filepath.Join(dir, "done/dep.md")); err != nil {
				t.Fatal(err)
			}
			if _, err := Transition(dir, req); err != nil {
				t.Fatalf("satisfied dependency rejected: %v", err)
			}
		})
	}
}

func TestClaimResumeZoneBoundary(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog", "archive", "_archive", "doing/nested"} {
		t.Run(zone, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			if _, err := Release(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
				t.Fatal(err)
			}
			if zone != "todo" {
				if err := os.MkdirAll(filepath.Join(dir, zone), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(dir, "todo/custom-name.md"), filepath.Join(dir, zone, "custom-name.md")); err != nil {
					t.Fatal(err)
				}
			}
			before := boardBytes(t, dir)
			_, err := ClaimResume(dir, ClaimRequest{ID: req.ID, Owner: req.Owner, Token: strings.Repeat("e", 32)})
			allowed := zone == "doing" || zone == "review" || zone == "blocked" || zone == "done"
			if allowed && err != nil {
				t.Fatalf("resumable card rejected: %v", err)
			}
			if !allowed {
				if err == nil {
					t.Fatal("non-workflow card resumed")
				}
				if !reflect.DeepEqual(before, boardBytes(t, dir)) {
					t.Fatal("rejection modified board")
				}
			}
		})
	}
}

func TestTransitionInvalidAuthorityPreservesBoard(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*TransitionRequest){
		"owner":              func(r *TransitionRequest) { r.Owner = "another-worker" },
		"token":              func(r *TransitionRequest) { r.Token = strings.Repeat("e", 32) },
		"missing task":       func(r *TransitionRequest) { r.ID = "TASK-999" },
		"wrong source":       func(r *TransitionRequest) { r.From, r.To = "review", "done" },
		"unsupported edge":   func(r *TransitionRequest) { r.To = "done" },
		"same zone":          func(r *TransitionRequest) { r.To = r.From },
		"invalid request id": func(r *TransitionRequest) { r.RequestID = "not-a-token" },
	} {
		t.Run(name, func(t *testing.T) {
			dir, req, _, _ := transitionFixture(t)
			mutate(&req)
			before := boardBytes(t, dir)
			if _, err := Transition(dir, req); err == nil {
				t.Fatal("invalid transition accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejected transition modified board")
			}
		})
	}
}
