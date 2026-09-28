package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNonWorkflowCompletionCannotUnblockDependency(t *testing.T) {
	t.Parallel()
	for _, zone := range []string{"plan", "plan/done", "issue", "archive", "archive/done", "_archive", "_archive/done", "done/nested"} {
		t.Run(zone, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, zone), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, zone, "card.md"), []byte("---\nid: TASK-1\nstatus: done\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "dependent", DependsOn: []string{"TASK-1"}}); err != nil {
				t.Fatal(err)
			}
			ready, err := Ready(root)
			if err != nil || len(ready) != 0 {
				t.Fatalf("ready=%v err=%v", ready, err)
			}
		})
	}
}

func TestCreateRejectsExistingCycleWithoutPublishing(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ndepends-on: [TASK-1]\n---\n")
	path := filepath.Join(root, "todo", "existing.md")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, root)
	if _, err := Create(root, CreateRequest{Title: "must not publish"}); err == nil || !strings.Contains(err.Error(), "self dependency") {
		t.Fatalf("error=%v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(raw) {
		t.Fatalf("existing card changed: %q %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "todo"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("cards=%v err=%v", entries, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, root)) {
		t.Fatal("invalid graph changed board or ID reservations")
	}
}

func TestQueueSeparatesHumanOnlyCardsWithoutChangingReady(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{name: "agent.md", raw: "---\nid: TASK-1\ntitle: Agent\nallowed-paths: [internal/taskstore/agent.go]\n---\n"},
		{name: "human.md", raw: "---\nid: TASK-2\ntitle: Human\nneeds-human: true\nallowed-paths: [internal/taskstore/human.go]\n---\n"},
	} {
		if err := os.WriteFile(filepath.Join(root, "todo", tc.name), []byte(tc.raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ready, err := Ready(root)
	if err != nil || len(ready) != 2 {
		t.Fatalf("Ready() = %+v, %v", ready, err)
	}
	queue, err := Queue(root)
	if err != nil {
		t.Fatal(err)
	}
	if queue.RunnableCount != 2 || len(queue.Runnable) != 2 {
		t.Fatalf("runnable = %+v", queue)
	}
	if queue.AgentRunnableCount != 1 || len(queue.AgentRunnable) != 1 || queue.AgentRunnable[0].Card.ID != "TASK-1" {
		t.Fatalf("agent runnable = %+v", queue)
	}
	if !queue.Runnable[1].NeedsHuman || queue.Runnable[1].Card.ID != "TASK-2" {
		t.Fatalf("human card was not visible in runnable projection: %+v", queue.Runnable)
	}
	encoded, err := json.Marshal(queue)
	if err != nil || !strings.Contains(string(encoded), `"needsHuman":true`) {
		t.Fatalf("queue JSON = %s, %v", encoded, err)
	}
	if err := os.Remove(filepath.Join(root, "todo", "agent.md")); err != nil {
		t.Fatal(err)
	}
	humanOnly, err := Queue(root)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(humanOnly)
	if err != nil || humanOnly.RunnableCount != 1 || humanOnly.AgentRunnableCount != 0 ||
		len(humanOnly.AgentRunnable) != 0 || !strings.Contains(string(encoded), `"agentRunnable":[]`) {
		t.Fatalf("human-only queue = %+v, JSON = %s, err = %v", humanOnly, encoded, err)
	}
}

func TestQueueRoutesScopedWorkAndP0Issues(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		raw  string
	}{
		{"todo/agent.md", "---\nid: TASK-1\ntitle: Agent\nallowed-paths: [internal/taskstore/queue_routing.go]\n---\n"},
		{"todo/human.md", "---\nid: TASK-2\ntitle: Human implementation\nneeds-human: true\nallowed-paths: [internal/taskstore/store.go]\n---\n"},
		{"todo/decision.md", "---\nid: TASK-5\ntitle: Choose direction\nexecution-mode: decision\nneeds-human: true\n---\n"},
		{"issue/p0.md", "---\nid: ISSUE-3\ntitle: Escalate\nstatus: open\npriority: P0\nexecution-mode: external\nneeds-human: true\n---\n"},
		{"issue/p1.md", "---\nid: ISSUE-4\ntitle: Deferred\nstatus: pending\npriority: P1\nexecution-mode: external\nneeds-human: true\n---\n"},
		{"issue/done.md", "---\nid: ISSUE-6\ntitle: Resolved\nstatus: done\npriority: P0\nexecution-mode: external\nneeds-human: true\n---\n"},
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, tc.path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, tc.path), []byte(tc.raw), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	queue, err := Queue(root)
	if err != nil {
		t.Fatal(err)
	}
	if queue.RunnableCount != 4 || queue.AgentRunnableCount != 1 || len(queue.Runnable) != 4 || len(queue.AgentRunnable) != 1 {
		t.Fatalf("queue = %+v", queue)
	}
	if got := queue.AgentRunnable[0]; got.Card.ID != "TASK-1" || got.ExecutionMode != "implementation" || !reflect.DeepEqual(got.AllowedPaths, []string{"internal/taskstore/queue_routing.go"}) {
		t.Fatalf("agent item = %+v", got)
	}
	if got := queue.Runnable[0]; got.Card.ID != "ISSUE-3" || got.ExecutionMode != "external" || !got.NeedsHuman || !reflect.DeepEqual(got.AllowedPaths, []string{}) {
		t.Fatalf("P0 issue = %+v", got)
	}
	if got := queue.Runnable[2]; got.Card.ID != "TASK-5" || got.ExecutionMode != "decision" || !got.NeedsHuman || !reflect.DeepEqual(got.AllowedPaths, []string{}) {
		t.Fatalf("decision = %+v", got)
	}
	for _, item := range queue.Runnable {
		if item.Card.ID == "ISSUE-4" || item.Card.ID == "ISSUE-6" {
			t.Fatalf("ineligible issue was admitted: %+v", queue)
		}
	}
}

func TestQueueRejectsInvalidRouteWithoutPartialProjection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"unscoped implementation", "---\nid: TASK-1\ntitle: Unscoped\n---\n"},
		{"tasks subtree", "---\nid: TASK-1\ntitle: Board write\nallowed-paths: [tasks/todo/TASK-1.md]\n---\n"},
		{"absolute path", "---\nid: TASK-1\ntitle: Absolute\nallowed-paths: [/etc/passwd]\n---\n"},
		{"windows volume", "---\nid: TASK-1\ntitle: Windows volume\nallowed-paths: ['C:/Windows']\n---\n"},
		{"parent traversal", "---\nid: TASK-1\ntitle: Traversal\nallowed-paths: [../secret]\n---\n"},
		{"glob", "---\nid: TASK-1\ntitle: Glob\nallowed-paths: ['internal/*.go']\n---\n"},
		{"glob close bracket", "---\nid: TASK-1\ntitle: Glob\nallowed-paths: ['internal/].go']\n---\n"},
		{"backslash", "---\nid: TASK-1\ntitle: Backslash\nallowed-paths: ['internal\\\\file.go']\n---\n"},
		{"whitespace", "---\nid: TASK-1\ntitle: Whitespace\nallowed-paths: ['internal/task store.go']\n---\n"},
		{"external with paths", "---\nid: TASK-1\ntitle: External\nexecution-mode: external\nneeds-human: true\nallowed-paths: [internal/taskstore/store.go]\n---\n"},
		{"decision without human", "---\nid: TASK-1\ntitle: Decide\nexecution-mode: decision\n---\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "todo", "card.md"), []byte(tc.raw), 0o644); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, root)
			projection, err := Queue(root)
			if err == nil || projection.Runnable != nil || projection.AgentRunnable != nil || projection.RunnableCount != 0 || projection.AgentRunnableCount != 0 {
				t.Fatalf("Queue() = %+v, %v", projection, err)
			}
			if !reflect.DeepEqual(before, boardBytes(t, root)) {
				t.Fatal("Queue changed board after rejecting route")
			}
		})
	}
}

func TestActiveIssueStatusIsClosed(t *testing.T) {
	for status, want := range map[string]bool{
		"todo": true, "open": true, "pending": true, "doing": true,
		"in-progress": true, "in_progress": true, "review": true, "blocked": true,
		"done": false, "Done": false, "superseded": false,
		"cancelled": false, "canceled": false, "unknown": false, "": false,
	} {
		if got := activeIssueStatus(status); got != want {
			t.Errorf("activeIssueStatus(%q) = %v, want %v", status, got, want)
		}
	}
}

func TestQueueFailsClosedForMalformedNeedsHuman(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-1\ntitle: Invalid\nneeds-human: \"false\"\n---\n")
	if err := os.WriteFile(filepath.Join(root, "todo", "invalid.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Queue(root); err == nil || !strings.Contains(err.Error(), "needs-human") {
		t.Fatalf("Queue() error = %v, want needs-human parse failure", err)
	}
	if _, err := Ready(root); err == nil || !strings.Contains(err.Error(), "needs-human") {
		t.Fatalf("Ready() error = %v, want same malformed-card failure", err)
	}
}
