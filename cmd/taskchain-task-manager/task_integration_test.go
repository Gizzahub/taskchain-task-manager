package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

func TestIntegrationRequestStatusCancelUsesRuntimeGeneration(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "integration-task", "--type", "feat", "--purpose", "delivery"); code != 0 {
		t.Fatalf("start stderr=%s", stderr)
	}
	queueRoot := filepath.Join(t.TempDir(), "queue")
	var out bytes.Buffer
	args := []string{
		"request", "--queue-root", queueRoot, "--repo", fx.primary, "--repo-id", "sample-repo",
		"--task", "INTEGRATION_TASK", "--idempotency-key", "same-request", "--request-id", "request-1", "--json",
	}
	err := taskIntegration(context.Background(), args, &out)
	if err != nil {
		t.Fatal(err)
	}
	var request integrationqueue.Request
	if err := json.Unmarshal(out.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.State != integrationqueue.Queued || request.Sequence != 1 || request.Spec.Generation.Task != "integration-task" || request.Spec.Generation.Purpose != "delivery" || request.Spec.Resource != "host/testhost/integration" || request.Spec.GateProfile != "repository-default" {
		t.Fatalf("request=%+v", request)
	}
	wantTask := integrationTestGit(t, fx.primary, "rev-parse", "refs/heads/dev/tester/testhost/feat/integration-task^{commit}")
	wantSource := integrationTestGit(t, fx.primary, "rev-parse", "refs/remotes/origin/master^{commit}")
	if request.Spec.ExpectedTaskSHA != wantTask || request.Spec.ExpectedSourceSHA != wantSource || request.Spec.CommonDir == "" {
		t.Fatalf("missing Git binding: %+v", request.Spec)
	}

	out.Reset()
	err = taskIntegration(context.Background(), []string{"status", "--queue-root", queueRoot, "--request-id", "request-1", "--json"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	var view integrationqueue.View
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Request.Revision != 1 || view.CoordinatorPosition == nil || *view.CoordinatorPosition != 1 || view.ProviderPosition != "UNKNOWN" {
		t.Fatalf("view=%+v", view)
	}

	out.Reset()
	err = taskIntegration(context.Background(), []string{"cancel", "--queue-root", queueRoot, "--request-id", "request-1", "--revision", "1", "--json"}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if request.State != integrationqueue.Cancelled || request.Revision != 2 {
		t.Fatalf("cancel=%+v", request)
	}
}

func integrationTestGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func TestIntegrationRequestRefusesBadInputsBeforeQueueWrite(t *testing.T) {
	cases := []struct {
		name string
		args func(string) []string
		want string
	}{
		{"missing root", func(string) []string { return []string{"status"} }, "--queue-root is required"},
		{"relative root", func(string) []string { return []string{"status", "--queue-root", "queue"} }, "--queue-root must be an absolute path"},
		{"bad command", func(root string) []string { return []string{"missing", "--queue-root", root} }, "unknown integration command"},
		{"cancel revision", func(root string) []string { return []string{"cancel", "--queue-root", root, "--request-id", "r"} }, "cancel requires --request-id and --revision"},
		{"request missing", func(root string) []string { return []string{"request", "--queue-root", root} }, "request requires --repo"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			queueRoot := filepath.Join(t.TempDir(), "queue")
			var out bytes.Buffer
			err := taskIntegration(context.Background(), tc.args(queueRoot), &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want=%q", err, tc.want)
			}
			if _, err := os.Stat(queueRoot); !os.IsNotExist(err) {
				t.Fatalf("invalid input created queue root: %v", err)
			}
		})
	}
}

func TestIntegrationStatusAbsentQueueDoesNotCreateIt(t *testing.T) {
	queueRoot := filepath.Join(t.TempDir(), "absent")
	var out, diag bytes.Buffer
	code := runTask([]string{"integration", "status", "--queue-root", queueRoot, "--json"}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "integration queue") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	if _, err := os.Stat(queueRoot); !os.IsNotExist(err) {
		t.Fatalf("status created queue root: %v", err)
	}
}

func TestIntegrationRequestOwnershipAndPurposeRefusalsDoNotCreateQueue(t *testing.T) {
	cases := []struct {
		name   string
		start  []string
		mutate func(t *testing.T, fx runFixture)
		owner  string
		want   string
	}{
		{"owner", []string{"run-start", "integration-task", "--type", "feat"}, nil, "other", "only the recorded execution owner"},
		{"experiment", []string{"run-start", "integration-task", "--type", "feat", "--purpose", "experiment"}, nil, "tester", "valid delivery execution"},
		{"delivery prefix", []string{"run-start", "integration-task", "--type", "feat", "--purpose", "experiment"}, func(t *testing.T, fx runFixture) {
			path := filepath.Join(fx.ledger, "executions.json")
			body := mustRead(t, path)
			mustWrite(t, path, strings.Replace(body, `"purpose": "experiment"`, `"purpose": "delivery"`, 1))
		}, "tester", "valid delivery execution"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunEnv(t)
			fx := newRunFixture(t)
			if _, stderr, code := invoke(tc.start...); code != 0 {
				t.Fatal(stderr)
			}
			if tc.mutate != nil {
				tc.mutate(t, fx)
			}
			t.Setenv("CE_TASK_ACTOR", tc.owner)
			queueRoot := filepath.Join(t.TempDir(), "queue")
			var out bytes.Buffer
			err := taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-1", "key-1"), &out)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want=%q", err, tc.want)
			}
			if _, err := os.Stat(queueRoot); !os.IsNotExist(err) {
				t.Fatalf("refusal created queue: %v", err)
			}
		})
	}
}

