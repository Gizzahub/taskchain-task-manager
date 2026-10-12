package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func TestRunRecoverClaimDeadDispatchesOwnerDeclaration(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "sample-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	var executions []taskruntime.Execution
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(fx.ledger, "executions.json"))), &executions); err != nil {
		t.Fatal(err)
	}
	e := executions[0]
	head, err := exec.Command("git", "-C", e.Worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	claim := taskruntime.Receipt{Task: e.Task, Operation: "finish", Status: taskruntime.StatusFinishing, Owner: e.Owner, Branch: e.Branch, Worktree: e.Worktree, CreatedAt: time.Now().UTC(), FinishPID: 99, FinishHost: "foreign-host", TaskHead: strings.TrimSpace(string(head))}
	raw, err := json.Marshal(claim)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(fx.ledger, "receipts.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	before := mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl"))
	resumed, diagnostic, code := invoke("run-start", "sample-task", "--type", "feat", "--resume", "--json")
	if code != 1 || !strings.Contains(diagnostic, "FINISHING") || !strings.Contains(resumed, "FINISHING") {
		t.Fatalf("claimed resume %d %s %s", code, resumed, diagnostic)
	}
	if mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl")) != before {
		t.Fatal("resume altered finishing claim")
	}
	if _, _, code := invoke("run-recover", "sample-task", "--json"); code != 1 {
		t.Fatalf("foreign claim recovered without declaration: %d", code)
	}
	if mustRead(t, filepath.Join(fx.ledger, "receipts.jsonl")) != before {
		t.Fatal("undeclared foreign claim changed")
	}
	stdout, stderr, code := invoke("run-recover", "sample-task", "--claim-dead", "--json")
	if code != 0 || stderr != "" {
		t.Fatalf("code %d stdout %s stderr %s", code, stdout, stderr)
	}
	var result taskruntime.Response
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatal(err)
	}
	if result.Receipt == nil || result.Receipt.Operation != "recover" || result.Receipt.Status == taskruntime.StatusDone || result.Receipt.PerformedBy == nil || *result.Receipt.PerformedBy != e.Owner {
		t.Fatalf("invalid declaration receipt %+v", result)
	}
}
