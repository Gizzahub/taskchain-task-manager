package taskstore

import (
	"bytes"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestRelocationPreparationPreservesIdentityAndScopedPath(t *testing.T) {
	p, err := boardpolicy.New(boardpolicy.Declaration{Relocations: []boardpolicy.Transition{{From: "plan", To: []string{"todo", "issue"}}}})
	if err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: PLAN-001\ntitle: Example\nkind: plan\nstatus: custom\n---\n| **Status** | [x] Done |\n")
	for _, scope := range []string{"", "module/"} {
		source := scope + "plan/category/custom.md"
		patched, changed, err := prepareRelocationPatch(raw, "PLAN-1", source, scope+"todo/category/custom.md", p)
		if err != nil || !changed || !bytes.Contains(patched, []byte("[ ] Pending")) || !bytes.Contains(patched, []byte("id: PLAN-001\ntitle: Example\nkind: plan\nstatus: custom")) {
			t.Fatalf("workflow patch=%q changed=%v err=%v", patched, changed, err)
		}
		patched, changed, err = prepareRelocationPatch(raw, "PLAN-1", source, scope+"issue/category/custom.md", p)
		if err != nil || changed || !bytes.Equal(raw, patched) {
			t.Fatalf("unmapped kind patch changed bytes: %v", err)
		}
	}
	for _, target := range []string{"todo/other/custom.md", "other/plan/category/custom.md", "todo/category/renamed.md", "../todo/category/custom.md", "archive/category/custom.md", "plan/category/custom.md"} {
		if _, _, err := prepareRelocationPatch(raw, "PLAN-1", "plan/category/custom.md", target, p); err == nil {
			t.Fatalf("invalid target accepted: %s", target)
		}
	}
	if _, _, err := prepareRelocationPatch(raw, "PLAN-2", "plan/category/custom.md", "todo/category/custom.md", p); err == nil {
		t.Fatal("wrong identity accepted")
	}
	for _, pair := range [][2]string{
		{"todos/plan/custom.md", "todos/todo/custom.md"},
		{"plan/done/custom.md", "todo/done/custom.md"},
		{"plan/WIP/custom.md", "todo/WIP/custom.md"},
	} {
		if _, _, err := prepareRelocationPatch(raw, "PLAN-1", pair[0], pair[1], p); err == nil {
			t.Fatalf("ambiguous zone accepted: %v", pair)
		}
	}
}