func TestIntegrationRequestBoundReceiptAndRefGuards(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(t *testing.T, fx runFixture)
		wantErr   string
		wantQueue bool
	}{
		{"unbound terminal ignored", func(t *testing.T, fx runFixture) { appendIntegrationReceipt(t, fx, "ABORTED", "other-worktree") }, "", true},
		{"bound terminal", func(t *testing.T, fx runFixture) { appendIntegrationReceipt(t, fx, "ABORTED", "") }, "terminal or finishing", false},
		{"bound finishing", func(t *testing.T, fx runFixture) { appendIntegrationReceipt(t, fx, "FINISHING", "") }, "terminal or finishing", false},
		{"missing task ref", func(t *testing.T, fx runFixture) {
			var executions []taskruntime.Execution
			if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(fx.ledger, "executions.json"))), &executions); err != nil {
				t.Fatal(err)
			}
			integrationTestGit(t, fx.primary, "update-ref", "-d", "refs/heads/"+executions[0].Branch)
		}, "resolve task branch", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunEnv(t)
			fx := newRunFixture(t)
			if _, stderr, code := invoke("run-start", "integration-task", "--type", "feat"); code != 0 {
				t.Fatal(stderr)
			}
			tc.mutate(t, fx)
			queueRoot := filepath.Join(t.TempDir(), "queue")
			var out bytes.Buffer
			err := taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-1", "key-1"), &out)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err=%v want=%q", err, tc.wantErr)
			}
			_, statErr := os.Stat(queueRoot)
			if tc.wantQueue != !os.IsNotExist(statErr) {
				t.Fatalf("queue exists=%t err=%v", !os.IsNotExist(statErr), statErr)
			}
		})
	}
}

func TestIntegrationCancelOwnerAndChangedSHAIdempotency(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "integration-task", "--type", "feat"); code != 0 {
		t.Fatal(stderr)
	}
	queueRoot := filepath.Join(t.TempDir(), "queue")
	var out bytes.Buffer
	if err := taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-1", "same-key"), &out); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CE_TASK_ACTOR", "other")
	out.Reset()
	err := taskIntegration(context.Background(), []string{"cancel", "--queue-root", queueRoot, "--request-id", "request-1", "--revision", "1"}, &out)
	if err == nil || !strings.Contains(err.Error(), "only request owner") {
		t.Fatalf("cancel err=%v", err)
	}
	t.Setenv("CE_TASK_ACTOR", "tester")
	var executions []taskruntime.Execution
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(fx.ledger, "executions.json"))), &executions); err != nil {
		t.Fatal(err)
	}
	integrationTestGit(t, executions[0].Worktree, "commit", "--allow-empty", "-m", "advance task")
	out.Reset()
	err = taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-2", "same-key"), &out)
	if err == nil || !strings.Contains(err.Error(), "conflicts with immutable request") {
		t.Fatalf("dedup err=%v", err)
	}
	store, err := integrationqueue.Open(queueRoot)
	if err != nil {
		t.Fatal(err)
	}
	view, err := store.Get("request-1")
	if err != nil {
		t.Fatal(err)
	}
	if view.Request.Spec.ExpectedTaskSHA == integrationTestGit(t, executions[0].Worktree, "rev-parse", "HEAD") {
		t.Fatal("changed SHA replaced original request")
	}
}

func integrationRequestArgs(queueRoot, repo, requestID, key string) []string {
	return []string{"request", "--queue-root", queueRoot, "--repo", repo, "--repo-id", "sample-repo", "--task", "integration-task", "--idempotency-key", key, "--request-id", requestID, "--json"}
}

func appendIntegrationReceipt(t *testing.T, fx runFixture, status, worktree string) {
	t.Helper()
	var executions []taskruntime.Execution
	if err := json.Unmarshal([]byte(mustRead(t, filepath.Join(fx.ledger, "executions.json"))), &executions); err != nil {
		t.Fatal(err)
	}
	e := executions[0]
	if worktree == "" {
		worktree = e.Worktree
	}
	receipt := taskruntime.Receipt{Task: e.Task, Purpose: taskruntime.PurposeDelivery, Operation: "abort", Status: status, Owner: e.Owner, Branch: e.Branch, Worktree: worktree, CreatedAt: e.StartedAt.Add(time.Second)}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fx.ledger, "receipts.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
