package taskstore

import (
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func TestModuleDependencyEligibilityDoesNotPromoteKindOrArchive(t *testing.T) {
	t.Parallel()
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"backend", "frontend"}, KindStatus: map[string]string{"plan": "done"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		want bool
	}{
		{"done/TASK-1.md", true},
		{"backend/done/auth/TASK-1.md", true},
		{"backend/plan/auth/TASK-1.md", false},
		{"backend/_archive/done/TASK-1.md", false},
		{"backend/archive/plan/TASK-1.md", false},
		{"backend/todo/done/TASK-1.md", false},
		{"other/done/TASK-1.md", false},
		{"done/category/TASK-1.md", false},
	} {
		dep := Entry{Path: tc.path, Card: card.View{ID: "TASK-1", Status: "done"}}
		if got := entryInWorkflowZone(dep, "done", policy); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.path, got, tc.want)
		}
		work := Entry{Path: "frontend/todo/login/TASK-2.md", Card: card.View{ID: "TASK-2", Status: "pending", DependsOn: []string{"TASK-1"}}}
		ready, err := readyLockedWithPolicy([]Entry{dep, work}, claimsLedger{}, policy)
		if err != nil || (len(ready) == 1) != tc.want || len(ready) > 1 {
			t.Fatalf("%s readiness: %+v, %v (want eligible=%v)", tc.path, ready, err, tc.want)
		}
	}
}
