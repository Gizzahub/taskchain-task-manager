package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOut(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func configureSource(t *testing.T) string {
	t.Helper()
	branch := gitOut(t, "branch", "--show-current")
	if branch == "" {
		t.Fatal("fixture has no branch")
	}
	gitOut(t, "config", "ce.integrationBranch", branch)
	return branch
}

func commitAll(t *testing.T, message string) {
	t.Helper()
	gitOut(t, "add", "-A")
	gitOut(t, "commit", "-qm", message)
}

func worktreeFiles(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := filepath.ToSlash(path)
		if rel == ".git" || strings.HasPrefix(rel, ".git/") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSameFiles(t *testing.T, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("file set changed: before=%d after=%d", len(before), len(after))
	}
	for path, body := range before {
		if after[path] != body {
			t.Fatalf("bytes changed: %s", path)
		}
	}
}

func assertNoMutationLock(t *testing.T) {
	t.Helper()
	path := filepath.Join(".git", "ce", "task-runtime", "v1", "mutation.lock")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("mutation lock remains: %v", err)
	}
}

func writeRuntimeFile(t *testing.T, name, body string) {
	t.Helper()
	dir := filepath.Join(".git", "ce", "task-runtime", "v1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBoardRuntimeRefusals(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		stderr string
	}{
		{name: "cleanup flag", args: []string{"cleanup", "--bogus"}, stderr: "unknown flag: --bogus"},
		{name: "cleanup apply empty", args: []string{"cleanup", "--apply"}, stderr: "cleanup --apply requires explicit card IDs/paths or --all"},
		{name: "cleanup mixed selection", args: []string{"cleanup", "--all", "TASK-1"}, stderr: "choose --all or explicit cards"},
		{name: "finalize-all card", args: []string{"done-finalize-all", "TASK-1"}, stderr: "ce task done-finalize-all takes no card arguments; use ce task done-finalize for one card"},
		{name: "finalize-all flag", args: []string{"done-finalize-all", "--apply"}, stderr: "unknown flag: --apply (valid: --dry-run, --json, --help, -h)"},
		{name: "reconcile flag", args: []string{"reconcile", "--bogus"}, stderr: "unknown reconcile argument: --bogus (valid: --dry-run, --apply, --json, --help, -h)"},
		{name: "reconcile exclusive", args: []string{"reconcile", "--dry-run", "--apply"}, stderr: "--dry-run and --apply are mutually exclusive"},
		{name: "maintenance missing", args: []string{"maintenance"}, stderr: "maintenance plan file argument is required"},
		{name: "maintenance mode word", args: []string{"maintenance", "plan"}, stderr: "maintenance plan file argument is required"},
		{name: "maintenance two files", args: []string{"maintenance", "a.json", "b.json"}, stderr: "unexpected multiple plan files: a.json, b.json"},
		{name: "maintenance files", args: []string{"maintenance", "--files"}, stderr: "--files requires a directory argument"},
		{name: "maintenance flag", args: []string{"maintenance", "--bogus", "plan.json"}, stderr: "unknown flag: --bogus"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			var out, diag bytes.Buffer
			code := runTask(test.args, &out, &diag)
			if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), test.stderr) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
			}
		})
	}
}

func TestBoardRuntimeHelp(t *testing.T) {
	for _, args := range [][]string{
		{"cleanup", "--help"},
		{"done-finalize-all", "-h"},
		{"reconcile", "--help"},
		{"maintenance", "--help"},
	} {
		t.Run(args[0], func(t *testing.T) {
			t.Chdir(t.TempDir())
			var out, diag bytes.Buffer
			if code := runTask(args, &out, &diag); code != 0 || diag.Len() != 0 || !strings.Contains(out.String(), "taskchain-task-manager task "+args[0]) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
			}
		})
	}
}

func TestCleanupPreviewPreservesBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-1\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nDone.\n"
	writeCardFile(t, "tasks/done/001-finished.md", card)
	commitAll(t, "board")
	configureSource(t)
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"cleanup", "TASK-1"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "Preview finalized-card cleanup") || !strings.Contains(out.String(), "BLOCKED") || !strings.Contains(out.String(), "No files changed") {
		t.Fatalf("stdout=%s", out.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
	assertNoMutationLock(t)
	if _, err := os.Stat(".ce-task-cleanup.lock"); !os.IsNotExist(err) {
		t.Fatal("preview created the cleanup directory lock")
	}
}

func TestCleanupApplyRefusalsPreserveBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-1\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\nquality-review: pass\nquality-review-evidence: recorded\n---\n\n## Summary\n\nDone.\n"
	writeCardFile(t, "tasks/done/001-finished.md", card)
	writeCardFile(t, "tasks/todo/002-open.md", "---\nid: TASK-2\ntitle: Open\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nOpen.\n")
	writeCardFile(t, "README.md", "see tasks/done/001-finished.md\n")
	commitAll(t, "board")
	source := configureSource(t)
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"cleanup", "--apply", "--all"}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "cleanup --apply refuses configured source branch \""+source+"\"") {
		t.Fatalf("source code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))

	gitOut(t, "checkout", "-qb", "work")
	before = worktreeFiles(t)
	out.Reset()
	diag.Reset()
	code = runTask([]string{"cleanup", "--apply", "nope"}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "no finalized cleanup card matches nope") {
		t.Fatalf("match code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))

	out.Reset()
	diag.Reset()
	code = runTask([]string{"cleanup", "--apply", "--all"}, &out, &diag)
	if code != 1 || !strings.Contains(diag.String(), "cleanup preflight blocked 1 selected group(s); no files changed") || !strings.Contains(out.String(), "referenced by README.md (path string)") {
		t.Fatalf("reference code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
	assertNoMutationLock(t)
}

func TestCleanupApplyRemovesUnreferencedCard(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-1\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\nquality-review: pass\nquality-review-evidence: recorded\n---\n\n## Summary\n\nDone.\n"
	writeCardFile(t, "tasks/done/001-finished.md", card)
	writeCardFile(t, "tasks/todo/002-open.md", "---\nid: TASK-2\ntitle: Open\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nOpen.\n")
	commitAll(t, "board")
	configureSource(t)
	gitOut(t, "checkout", "-qb", "work")
	kept := mustRead(t, "tasks/todo/002-open.md")

	var out, diag bytes.Buffer
	code := runTask([]string{"cleanup", "--apply", "TASK-1"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "REMOVED tasks/done/001-finished.md") || !strings.Contains(out.String(), "1 removable finalized card group(s).") {
		t.Fatalf("stdout=%s", out.String())
	}
	if _, err := os.Stat("tasks/done/001-finished.md"); !os.IsNotExist(err) {
		t.Fatalf("card still present: %v", err)
	}
	if mustRead(t, "tasks/todo/002-open.md") != kept {
		t.Fatal("sibling card changed")
	}
	if !strings.Contains(gitOut(t, "ls-files"), "tasks/done/001-finished.md") {
		t.Fatal("cleanup updated the git index")
	}
	assertNoMutationLock(t)
	if _, err := os.Stat(".ce-task-cleanup.lock"); !os.IsNotExist(err) {
		t.Fatal("cleanup directory lock remains")
	}
}

func TestDoneFinalizeDelegatesAndPreservesBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-3\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nDone.\n"
	writeCardFile(t, "tasks/done/003-finished.md", card)
	commitAll(t, "board")
	configureSource(t)
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"done-finalize", "--dry-run", "TASK-3"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if strings.Contains(diag.String()+out.String(), "cleanup is not available") || !strings.Contains(out.String(), "Preview finalized-card cleanup") {
		t.Fatalf("stdout=%s stderr=%s", out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
}

func TestDoneFinalizeAllDryRunPreservesBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	writeCardFile(t, "tasks/done/003-finished.md", "---\nid: TASK-3\ntitle: Finished\npriority: P2\neffort: S\ncreated: 2026-10-11\n---\n\n## Summary\n\nDone.\n")
	commitAll(t, "board")
	source := configureSource(t)
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"done-finalize-all"}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "done-finalize-all: cleanup --apply refuses configured source branch \""+source+"\"") {
		t.Fatalf("apply code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))

	out.Reset()
	diag.Reset()
	code = runTask([]string{"done-finalize-all", "--dry-run", "--json"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("dry-run code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), `"dry_run":true`) || !strings.Contains(out.String(), `"skipped":[`) || strings.Contains(out.String(), `"skipped":null`) {
		t.Fatalf("json=%s", out.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
	assertNoMutationLock(t)
}

func TestReconcileDryRunPreservesBytes(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-7\ntitle: Hold\npriority: P2\neffort: S\nstatus: in-progress\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n\n| **Status** | [~] In Progress |\n"
	writeCardFile(t, "tasks/doing/007-hold.md", card)
	writeRuntimeFile(t, "executions.json", `[{"task":"TASK-7","type":"feat","source":"source","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"dev/runner/builder/feat/task-7","worktree":"primary","startedAt":"2026-10-11T00:00:00Z","updatedAt":"2026-10-11T00:00:00Z"}]`+"\n")
	writeRuntimeFile(t, "receipts.jsonl", `{"task":"TASK-7","operation":"abort","status":"ABORTED","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"dev/runner/builder/feat/task-7","worktree":"primary","createdAt":"2026-10-11T00:00:00Z","sourcePushed":false,"worktreeRemoved":false,"localBranchRemoved":false,"remoteBranchRemoved":false}`+"\n")
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"reconcile", "--dry-run"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "reconcile: dry-run=true applied=false") || !strings.Contains(out.String(), "move-to-blocked") || strings.Contains(out.String(), "done") {
		t.Fatalf("stdout=%s", out.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
	assertNoMutationLock(t)
	if _, err := os.Stat("tasks/blocked/007-hold.md"); !os.IsNotExist(err) {
		t.Fatal("dry-run created a destination")
	}
}

func TestReconcileNonDeliveryTerminalEvidenceStaysPut(t *testing.T) {
	cases := []struct {
		name     string
		purpose  string
		prefix   string
		receipt  string
		wantMove string
	}{
		{
			name:     "experiment abort",
			purpose:  "experiment",
			prefix:   "exp",
			receipt:  `{"purpose":"experiment","task":"TASK-7","operation":"abort","status":"ABORTED","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"exp/runner/builder/feat/task-7","worktree":"primary","createdAt":"2026-10-11T00:00:00Z","sourcePushed":false,"worktreeRemoved":false,"localBranchRemoved":false,"remoteBranchRemoved":false}` + "\n",
			wantMove: "move-to-blocked",
		},
		{
			name:     "review done",
			purpose:  "review",
			prefix:   "review",
			receipt:  `{"purpose":"review","task":"TASK-7","operation":"finish","status":"DONE","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"review/runner/builder/feat/task-7","worktree":"primary","createdAt":"2026-10-11T00:00:00Z","sourceHead":"after","sourceHeadBefore":"before","taskHead":"task","sourcePushed":true,"worktreeRemoved":true,"localBranchRemoved":true,"remoteBranchRemoved":true}` + "\n",
			wantMove: "move-to-review",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			initGitRepo(t)
			const card = "---\nid: TASK-7\ntitle: Hold\npriority: P2\neffort: S\nstatus: in-progress\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n\n| **Status** | [~] In Progress |\n"
			writeCardFile(t, "tasks/doing/007-hold.md", card)
			branch := tc.prefix + "/runner/builder/feat/task-7"
			writeRuntimeFile(t, "executions.json", `[{"task":"TASK-7","type":"feat","purpose":"`+tc.purpose+`","source":"source","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"`+branch+`","worktree":"primary","startedAt":"2026-10-11T00:00:00Z","updatedAt":"2026-10-11T00:00:00Z"}]`+"\n")
			writeRuntimeFile(t, "receipts.jsonl", tc.receipt)

			var out, diag bytes.Buffer
			code := runTask([]string{"reconcile", "--dry-run"}, &out, &diag)
			if code != 0 || diag.Len() != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
			}
			if strings.Contains(out.String(), tc.wantMove) || !strings.Contains(out.String(), "non-delivery runtime evidence does not permit board transition") {
				t.Fatalf("stdout=%s", out.String())
			}
			if _, err := os.Stat("tasks/blocked/007-hold.md"); !os.IsNotExist(err) {
				t.Fatal("non-delivery abort moved the card")
			}
			if _, err := os.Stat("tasks/review/007-hold.md"); !os.IsNotExist(err) {
				t.Fatal("non-delivery done moved the card")
			}
		})
	}
}

func TestReconcileMalformedReceiptPrintsJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	writeCardFile(t, "tasks/doing/007-hold.md", "---\nid: TASK-7\ntitle: Hold\n---\n\n## Summary\n\nWork.\n")
	writeRuntimeFile(t, "receipts.jsonl", "\n")
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"reconcile", "--dry-run", "--json"}, &out, &diag)
	if code != 1 || !strings.Contains(diag.String(), "empty receipt record") {
		t.Fatalf("code=%d stderr=%s stdout=%s", code, diag.String(), out.String())
	}
	if !strings.Contains(out.String(), `"dry_run":true`) || !strings.Contains(out.String(), `"cards":[]`) {
		t.Fatalf("stdout=%s", out.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))
}

func TestReconcileApplyMovesToBlocked(t *testing.T) {
	t.Chdir(t.TempDir())
	initGitRepo(t)
	const card = "---\nid: TASK-7\ntitle: Hold\npriority: P2\neffort: S\nstatus: in-progress\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n\n| **Status** | [~] In Progress |\n"
	writeCardFile(t, "tasks/doing/007-hold.md", card)
	commitAll(t, "board")
	source := configureSource(t)
	before := worktreeFiles(t)

	var out, diag bytes.Buffer
	code := runTask([]string{"reconcile"}, &out, &diag)
	if code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "reconcile: cleanup --apply refuses configured source branch \""+source+"\"") {
		t.Fatalf("guard code=%d stdout=%q stderr=%q", code, out.String(), diag.String())
	}
	assertSameFiles(t, before, worktreeFiles(t))

	gitOut(t, "checkout", "-qb", "work")
	writeRuntimeFile(t, "executions.json", `[{"task":"TASK-7","type":"feat","source":"source","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"dev/runner/builder/feat/task-7","worktree":"primary","startedAt":"2026-10-11T00:00:00Z","updatedAt":"2026-10-11T00:00:00Z"}]`+"\n")
	writeRuntimeFile(t, "receipts.jsonl", `{"task":"TASK-7","operation":"abort","status":"ABORTED","owner":{"actor":"runner","host":"builder","kind":"agent"},"branch":"dev/runner/builder/feat/task-7","worktree":"primary","createdAt":"2026-10-11T00:00:00Z","sourcePushed":false,"worktreeRemoved":false,"localBranchRemoved":false,"remoteBranchRemoved":false}`+"\n")
	out.Reset()
	diag.Reset()
	code = runTask([]string{"reconcile"}, &out, &diag)
	if code != 0 || diag.Len() != 0 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, out.String(), diag.String())
	}
	if !strings.Contains(out.String(), "moved-to-blocked") || strings.Contains(out.String(), "moved-to-done") {
		t.Fatalf("stdout=%s", out.String())
	}
	if _, err := os.Stat("tasks/doing/007-hold.md"); !os.IsNotExist(err) {
		t.Fatal("source card remains")
	}
	if _, err := os.Stat("tasks/done/007-hold.md"); !os.IsNotExist(err) {
		t.Fatal("card moved to done")
	}
	moved := mustRead(t, "tasks/blocked/007-hold.md")
	if !strings.Contains(moved, "status: in-progress") || !strings.Contains(moved, "[!] Blocked") || !strings.Contains(moved, "## Runtime interruption evidence") {
		t.Fatalf("moved card:\n%s", moved)
	}
	assertNoMutationLock(t)
}
