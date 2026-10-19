package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
)

func TestIntegrationExecuteReportsUnpublishedObservedOutcome(t *testing.T) {
	for _, code := range []int{0, 4} {
		var out bytes.Buffer
		current := integrationqueue.Request{State: integrationqueue.Running}
		state := integrationqueue.Succeeded
		if code == 4 {
			state = integrationqueue.RevalidationRequired
		}
		err := writeIntegrationExecutionResult(&out, &current, integrationqueue.Outcome{State: state, ExitCode: code, Reason: "provider result"}, errors.New("queue CAS changed"), true)
		var exit interface{ ExitCode() int }
		want := code
		if want == 0 {
			want = 3
		}
		if !errors.As(err, &exit) || exit.ExitCode() != want {
			t.Fatal("lost invocation exit", err)
		}
		var body struct {
			Request  integrationqueue.Request `json:"request"`
			ExitCode int                      `json:"exitCode"`
			Observed integrationqueue.Outcome `json:"observedOutcome"`
			Error    string                   `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Request.State != integrationqueue.Running || body.ExitCode != want || body.Observed.State != state || body.Observed.ExitCode != code || !strings.Contains(body.Error, "CAS changed") {
			t.Fatalf("lost unpublished result %+v", body)
		}
	}
}

func TestIntegrationExecuteMissingInputsDoesNotCreateQueue(t *testing.T) {
	isolateRunEnv(t)
	for _, command := range []string{"execute", "resume"} {
		t.Run(command, func(t *testing.T) {
			queueRoot := filepath.Join(t.TempDir(), "queue")
			var out bytes.Buffer
			err := taskIntegration(context.Background(), []string{command, "--queue-root", queueRoot, "--json"}, &out)
			if err == nil || !strings.Contains(err.Error(), command+" requires --request-id and --revision") {
				t.Fatalf("err=%v", err)
			}
			if _, statErr := os.Stat(queueRoot); !os.IsNotExist(statErr) {
				t.Fatalf("invalid %s created queue: %v", command, statErr)
			}
		})
	}
}

func TestIntegrationExecuteRejectsCapturedTaskMovementBeforeProvider(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "integration-task", "--type", "feat", "--purpose", "delivery"); code != 0 {
		t.Fatal(stderr)
	}
	queueRoot := filepath.Join(t.TempDir(), "queue")
	var out bytes.Buffer
	if err := taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-1", "key-1"), &out); err != nil {
		t.Fatal(err)
	}
	var request integrationqueue.Request
	if err := json.Unmarshal(out.Bytes(), &request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fx.primary), "provider-calls.log"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(filepath.Dir(fx.primary), "bin", "gz-git")
	mustWriteMode(t, provider, providerProbeOnlyScript, 0o755)
	t.Setenv("GZ_CALL_LOG", filepath.Join(filepath.Dir(fx.primary), "provider-calls.log"))
	commit := exec.Command("git", "-c", "user.name=Tester", "-c", "user.email=tester@example.com", "commit", "--allow-empty", "-m", "move captured task")
	commit.Dir = request.Spec.Generation.Worktree
	if output, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("move captured task: %v\n%s", err, output)
	}
	out.Reset()

	var diagnostics bytes.Buffer
	code := run([]string{"task", "integration", "execute", "--queue-root", queueRoot, "--request-id", request.Spec.ID, "--revision", "1", "--json"}, &out, &diagnostics)
	if code != 4 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diagnostics.String())
	}
	var result struct {
		Request  integrationqueue.Request `json:"request"`
		ExitCode int                      `json:"exitCode"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("missing JSON envelope: %v output=%q", err, out.String())
	}
	if result.ExitCode != 4 || result.Request.State != integrationqueue.RevalidationRequired || result.Request.LastExitCode != 4 {
		t.Fatalf("result=%+v", result)
	}
	if calls, err := os.ReadFile(filepath.Join(filepath.Dir(fx.primary), "provider-calls.log")); err != nil || len(calls) != 0 {
		t.Fatalf("provider was called: err=%v calls=%q", err, calls)
	}
	if !strings.Contains(diagnostics.String(), "revalidation") {
		t.Fatalf("diagnostics=%q", diagnostics.String())
	}
}

func TestIntegrationExecuteTerminalRequestIsIdempotentAndOwnerBound(t *testing.T) {
	isolateRunEnv(t)
	fx := newRunFixture(t)
	if _, stderr, code := invoke("run-start", "integration-task", "--type", "feat", "--purpose", "delivery"); code != 0 {
		t.Fatal(stderr)
	}
	queueRoot := filepath.Join(t.TempDir(), "queue")
	var out bytes.Buffer
	if err := taskIntegration(context.Background(), integrationRequestArgs(queueRoot, fx.primary, "request-1", "key-1"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := taskIntegration(context.Background(), []string{"cancel", "--queue-root", queueRoot, "--request-id", "request-1", "--revision", "1", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var cancelled integrationqueue.Request
	if err := json.Unmarshal(out.Bytes(), &cancelled); err != nil {
		t.Fatal(err)
	}
	if cancelled.State != integrationqueue.Cancelled {
		t.Fatalf("cancelled=%+v", cancelled)
	}
	out.Reset()
	if err := taskIntegration(context.Background(), []string{"execute", "--queue-root", queueRoot, "--request-id", "request-1", "--revision", "2", "--json"}, &out); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Request  integrationqueue.Request `json:"request"`
		ExitCode int                      `json:"exitCode"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Request.State != integrationqueue.Cancelled || result.ExitCode != 0 {
		t.Fatalf("terminal execute=%+v", result)
	}
	t.Setenv("CE_TASK_ACTOR", "other")
	if err := taskIntegration(context.Background(), []string{"execute", "--queue-root", queueRoot, "--request-id", "request-1", "--revision", "2", "--json"}, &out); err == nil || !strings.Contains(err.Error(), "only request owner") {
		t.Fatalf("foreign execute err=%v", err)
	}
}

const providerProbeOnlyScript = `#!/bin/bash
set -eu
printf '%s\n' "$*" >> "$GZ_CALL_LOG"
printf 'unexpected provider invocation\n' >&2
exit 99
`
