package taskruntime

import (
	"context"
	"strings"
	"testing"
	"time"
)

type recordRunner struct {
	reqs []CommandRequest
}

func (r *recordRunner) Run(_ context.Context, req CommandRequest) (CommandResult, error) {
	r.reqs = append(r.reqs, req)
	return CommandResult{Command: req.Command, Args: append([]string(nil), req.Args...), ExitCode: 0}, nil
}

func TestGZGitIntegrateArguments(t *testing.T) {
	runner := &recordRunner{}
	provider := gzGitProvider{runner: runner}
	if _, err := provider.Check(context.Background(), "/work", "origin/master", true); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Run(context.Background(), "/work", "origin/master", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.reqs) != 2 {
		t.Fatalf("calls=%d", len(runner.reqs))
	}
	if !runner.reqs[0].ProcessGroup || runner.reqs[1].ProcessGroup {
		t.Fatalf("process groups check=%v run=%v", runner.reqs[0].ProcessGroup, runner.reqs[1].ProcessGroup)
	}
	if strings.Join(runner.reqs[0].Args, " ") != "integrate check --target origin/master --no-fetch" {
		t.Fatalf("check args=%v", runner.reqs[0].Args)
	}
	if strings.Join(runner.reqs[1].Args, " ") != "integrate run --target origin/master" {
		t.Fatalf("run args=%v", runner.reqs[1].Args)
	}
	if runner.reqs[0].Command != providerGZGit || runner.reqs[0].WorkDir != "/work" || runner.reqs[1].WorkDir != "/work" {
		t.Fatalf("requests=%+v", runner.reqs)
	}
}

func TestProcessGroupCancelStopsTheChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NewExecutor().Run(ctx, CommandRequest{Command: "sh", Args: []string{"-c", "sleep 30"}, ProcessGroup: true})
	if err == nil {
		t.Fatal("cancelled command returned success")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatalf("cancellation took %s: %v", time.Since(started), err)
	}
}
