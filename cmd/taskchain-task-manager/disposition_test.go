package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestQueueCLIRoutesP0IssueAndDecisionWithoutSyntheticScope(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(board, "issue"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, raw := range map[string]string{
		"issue/ISSUE-1.md": "---\nid: ISSUE-1\nstatus: open\npriority: P0\nexecution-mode: external\nneeds-human: true\n---\n",
		"todo/TASK-1.md":   "---\nid: TASK-1\nstatus: pending\nexecution-mode: decision\nneeds-human: true\n---\n",
		"todo/TASK-2.md":   "---\nid: TASK-2\nstatus: pending\nallowed-paths: [src/worker.go]\n---\n",
	} {
		if err := os.WriteFile(filepath.Join(board, path), []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, diagnostics bytes.Buffer
	if code := run([]string{"queue", "--dir", board, "--json"}, &out, &diagnostics); code != 0 || diagnostics.Len() != 0 {
		t.Fatalf("queue: code=%d stderr=%s", code, diagnostics.String())
	}
	var projection taskstore.QueueProjection
	if err := json.Unmarshal(out.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	if projection.RunnableCount != 3 || projection.AgentRunnableCount != 1 {
		t.Fatalf("counts: %+v", projection)
	}
	modes := map[string]string{}
	for _, item := range projection.Runnable {
		modes[item.Card.ID] = string(item.ExecutionMode)
	}
	if modes["ISSUE-1"] != "external" || modes["TASK-1"] != "decision" || modes["TASK-2"] != "implementation" {
		t.Fatalf("routes: %+v", modes)
	}
	if projection.AgentRunnable[0].Card.ID != "TASK-2" {
		t.Fatalf("agent runnable: %+v", projection.AgentRunnable)
	}
	if err := os.WriteFile(filepath.Join(board, "todo/TASK-2.md"), []byte("---\nid: TASK-2\nstatus: pending\nallowed-paths: [tasks/fake.md]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diagnostics.Reset()
	if code := run([]string{"queue", "--dir", board, "--json"}, &out, &diagnostics); code != 1 || out.Len() != 0 || diagnostics.Len() == 0 {
		t.Fatalf("invalid scope: code=%d out=%q stderr=%q", code, out.String(), diagnostics.String())
	}
}
