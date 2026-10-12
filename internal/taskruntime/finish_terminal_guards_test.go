package taskruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A FINISHING receipt can have been written by a process on another host or
// before a crash.  Terminal verbs must not replace it merely because its
// liveness cannot be decided locally.
func TestTerminalVerbsRefuseAnyFinishingReceipt(t *testing.T) {
	verbs := []struct {
		name string
		run  func(*endWorld) (Response, int)
	}{
		{"abort", func(w *endWorld) (Response, int) {
			return w.service().Abort(context.Background(), "sample-task", "stop")
		}},
		{"discard", func(w *endWorld) (Response, int) {
			return w.service().Discard(context.Background(), "sample-task", "cleanup", "")
		}},
		{"settle", func(w *endWorld) (Response, int) { return w.service().Settle(context.Background(), "sample-task") }},
		{"close", func(w *endWorld) (Response, int) {
			return w.service().Close(context.Background(), "sample-task", "administrative")
		}},
	}
	for _, verb := range verbs {
		t.Run(verb.name, func(t *testing.T) {
			w := newEndWorld(t)
			if verb.name == "close" {
				admin := "schema-version: 3\nrepository-id: sample-repo\nworktree-roots:\n  testhost: " + filepath.Dir(w.worktree) + "\nintegration-provider: gz-git\nintegration-network-policy: allow\nexecution-administrators:\n  - {actor: tester, host: testhost, kind: agent}\n"
				if err := os.WriteFile(filepath.Join(w.root, ConfigFile), []byte(admin), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.RemoveAll(w.worktree); err != nil {
					t.Fatal(err)
				}
			}
			claim := Receipt{Task: w.execution.Task, Operation: "finish", Status: StatusFinishing,
				Owner: Owner{Actor: "foreign", Host: "remote", Kind: "agent"}, Branch: w.execution.Branch,
				Worktree: w.execution.Worktree, CreatedAt: w.execution.StartedAt}
			if err := newStateStore(w.common).appendReceipt(claim); err != nil {
				t.Fatal(err)
			}
			before := w.receipts()
			result, code := verb.run(w)
			if code != 1 || result.Status != StatusBlocked || !strings.Contains(result.Reason, "FINISHING claim") {
				t.Fatalf("code=%d status=%s reason=%q", code, result.Status, result.Reason)
			}
			if after := w.receipts(); after != before {
				t.Fatalf("%s overwrote FINISHING receipt", verb.name)
			}
		})
	}
}
