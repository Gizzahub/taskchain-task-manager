package taskruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
)

// Opt in with an explicitly selected installed binary. Ordinary unit tests do
// not require gz-git or workstation credentials. Every remote here is local.
func TestIntegrationRequestNativeRecovery(t *testing.T) {
	bin := os.Getenv("TM_TEST_GZ_GIT")
	if bin == "" {
		t.Skip("set TM_TEST_GZ_GIT to an exact compatible gz-git binary")
	}
	if !filepath.IsAbs(bin) {
		t.Fatal("native binary must be absolute")
	}
	cases := []struct {
		name       string
		partial    bool
		targetMove bool
	}{
		{name: "normal"},
		{name: "partial", partial: true},
		{name: "target-moved", targetMove: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			primary, remote := filepath.Join(root, "primary"), filepath.Join(root, "remote.git")
			wtroot := filepath.Join(root, "worktrees", "sample-repo")
			wt := filepath.Join(wtroot, "tester__testhost__feat__sample-task")
			branch := "dev/tester/testhost/feat/sample-task"
			write := func(p, body string, mode os.FileMode) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			for k, v := range map[string]string{"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_SYSTEM": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1", "GIT_WORK_HOST": "testhost", "CE_TASK_ACTOR": "tester", "GIT_WORK_ACTOR": "conflicting-human", "GZ_GIT_TASK_ACTOR": "conflicting-agent", "XDG_CONFIG_HOME": filepath.Join(root, "config"), "FIXTURE_COMMON": filepath.Join(primary, ".git"), "FIXTURE_REMOTE": remote, "FIXTURE_WORKTREE": wt, "FIXTURE_ROOT": root, "TM_NATIVE_GZ": bin} {
				t.Setenv(k, v)
			}
			git := func(dir string, args ...string) string {
				t.Helper()
				cmd := exec.Command("git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return strings.TrimSpace(string(out))
			}
			git(root, "init", "--bare", "--initial-branch=main", remote)
			git(root, "clone", remote, primary)
			git(primary, "config", "user.name", "Synthetic test")
			git(primary, "config", "user.email", "fixture@example.invalid")
			write(filepath.Join(primary, ".gitignore"), ".ce/\n", 0644)
			write(filepath.Join(primary, "baseline.txt"), "baseline\n", 0644)
			write(filepath.Join(primary, ".gz-git.yaml"), "version: 1\nbranch:\n  defaultBranch: main\n  integrationBranch: master\n  taskPattern: [\"dev/*/*/*\"]\n  readiness:\n    version: 1\n    runner: .gz-git/readiness/check\n", 0644)
			write(filepath.Join(primary, ".gz-git/readiness/check"), `#!/bin/bash
set -euo pipefail
[[ $# == 8 && "$1" == --source-dir && "$3" == --source-sha ]]
[[ "$(git -C "$2" rev-parse HEAD)" == "$4" ]]
[[ "$(cat "$2/task.txt")" == payload ]]
printf 'gate\n' >> "$FIXTURE_ROOT/gate.log"
printf '{"version":1,"status":"ready","summary":"synthetic_payload_verified"}\n'
`, 0700)
			git(primary, "add", ".")
			git(primary, "commit", "-m", "fixture baseline\n\nModel: GPT-6")
			base := git(primary, "rev-parse", "HEAD")
			git(primary, "push", "origin", "HEAD:refs/heads/main", "HEAD:refs/heads/master")
			git(primary, "checkout", "-b", "master", "--track", "origin/master")
			git(primary, "worktree", "add", "-b", branch, wt, "master")
			write(filepath.Join(wt, "task.txt"), "payload\n", 0644)
			git(wt, "add", "task.txt")
			git(wt, "commit", "-m", "fixture payload\n\nModel: GPT-6")
			taskSHA := git(wt, "rev-parse", "HEAD")
			git(wt, "push", "-u", "origin", branch)
			write(filepath.Join(primary, ConfigFile), fmt.Sprintf("schema-version: 2\nrepository-id: sample-repo\nworktree-roots:\n  testhost: %s\nintegration-provider: gz-git\nintegration-network-policy: allow\n", wtroot), 0644)
			common := filepath.Join(primary, ".git")
			e := Execution{Task: "sample-task", Type: "feat", Purpose: PurposeDelivery, Source: "master", Owner: Owner{Actor: "tester", Host: "testhost", Kind: "agent"}, Branch: branch, Worktree: wt, StartedAt: time.Now().UTC()}
			if err := newStateStore(common).save([]Execution{e}); err != nil {
				t.Fatal(err)
			}
			wrapper := filepath.Join(root, "tools", "gz-git")
			wrapperBody := `#!/bin/bash
set -euo pipefail
printf '%s\n' "$*" >> "$FIXTURE_ROOT/provider.log"
if [[ "${1:-}" == integrate && "${2:-}" == run && "${3:-}" != --help && "${TARGET_MOVE_ONCE:-0}" == 1 && ! -e "$FIXTURE_ROOT/target-moved" ]]; then
  tree=$(git --git-dir="$FIXTURE_REMOTE" rev-parse refs/heads/master^{tree})
  parent=$(git --git-dir="$FIXTURE_REMOTE" rev-parse refs/heads/master)
  moved=$(printf 'synthetic target movement\n' | git --git-dir="$FIXTURE_REMOTE" commit-tree "$tree" -p "$parent")
  git --git-dir="$FIXTURE_REMOTE" update-ref refs/heads/master "$moved" "$parent"
  touch "$FIXTURE_ROOT/target-moved"
fi
exec "$TM_NATIVE_GZ" "$@"
`

			write(wrapper, wrapperBody, 0700)
			t.Setenv("PATH", filepath.Dir(wrapper)+string(os.PathListSeparator)+os.Getenv("PATH"))
			if tc.targetMove {
				t.Setenv("TARGET_MOVE_ONCE", "1")
			}
			if tc.partial {
				write(filepath.Join(remote, "hooks/post-receive"), `#!/bin/bash
set -euo pipefail
while read -r old new ref; do
  if [[ "$ref" == refs/heads/master ]]; then
    git --git-dir="$FIXTURE_COMMON" worktree lock "$FIXTURE_WORKTREE" --reason synthetic-after-push
  fi
done
`, 0700)
			}
			o := integrationqueue.Owner{Actor: "tester", Host: "testhost", Kind: "agent"}
			r := integrationqueue.Request{Spec: integrationqueue.Spec{ID: "request-native", IdempotencyKey: "native", RepositoryID: "sample-repo", RepositoryPath: primary, CommonDir: common, Source: "master", Target: "origin/master", ExpectedTaskSHA: taskSHA, ExpectedSourceSHA: base, GateProfile: "repository-default", Resource: "synthetic", Owner: o, Generation: integrationqueue.Generation{Task: e.Task, Type: e.Type, Purpose: "delivery", Source: e.Source, Owner: o, Branch: branch, Worktree: wt, StartedAt: e.StartedAt}}}
			svc := NewService(primary, NewExecutor())
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			out := svc.Execute(ctx, r)
			if tc.partial {
				if out.State != integrationqueue.Running || out.ExitCode != 3 {
					t.Fatalf("partial %+v", out)
				}
				git(primary, "worktree", "unlock", wt)
				out = svc.Reconcile(ctx, r)
			}
			if tc.targetMove {
				if out.State != integrationqueue.RevalidationRequired || out.ExitCode != 4 {
					t.Fatalf("target movement outcome %+v", out)
				}
				receipts, err := newStateStore(common).latestReceipts()
				if err != nil {
					t.Fatal(err)
				}
				last := receipts[e.Task]
				nativeExit4 := false
				for _, diagnostic := range last.Diagnostics {
					if diagnostic.Command == "gz-git" && strings.Contains(strings.Join(diagnostic.Args, " "), "integrate run") && diagnostic.ExitCode == 4 {
						nativeExit4 = true
					}
				}
				if !requestReceiptMatches(last, newRequestBinding(r.Spec)) || last.Status != StatusBlocked || !strings.Contains(last.Reason, "revalidation") || !nativeExit4 {
					data, _ := json.Marshal(last)
					t.Fatal("revalidation receipt", string(data))
				}
				if got := git(root, "--git-dir="+remote, "rev-parse", "refs/heads/master"); got == taskSHA || got == base {
					t.Fatal("target movement did not leave a distinct remote commit", got)
				}
				if _, err := os.Stat(wt); err != nil {
					t.Fatal("target movement removed worktree", err)
				}
				if !strings.Contains(git(root, "--git-dir="+remote, "show-ref"), branch) {
					t.Fatal("target movement removed remote task branch")
				}
				log, err := os.ReadFile(filepath.Join(root, "provider.log"))
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(string(log), "\n")
				runs := 0
				var runLine string
				for _, line := range lines {
					if strings.HasPrefix(line, "integrate run ") && !strings.HasSuffix(line, "--help") {
						runs++
						runLine = line
					}
				}
				if runs != 1 || !strings.Contains(runLine, "--expect-source "+taskSHA) || !strings.Contains(runLine, "--expect-target "+base) {
					t.Fatalf("native run args/runs=%d line=%q log=%q", runs, runLine, log)
				}
				gate, err := os.ReadFile(filepath.Join(root, "gate.log"))
				if err == nil && string(gate) != "gate\n" {
					t.Fatal("gate repeated", string(gate))
				}
				if err != nil && !os.IsNotExist(err) {
					t.Fatal("read gate evidence", err)
				}
				t.Logf("actual native target-moved run=1 gate=%d source=%s receipt=%s", strings.Count(string(gate), "gate\n"), taskSHA, last.NativeExecutionID)
				return
			}
			if out.State != integrationqueue.Succeeded || out.ExitCode != 0 {
				t.Fatalf("outcome %+v", out)
			}
			if got := git(root, "--git-dir="+remote, "rev-parse", "refs/heads/master"); got != taskSHA {
				t.Fatal("wrong pushed target", got)
			}
			if _, err := os.Stat(wt); !os.IsNotExist(err) {
				t.Fatal("worktree remains", err)
			}
			if strings.Contains(git(root, "--git-dir="+remote, "show-ref"), branch) {
				t.Fatal("remote task branch remains")
			}
			log, err := os.ReadFile(filepath.Join(root, "provider.log"))
			if err != nil {
				t.Fatal(err)
			}
			runs := 0
			for _, line := range strings.Split(string(log), "\n") {
				if strings.HasPrefix(line, "integrate run ") && !strings.HasSuffix(line, "--help") {
					runs++
				}
			}
			if runs != 1 {
				t.Fatal("integration repeated", string(log))
			}
			gate, err := os.ReadFile(filepath.Join(root, "gate.log"))
			if err != nil || string(gate) != "gate\n" {
				t.Fatal("gate repeated/missing", err, string(gate))
			}
			receipts, err := newStateStore(common).latestReceipts()
			if err != nil {
				t.Fatal(err)
			}
			last := receipts[e.Task]
			if !requestReceiptMatches(last, newRequestBinding(r.Spec)) || last.Status != StatusDone || last.SourceHead != taskSHA {
				data, _ := json.Marshal(last)
				t.Fatal("terminal binding", string(data))
			}
			t.Logf("actual native partial=%t run=1 gate=1 source=%s receipt=%s", tc.partial, taskSHA, last.NativeExecutionID)
		})
	}
}
