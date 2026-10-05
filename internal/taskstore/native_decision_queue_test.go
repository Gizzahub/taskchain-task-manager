package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestQueueIncludesOnlyProposedNativeDecisions(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	writeBoardFile(t, root, "decision/README.md", "# decisions\n")
	writeBoardFile(t, root, "decision/a-proposed.md", decisionCard("TASK-8", "Proposed", "execution-mode: decision\nneeds-human: true\n"))
	writeBoardFile(t, root, "decision/b-proposed.md", decisionCard("TASK-9", "Proposed", "execution-mode: decision\nneeds-human: true\n"))
	writeBoardFile(t, root, "decision/accepted.md", decisionCard("TASK-7", "Accepted", "execution-mode: decision\nneeds-human: true\n"))
	writeBoardFile(t, root, "decision/rejected.md", decisionCard("TASK-6", "Rejected", "execution-mode: decision\nneeds-human: true\n"))
	writeBoardFile(t, root, "todo/agent.md", "---\nid: TASK-1\ntitle: Agent\nallowed-paths: [internal/taskstore/store.go]\n---\n")
	writeBoardFile(t, root, "todo/blocked.md", "---\nid: TASK-2\ntitle: Waiting\nallowed-paths: [internal/taskstore/queue_routing.go]\ndepends-on: [TASK-7]\n---\n")

	queue, err := Queue(root)
	if err != nil {
		t.Fatal(err)
	}
	if queue.RunnableCount != len(queue.Runnable) || queue.AgentRunnableCount != len(queue.AgentRunnable) {
		t.Fatalf("counts drifted: %+v", queue)
	}
	if queue.RunnableCount != 3 || queue.AgentRunnableCount != 1 {
		t.Fatalf("queue = %+v", queue)
	}
	wantRunnable := []string{"decision/a-proposed.md", "decision/b-proposed.md", "todo/agent.md"}
	wantIDs := []string{"TASK-8", "TASK-9", "TASK-1"}
	for i, item := range queue.Runnable {
		if item.Path != wantRunnable[i] || item.Card.ID != wantIDs[i] {
			t.Fatalf("runnable[%d] = %s %s", i, item.Path, item.Card.ID)
		}
	}
	for _, item := range queue.Runnable[:2] {
		if !item.NeedsHuman || item.ExecutionMode != "decision" || len(item.AllowedPaths) != 0 || item.Card.Status != "Proposed" {
			t.Fatalf("proposed decision = %+v", item)
		}
	}
	if queue.AgentRunnable[0].Card.ID != "TASK-1" || queue.AgentRunnable[0].NeedsHuman {
		t.Fatalf("agent runnable = %+v", queue.AgentRunnable)
	}
	for _, item := range queue.Runnable {
		if item.Card.ID == "TASK-7" || item.Card.ID == "TASK-6" || item.Card.ID == "TASK-2" {
			t.Fatalf("non-proposed or blocked card queued: %+v", queue.Runnable)
		}
	}
	ready, err := Ready(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Card.ID != "TASK-1" {
		t.Fatalf("ready = %+v", ready)
	}
}

func TestQueueAcceptsEmptyDecisionDirectoryAndReadme(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "decision"), 0o755); err != nil {
		t.Fatal(err)
	}
	queue, err := Queue(root)
	if err != nil || queue.RunnableCount != 0 || queue.AgentRunnableCount != 0 || len(queue.Runnable) != 0 || len(queue.AgentRunnable) != 0 {
		t.Fatalf("empty decision dir queue = %+v, %v", queue, err)
	}
	writeBoardFile(t, root, "decision/README.md", "# index\n")
	listed, err := List(root)
	if err != nil || len(listed) != 0 {
		t.Fatalf("readme-only decision dir listed %+v, %v", listed, err)
	}
	queue, err = Queue(root)
	if err != nil || queue.RunnableCount != 0 || len(queue.AgentRunnable) != 0 {
		t.Fatalf("readme queue = %+v, %v", queue, err)
	}
}

