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
		{name: "agent.md", raw: "---\nid: TASK-1\ntitle: Agent\n---\n"},
		{name: "human.md", raw: "---\nid: TASK-2\ntitle: Human\nneeds-human: true\n---\n"},
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
