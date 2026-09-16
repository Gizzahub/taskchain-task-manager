package taskstore

import (
	"reflect"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func TestDefaultPolicyPreservesStoreDirectories(t *testing.T) {
	want := []string{"todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog", "archive", "_archive"}
	if got := currentPolicy().KnownDirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("default store directories=%v want=%v", got, want)
	}
	for _, zone := range want {
		if !containsKnownDir(zone) {
			t.Errorf("missing known directory %s", zone)
		}
	}
}

func TestPolicyClassificationCannotGrantWorkflowCompletion(t *testing.T) {
	p, err := boardpolicy.New(boardpolicy.Declaration{Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "done"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Entry{
		{Path: "manual/TASK-1.md", Card: card.View{ID: "TASK-1", Status: "done"}},
		{Path: "archive/done/TASK-1.md", Card: card.View{ID: "TASK-1", Status: "done"}},
		{Path: "done/PLAN-1.md", Card: card.View{ID: "PLAN-1", Status: "done"}},
		{Path: "done/TASK-1.md", Card: card.View{ID: "TASK-1", Status: "pending"}},
	} {
		if entryInWorkflowZone(entry, p.DoneZone(), p) || entryInWorkflowZone(entry, "manual", p) {
			t.Errorf("non-completed workflow card accepted: %+v", entry)
		}
	}
	if !entryInWorkflowZone(Entry{Path: "done/custom.md", Card: card.View{ID: "TASK-001", Status: "done"}}, p.DoneZone(), p) {
		t.Fatal("valid completed workflow card rejected")
	}
	// A constructed policy is not global runtime activation.
	if containsKnownDir("manual") || validZone("manual") || allowedEdge("manual", "todo") {
		t.Fatal("unactivated policy leaked into default store")
	}
}