func TestNativeDecisionNestedTodoCategoryStaysOpaque(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	writeBoardFile(t, root, "decision/todo/TASK-3.md", decisionCard("TASK-3", "Proposed", "execution-mode: decision\nneeds-human: true\n"))
	listed, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != "decision/todo/TASK-3.md" || listed[0].Card.Status != "Proposed" {
		t.Fatalf("nested decision = %+v", listed)
	}
	queue, err := Queue(root)
	if err != nil {
		t.Fatal(err)
	}
	if queue.RunnableCount != 0 || queue.AgentRunnableCount != 0 {
		t.Fatalf("nested category entered the queue: %+v", queue)
	}
	ready, err := Ready(root)
	if err != nil || len(ready) != 0 {
		t.Fatalf("nested category became ready: %+v, %v", ready, err)
	}
}

func TestNativeDecisionActivatedModuleStaysInventory(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	writeBoardFile(t, board, "backend/decision/TASK-50.md", decisionCard("TASK-50", "Proposed", "execution-mode: decision\nneeds-human: true\n"))
	listed, err := List(board)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Path != "backend/decision/TASK-50.md" || listed[0].Card.Status != "Proposed" {
		t.Fatalf("module decision = %+v", listed)
	}
	queue, err := Queue(board)
	if err != nil || queue.RunnableCount != 0 || queue.AgentRunnableCount != 0 || len(queue.Runnable) != 0 || len(queue.AgentRunnable) != 0 {
		t.Fatalf("module decision queue = %+v, %v", queue, err)
	}
	ready, err := Ready(board)
	if err != nil || len(ready) != 0 {
		t.Fatalf("module decision ready = %+v, %v", ready, err)
	}
}

func TestQueueRejectsProposedDecisionRoute(t *testing.T) {
	t.Parallel()
	cases := []string{
		decisionCard("TASK-4", "Proposed", "execution-mode: decision\n"),
		decisionCard("TASK-4", "Proposed", "execution-mode: implementation\nneeds-human: true\nallowed-paths: [internal/taskstore/store.go]\n"),
		decisionCard("TASK-4", "Proposed", "execution-mode: external\nneeds-human: true\n"),
		decisionCard("TASK-4", "Proposed", "execution-mode: decision\nneeds-human: true\nallowed-paths: [internal/taskstore/store.go]\n"),
		decisionCard("TASK-4", "Proposed", "needs-human: true\n"),
	}
	for _, raw := range cases {
		root := filepath.Join(t.TempDir(), "tasks")
		if err := Init(root); err != nil {
			t.Fatal(err)
		}
		writeBoardFile(t, root, "decision/proposed.md", raw)
		before := boardBytes(t, root)
		projection, err := Queue(root)
		if err == nil || projection.Runnable != nil || projection.AgentRunnable != nil || projection.RunnableCount != 0 || projection.AgentRunnableCount != 0 {
			t.Fatalf("Queue() = %+v, %v for %s", projection, err, raw)
		}
		if !strings.Contains(err.Error(), "native decision Proposed") {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, root)) {
			t.Fatal("rejected queue changed the board")
		}
	}
}

func TestQueueStillRejectsUnknownRootAndSymlink(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	writeBoardFile(t, root, "decision/proposed.md", decisionCard("TASK-4", "Proposed", "execution-mode: decision\nneeds-human: true\n"))
	if err := os.Mkdir(filepath.Join(root, "decisions"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Queue(root); err == nil || !strings.Contains(err.Error(), "unsupported task directory") {
		t.Fatalf("unknown root directory error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "decisions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "todo"), filepath.Join(root, "notes")); err != nil {
		t.Fatal(err)
	}
	if _, err := Queue(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func decisionCard(id, status, extra string) string {
	return "---\nid: " + id + "\ntitle: Decide\nstatus: " + status + "\n" + extra + "---\n"
}

func writeBoardFile(t *testing.T, root, rel, raw string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}
