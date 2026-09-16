package cardid

import "testing"

func TestIdentityPreservesNamespaceAndNormalizesNumber(t *testing.T) {
	for raw, want := range map[string]string{"TASK-090": "TASK-90", "TASK-000": "TASK-0", "TASK-90": "TASK-90", "PLAN-090": "PLAN-90", "ISSUE-007": "ISSUE-7", "BACKLOG-0008": "BACKLOG-8", "TASK-18446744073709551615": "TASK-18446744073709551615"} {
		id, err := Parse(raw)
		if err != nil || id.Key() != want {
			t.Fatalf("%s: %v %v", raw, id, err)
		}
	}
	for _, raw := range []string{"task-1", "OTHER-1", "TASK-", "TASK--1", "TASK-+1", " TASK-1", "TASK-1 ", "TASK-1-2", "TASK-１", "TASK-18446744073709551616"} {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("invalid ID accepted: %q", raw)
		}
	}
}
