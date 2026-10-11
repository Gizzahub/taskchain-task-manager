package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const wtFake = `#!/bin/bash
set -eu
if [[ "${1:-}" == "--version" ]]; then
  printf 'wt 0.80.0\n'
  exit 0
fi
if [[ "${1:-}" == "--config-set" && "${3:-}" == "list" ]]; then
  path=""
  branch=""
  first=1
  emit() {
    if [[ -z "$path" || -z "$branch" ]]; then
      return 0
    fi
    short="${branch#refs/heads/}"
    if [[ "$first" == "0" ]]; then
      printf ','
    fi
    first=0
    printf '{"branch":"%s","worktree":{"path":"%s"}}' "$short" "$path"
  }
  printf '{"schema":2,"items":['
  while IFS= read -r line; do
    case "$line" in
      worktree\ *)
        emit
        path="${line#worktree }"
        branch=""
        ;;
      branch\ *)
        branch="${line#branch }"
        ;;
    esac
  done < <(git worktree list --porcelain)
  emit
  printf ']}\n'
  exit 0
fi
if [[ "${1:-}" == "--config-set" && "${3:-}" == "switch" && "${5:-}" == "--base" && -n "${9:-}" ]]; then
  path=$(printf '%s\n' "$2" | sed -e 's/^worktree-path = "//' -e 's/"$//')
  git worktree add -b "$9" "$path" "$6"
  printf '{}\n'
  exit 0
fi
printf 'unexpected wt arguments\n' >&2
exit 1
`

const gzGitFake = `#!/bin/bash
set -eu
if [[ "${1:-}" == "--version" ]]; then
  printf 'gz-git version 1.2.3\n'
  exit 0
fi
if [[ "${1:-}" == "integrate" && "${2:-}" == "--help" ]]; then
  printf '%s\n' 'Usage:' '  gz-git integrate [command]' '' 'Available Commands:' '  check       Check integration' '  run         Run integration'
  exit 0
fi
if [[ "${1:-}" == "integrate" && ( "${2:-}" == "check" || "${2:-}" == "run" ) && "${3:-}" == "--help" ]]; then
  printf '%s\n' 'Usage:' "  gz-git integrate ${2} [flags]" '' 'Flags:' '      --expect-source' '      --no-fetch' '      --release' '      --target'
  exit 0
fi
printf 'unexpected gz-git arguments\n' >&2
exit 1
`

func isolateRunEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_WORK_HOST", "testhost")
	t.Setenv("CE_TASK_ACTOR", "tester")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	if err := os.WriteFile(filepath.Join(home, "gitconfig"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKS_DIR", "")
	t.Setenv("GIT_AUTHOR_NAME", "Tester")
	t.Setenv("GIT_AUTHOR_EMAIL", "tester@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Tester")
	t.Setenv("GIT_COMMITTER_EMAIL", "tester@example.com")
}

type runFixture struct {
	primary string
	wtRoot  string
	ledger  string
}

func newRunFixture(t *testing.T) runFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(base, "primary")
	wtRoot := filepath.Join(base, "places", "sample-repo")
	if err := os.MkdirAll(primary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wtRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	wtRoot, err = filepath.EvalSymlinks(wtRoot)
	if err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git(primary, "init", "-q", "-b", "master")
	git(primary, "config", "user.email", "tester@example.com")
	git(primary, "config", "user.name", "Tester")
	mustWrite(t, filepath.Join(primary, ".gitignore"), ".ce/\n")
	mustWrite(t, filepath.Join(primary, ".gz-git.yaml"), "branch:\n  integrationBranch: master\n")
	git(primary, "add", ".gitignore", ".gz-git.yaml")
	git(primary, "commit", "-qm", "declare runtime")
	git(primary, "remote", "add", "origin", primary)
	git(primary, "update-ref", "refs/remotes/origin/master", "HEAD")
	mustWrite(t, filepath.Join(primary, ".ce", "task-runtime.yaml"), fmt.Sprintf("schema-version: 2\nrepository-id: sample-repo\nworktree-roots:\n  testhost: %s\nintegration-provider: gz-git\nintegration-network-policy: allow\n", wtRoot))
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMode(t, filepath.Join(bin, "wt"), wtFake, 0o755)
	mustWriteMode(t, filepath.Join(bin, "gz-git"), gzGitFake, 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(primary)
	return runFixture{primary: primary, wtRoot: wtRoot, ledger: ledgerDir(t, primary)}
}

func ledgerDir(t *testing.T, primary string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--git-common-dir")
	cmd.Dir = primary
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(primary, path)
	}
	return filepath.Join(path, "ce", "task-runtime", "v1")
}

func invoke(args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"task"}, args...), &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	mustWriteMode(t, path, body, 0o644)
}

func mustWriteMode(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func lockHostForTest(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(host), "_")
}

func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("pid %d is still running", pid)
	}
	return pid
}
