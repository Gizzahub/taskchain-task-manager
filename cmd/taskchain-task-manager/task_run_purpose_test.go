package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func TestRunStartPurposePersistsAndResumePreservesOmission(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	cases := []struct {
		purpose string
		prefix  string
	}{
		{purpose: "delivery", prefix: "dev"},
		{purpose: "experiment", prefix: "exp"},
		{purpose: "review", prefix: "review"},
	}
	for _, tc := range cases {
		t.Run(tc.purpose, func(t *testing.T) {
			task := "purpose-" + tc.purpose
			stdout, stderr, code := invoke("run-start", task, "--type", "feat", "--purpose", tc.purpose, "--json")
			if code != 0 || stderr != "" {
				t.Fatalf("start code=%d stderr=%q stdout=%s", code, stderr, stdout)
			}
			var response taskruntime.Response
			if err := json.Unmarshal([]byte(stdout), &response); err != nil {
				t.Fatal(err)
			}
			if response.Execution == nil || response.Execution.Purpose != taskruntime.Purpose(tc.purpose) {
				t.Fatalf("execution=%+v", response.Execution)
			}
			wantBranch := tc.prefix + "/tester/testhost/feat/" + task
			if response.Execution.Branch != wantBranch {
				t.Fatalf("branch=%q want=%q", response.Execution.Branch, wantBranch)
			}
			wantPath := strings.ReplaceAll(strings.TrimPrefix(wantBranch, "dev/"), "/", "__")
			if filepath.Base(response.Execution.Worktree) != wantPath {
				t.Fatalf("worktree=%q", response.Execution.Worktree)
			}

			stdout, stderr, code = invoke("run-start", task, "--type", "feat", "--resume", "--json")
			if code != 0 || stderr != "" {
				t.Fatalf("omitted-purpose resume code=%d stderr=%q stdout=%s", code, stderr, stdout)
			}
			if err := json.Unmarshal([]byte(stdout), &response); err != nil {
				t.Fatal(err)
			}
			if response.Execution == nil || response.Execution.Purpose != taskruntime.Purpose(tc.purpose) {
				t.Fatalf("omitted-purpose resume execution=%+v", response.Execution)
			}

			mismatch := "delivery"
			if mismatch == tc.purpose {
				mismatch = "review"
			}
			stdout, stderr, code = invoke("run-start", task, "--type", "feat", "--purpose", mismatch, "--resume", "--json")
			if code != 1 || !strings.Contains(stderr, "requested purpose does not match existing execution purpose") || !strings.Contains(stdout, "requested purpose does not match existing execution purpose") {
				t.Fatalf("mismatch code=%d stderr=%q stdout=%s", code, stderr, stdout)
			}
		})
	}

	var executions []taskruntime.Execution
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(fx.ledger, "executions.json"))), &executions); err != nil {
		t.Fatal(err)
	}
	if len(executions) != len(cases) {
		t.Fatalf("executions=%d", len(executions))
	}
	for _, execution := range executions {
		if taskruntime.EffectivePurpose(execution.Purpose) != execution.Purpose {
			t.Fatalf("execution omitted persisted purpose: %+v", execution)
		}
	}
	receipts := mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl"))
	for _, tc := range cases {
		if !strings.Contains(receipts, `"purpose":"`+tc.purpose+`"`) {
			t.Fatalf("receipt is missing purpose %q: %s", tc.purpose, receipts)
		}
	}
}
