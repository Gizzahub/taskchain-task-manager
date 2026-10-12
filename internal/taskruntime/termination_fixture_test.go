package taskruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	oidBase   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	oidTask   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	oidSource = "cccccccccccccccccccccccccccccccccccccccc"
	oidMoved  = "dddddddddddddddddddddddddddddddddddddddd"
)

type endWorld struct {
	t               *testing.T
	root            string
	common          string
	worktree        string
	branch          string
	execution       Execution
	ancestorExit    int
	containedExit   int
	finishPorcelain string
	derivePorcelain string
	lsRemote        int
	sourceTip       string
	noUpstream      bool
	wtRemoveCode    int
	extraBranch     string
	extraWorktree   string
	oids            map[string]string
	mu              sync.Mutex
	provider        *fakeProvider
}

func newEndWorld(t *testing.T) *endWorld {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "repo")
	wtRoot := filepath.Join(base, "places", "sample-repo")
	common := filepath.Join(base, "common")
	branch := "dev/tester/testhost/feat/sample-task"
	worktree := filepath.Join(wtRoot, "tester__testhost__feat__sample-task")
	for _, dir := range []string{root, worktree, common} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("schema-version: 2\nrepository-id: sample-repo\nworktree-roots:\n  testhost: %s\nintegration-provider: gz-git\nintegration-network-policy: allow\n", wtRoot)
	if err := os.MkdirAll(filepath.Join(root, ".ce"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ConfigFile), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_WORK_HOST", "testhost")
	t.Setenv("CE_TASK_ACTOR", "tester")
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	owner := Owner{Actor: "tester", Host: "testhost", Kind: "agent"}
	execution := Execution{Task: "sample-task", Type: TypeFeat, Source: "master", Owner: owner, Branch: branch, Worktree: worktree, StartedAt: now, UpdatedAt: now}
	w := &endWorld{
		t: t, root: root, common: common, worktree: worktree, branch: branch, execution: execution,
		lsRemote: 2, sourceTip: oidBase,
		oids: map[string]string{
			"refs/heads/" + branch:          oidTask,
			"refs/remotes/origin/" + branch: "",
			"refs/heads/master":             oidSource,
			"refs/remotes/origin/master":    oidSource,
		},
		provider: &fakeProvider{},
	}
	if err := newStateStore(common).save([]Execution{execution}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *endWorld) service() *Service {
	return &Service{
		root: w.root, runner: scriptRunner{fn: w.run}, now: func() time.Time {
			return time.Date(2026, 10, 11, 1, 0, 0, 0, time.UTC)
		},
		provider: w.provider, sourcePollInterval: 5 * time.Millisecond,
	}
}

func (w *endWorld) executions() string {
	w.t.Helper()
	body, err := os.ReadFile(newStateStore(w.common).executionsPath())
	if err != nil {
		w.t.Fatal(err)
	}
	return string(body)
}

func (w *endWorld) receipts() string {
	w.t.Helper()
	body, err := os.ReadFile(newStateStore(w.common).receiptsPath())
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return string(body)
}

func (w *endWorld) run(_ context.Context, req CommandRequest) (CommandResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	res := CommandResult{Command: req.Command, Args: append([]string(nil), req.Args...), WorkDir: req.WorkDir}
	args := req.Args
	switch req.Command {
	case "git":
		switch {
		case sameArgs(args, "rev-parse", "--git-common-dir"):
			res.Stdout = w.common + "\n"
		case sameArgs(args, "branch", "--show-current"):
			res.Stdout = w.branch + "\n"
			if req.WorkDir == w.extraWorktree && w.extraBranch != "" {
				res.Stdout = w.extraBranch + "\n"
			}
		case len(args) >= 2 && args[0] == "status" && args[1] == "--porcelain" && containsArg(args, "--untracked-files=all"):
			res.Stdout = w.derivePorcelain
		case sameArgs(args, "status", "--porcelain"):
			res.Stdout = w.finishPorcelain
		case sameArgs(args, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"):
			if w.noUpstream {
				res.ExitCode, res.Stderr = 1, "no upstream\n"
			} else {
				res.Stdout = "origin/" + w.branch + "\n"
			}
		case sameArgs(args, "rev-list", "--left-right", "--count", "@{u}...HEAD"):
			res.Stdout = "0\t0\n"
		case sameArgs(args, "rev-parse", "HEAD"):
			res.Stdout = oidTask + "\n"
		case sameArgs(args, "rev-parse", "--verify", sourceTrackingRef("master")):
			res.Stdout = w.sourceTip + "\n"
		case sameArgs(args, "merge-base", sourceTrackingRef("master"), "HEAD"):
			res.Stdout = oidBase + "\n"
		case sameArgs(args, "merge-base", "--is-ancestor", sourceTrackingRef("master"), "HEAD"):
			res.ExitCode = w.ancestorExit
		case len(args) == 4 && args[0] == "rev-parse" && args[1] == "--verify" && args[2] == "--quiet" && strings.HasSuffix(args[3], "^{commit}"):
			ref := strings.TrimSuffix(args[3], "^{commit}")
			oid, ok := w.oids[ref]
			if !ok || oid == "" {
				res.ExitCode = 1
			} else {
				res.Stdout = oid + "\n"
			}
		case len(args) >= 2 && args[0] == "merge-base" && args[1] == "--is-ancestor":
			res.ExitCode = w.containedExit
		case len(args) > 0 && args[0] == "show-ref":
			res.ExitCode = 1
		case len(args) > 0 && args[0] == "ls-remote":
			res.ExitCode = w.lsRemote
		case len(args) > 0 && args[0] == "rev-list":
			res.Stdout = "0\n"
		default:
			res.ExitCode, res.Stderr = 1, "unscripted git "+strings.Join(args, " ")
		}
	case "wt":
		if len(args) > 0 && args[0] == "remove" {
			if w.wtRemoveCode != 0 {
				res.ExitCode, res.Stderr = w.wtRemoveCode, "wt remove failed\n"
			} else {
				_ = os.RemoveAll(w.worktree)
				res.Stdout = "{}\n"
			}
		} else {
			items := make([]string, 0, 2)
			if _, err := os.Stat(w.worktree); err == nil {
				items = append(items, fmt.Sprintf(`{"branch":%q,"worktree":{"path":%q}}`, w.branch, w.worktree))
			}
			if w.extraWorktree != "" {
				if _, err := os.Stat(w.extraWorktree); err == nil {
					items = append(items, fmt.Sprintf(`{"branch":%q,"worktree":{"path":%q}}`, w.extraBranch, w.extraWorktree))
				}
			}
			res.Stdout = `{"schema":2,"items":[` + strings.Join(items, ",") + "]}\n"
		}
	default:
		res.ExitCode = 1
	}
	return res, nil
}

func sameArgs(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

type scriptRunner struct {
	fn func(context.Context, CommandRequest) (CommandResult, error)
}

func (r scriptRunner) Run(ctx context.Context, req CommandRequest) (CommandResult, error) {
	return r.fn(ctx, req)
}

type fakeProvider struct {
	checkCode   int
	runCode     int
	runErr      error
	runStdout   string
	checkErr    error
	checkStdout string
	diagnoseErr error
	beforeCheck func()
	onRun       func()
	wait        bool
	checks      int
	runs        int
}

func (f *fakeProvider) Check(ctx context.Context, dir, target string, noFetch bool) (CommandResult, error) {
	f.checks++
	if f.beforeCheck != nil {
		f.beforeCheck()
	}
	result := CommandResult{Command: providerGZGit, Args: integrateArgs("check", target, noFetch), WorkDir: dir, ExitCode: f.checkCode, Stdout: f.checkStdout}
	if f.wait {
		<-ctx.Done()
		result.ExitCode, result.Error = 125, ctx.Err().Error()
		return result, ctx.Err()
	}
	return result, f.checkErr
}

func (f *fakeProvider) Run(_ context.Context, dir, target string, noFetch bool) (CommandResult, error) {
	f.runs++
	if f.onRun != nil {
		f.onRun()
	}
	return CommandResult{Command: providerGZGit, Args: integrateArgs("run", target, noFetch), WorkDir: dir, ExitCode: f.runCode, Stdout: f.runStdout}, f.runErr
}

func (f *fakeProvider) DiagnoseNoFetch(context.Context, string) (ProviderReport, []CommandDiagnostic, error) {
	if f.diagnoseErr != nil {
		return ProviderReport{}, nil, f.diagnoseErr
	}
	return ProviderReport{Name: providerGZGit, Present: true}, nil, nil
}
