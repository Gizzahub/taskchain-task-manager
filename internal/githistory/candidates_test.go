package githistory

import "testing"

func TestCandidateIDsConservativeLinesAndNormalization(t *testing.T) {
	raw := []byte("id: TASK-002 # padded\r\nid: 'PLAN-7'\nid: ISSUE-3\nid: BACKLOG-0004\n\nid: TASK-abc\ntext: id: TASK-99\n```\nid: TASK-8\n```\n")
	got, err := CandidateIDs(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"BACKLOG-4", "ISSUE-3", "PLAN-7", "TASK-2", "TASK-8"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestCandidateIDsOverflowOnlyKnownDecimal(t *testing.T) {
	if _, err := CandidateIDs([]byte("id: TASK-18446744073709551616\n")); err == nil {
		t.Fatal("known overflow ignored")
	}
	if got, err := CandidateIDs([]byte("id: TASK-1x\nid: OTHER-18446744073709551616\n")); err != nil || len(got) != 0 {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

func TestIsCardPathBoundedPredicates(t *testing.T) {
	cases := map[string]bool{
		"tasks/todo/TASK-1.md": true, "tasks/workflow/custom.md": true, "tasks/evidence/log.md": false,
		"tasks/.ce/state.md": false, "tasks/_archive/TASK-1.md": true, "tasks/archive/TASK-2.md": true, "tasks/README.md": false,
		"tasks/TEMPLATE.md": false, "tasks2/todo/TASK-1.md": false, "tasks/TASK-1.txt": false,
		"tasks/todo/README.md": false,
	}
	for path, want := range cases {
		if got := IsCardPath(path, "tasks"); got != want {
			t.Errorf("%s=%v want %v", path, got, want)
		}
	}
	if !IsCardPath("evidence/tasks/todo/odd\t\"name.md", "evidence/tasks") {
		t.Fatal("board prefix or unusual filename excluded")
	}
}
