package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runTaskOut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runTask(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestValidationCommandRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cases := []struct {
		name       string
		args       []string
		code       int
		stderr     string
		stdout     string
		stdoutMiss string
	}{
		{"unknown command", []string{"nope"}, 1, unknownTaskCommandText("nope"), "", ""},
		{"list help", []string{"list", "--help"}, 0, "", "usage: taskchain-task-manager task list ", ""},
		{"list unknown flag", []string{"list", "--priority=P2"}, 1, "unknown flag: --priority=P2 (valid: --status, --priority, --category, --kind, --state, --summary, --json, --help, -h)", "", ""},
		{"list priority vocab", []string{"list", "--priority", "high"}, 1, "unknown --priority value: high (valid: P0, P1, P2, P3)", "", ""},
		{"list priority missing", []string{"list", "--priority"}, 1, "--priority needs a value", "", ""},
		{"list positional", []string{"list", "tasks/todo/001-probe.md"}, 2, "unexpected argument: tasks/todo/001-probe.md", "", ""},
		{"list kind", []string{"list", "--kind", "nope"}, 1, "unknown --kind value: nope (valid: task, issue, plan, backlog)", "", ""},
		{"list state", []string{"list", "--state", "todo"}, 1, "unknown --state value: todo (valid: open, blocked, promoted, resolved)", "", ""},
		{"list status zone word", []string{"list", "--status", "todo"}, 1, "unknown --status value: todo (valid: pending, in-progress, review, blocked, done, cancelled)", "", ""},
		{"archive fold", []string{"archive", "--fold"}, 1, "unknown flag: --fold (valid: --force, --help, -h)", "", ""},
		{"validate none", []string{"validate"}, 2, "specify a task file or use --all", "", ""},
		{"validate unknown", []string{"validate", "--base=abc"}, 2, "unknown flag: --base=abc (valid: --all, --staged, --base, --json, --require-cards, --help, -h)", "", ""},
		{"validate empty path", []string{"validate", " "}, 2, "empty path argument (an unset shell variable?); name a task file or use --all", "", ""},
		{"validate json alone", []string{"validate", "--json"}, 2, "--json is only available with --staged", "", ""},
		{"validate require alone", []string{"validate", "--require-cards"}, 2, "--require-cards is only available with --all", "", ""},
		{"validate base twice", []string{"validate", "--base", "abc", "--base", "def"}, 2, "--base may be supplied only once", "", ""},
		{"validate base missing", []string{"validate", "--base"}, 2, "--base needs a value", "", ""},
		{"validate staged with all", []string{"validate", "--staged", "--all"}, 2, "--staged validates the Git index snapshot; it cannot be combined with --all or named files", "", ""},
		{"validate base with staged", []string{"validate", "--staged", "--base", "abc"}, 2, "--base cannot be combined with --staged", "", ""},
		{"validate all with file", []string{"validate", "--all", "tasks/todo/001-probe.md"}, 2, "--all validates the whole tasks tree; it cannot be combined with named files (tasks/todo/001-probe.md)", "", ""},
		{"validate help skips walk", []string{"validate", "--all", "--help"}, 0, "", "usage: taskchain-task-manager task validate ", "Validation tool:"},
		{"review none", []string{"review-receipt"}, 1, "task review-receipt requires exactly one card path", "", ""},
		{"review flag", []string{"review-receipt", "--json"}, 1, "task review-receipt requires exactly one card path", "", ""},
		{"review help extra", []string{"review-receipt", "--help", "card.md"}, 1, "task review-receipt requires exactly one card path", "", ""},
		{"review help", []string{"review-receipt", "--help"}, 0, "", "usage: taskchain-task-manager task review-receipt CARD\n", ""},
		{"doctor unknown", []string{"doctor", "--bogus"}, 2, "unknown flag --bogus (valid: --json, --capability, --help, -h)", "", ""},
		{"doctor capability", []string{"doctor", "--capability", "other"}, 2, "capability mismatch: want other have ce.task.gate-doctor/v2", "", ""},
		{"doctor capability missing", []string{"doctor", "--capability"}, 2, "flag --capability requires a value", "", ""},
		{"doctor help", []string{"doctor", "--help"}, 0, "", "usage: taskchain-task-manager task doctor ", ""},
		{"doctor not a repo", []string{"doctor"}, 2, "not a git repository", "", ""},
		{"gate-evidence unknown", []string{"gate-evidence"}, 1, unknownTaskCommandText("gate-evidence"), "", ""},
		{"change-id unknown", []string{"change-id"}, 1, unknownTaskCommandText("change-id"), "", ""},
		{"staged not a repo", []string{"validate", "--staged"}, 2, "staged validation requires a Git repository:", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runTaskOut(t, tc.args...)
			if code != tc.code {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if tc.stderr != "" && !strings.Contains(stderr, tc.stderr) {
				t.Fatalf("stderr=%q want %q", stderr, tc.stderr)
			}
			if tc.stdout != "" && !strings.Contains(stdout, tc.stdout) {
				t.Fatalf("stdout=%q want %q", stdout, tc.stdout)
			}
			if tc.stdoutMiss != "" && strings.Contains(stdout, tc.stdoutMiss) {
				t.Fatalf("stdout unexpectedly contains %q: %s", tc.stdoutMiss, stdout)
			}
			if stdout != "" && strings.Contains(stderr, stdout) {
				t.Fatalf("diagnostics leaked onto a check against stdout bytes")
			}
		})
	}
}

func probeCard(priority string) string {
	if priority != "" {
		priority = "priority: " + priority + "\n"
	}
	return "---\nid: TASK-001\ntitle: Probe\ntype: chore\n" + priority +
		"status: pending\neffort: M\nexec-tier: standard\ncreated: 2026-10-11\n---\n\n## Summary\n\nProbe the board.\n\n## Completion Criteria\n\n- [ ] marker is absent | verify: `test -f missing-marker.txt`\n"
}

func TestListBytesAndFilters(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeCardFile(t, "tasks/todo/001-probe.md", probeCard("P2"))

	code, stdout, stderr := runTaskOut(t, "list")
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	want := "=== Task List ===\nDirectory: " + filepath.Join(dir, "tasks") + "\n\n[ ] tasks/todo/001-probe.md\n   Probe | Medium | M\n\nTotal: 1 task(s)\n\n=== End ===\n"
	if stdout != want {
		t.Fatalf("list bytes:\n%s", stdout)
	}
	if strings.Contains(stdout, "Filter:") {
		t.Fatalf("unfiltered list gained a filter line: %s", stdout)
	}

	code, stdout, stderr = runTaskOut(t, "list", "--json")
	if code != 0 {
		t.Fatalf("json code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, `"priority": "Medium"`) || strings.Contains(stdout, "exec_tier") || strings.Contains(stdout, "Filter:") {
		t.Fatalf("json bytes changed: %s", stdout)
	}

	code, stdout, stderr = runTaskOut(t, "list", "--priority", "P2")
	if code != 0 {
		t.Fatalf("filter code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "Filter: Priority=P2\n") || !strings.Contains(stdout, "Probe | Medium | M") {
		t.Fatalf("filtered list: %s", stdout)
	}

	code, stdout, stderr = runTaskOut(t, "list", "--priority", "P0")
	if code != 0 || !strings.Contains(stdout, "No matching tasks") || strings.Contains(stdout, "Total:") {
		t.Fatalf("empty filter code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runTaskOut(t, "list", "--state", "open")
	if code != 0 || !strings.Contains(stdout, "No matching tasks") {
		t.Fatalf("state on a task code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runTaskOut(t, "list", "--summary", "--json")
	if code != 0 {
		t.Fatalf("summary code=%d stderr=%s", code, stderr)
	}
	var summary struct {
		Total      int            `json:"total"`
		ByStatus   map[string]int `json:"by_status"`
		ByPriority map[string]int `json:"by_priority"`
		ByCategory map[string]int `json:"by_category"`
	}
	if err := json.Unmarshal([]byte(stdout), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.ByPriority["Medium"] != 1 || summary.ByStatus["pending"] != 1 {
		t.Fatalf("summary=%s", stdout)
	}
}

func TestPreflightPrintsRawPriority(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeCardFile(t, "tasks/todo/001-probe.md", probeCard("high"))
	plain := probeCard("")
	plain = strings.Replace(plain, "id: TASK-001", "id: TASK-002", 1)
	plain = strings.Replace(plain, "title: Probe", "title: Plain", 1)
	writeCardFile(t, "tasks/todo/002-plain.md", plain)

	code, stdout, stderr := runTaskOut(t, "preflight")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "  high tasks/todo/001-probe.md (1/1 bound)\n") {
		t.Fatalf("raw priority missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "  --   tasks/todo/002-plain.md (1/1 bound)\n") {
		t.Fatalf("empty priority did not render as --:\n%s", stdout)
	}
	if strings.Contains(stdout, "Medium") || strings.Contains(stdout, "High") {
		t.Fatalf("text used the display label:\n%s", stdout)
	}

	code, stdout, stderr = runTaskOut(t, "preflight", "--json")
	if code != 0 {
		t.Fatalf("json code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, `"priority": "High"`) || !strings.Contains(stdout, `"priority": "Medium"`) {
		t.Fatalf("json priority labels: %s", stdout)
	}
}

func TestValidateNamedStagedAndRequireCards(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/001-probe.md", probeCard("P2"))
	if err := os.WriteFile("ce-tasks.yaml", []byte("gate: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stdout, _ := runTaskOut(t, "validate", "--all")
	if strings.Contains(stdout, "Criterion admission") || strings.Contains(stdout, "Criterion tree:") {
		t.Fatalf("--all bytes changed:\n%s", stdout)
	}

	code, stdout, stderr := runTaskOut(t, "validate", "tasks/todo/001-probe.md")
	if code != 2 || !strings.Contains(stderr, "decode ce-tasks.yaml:") {
		t.Fatalf("named dialect code=%d stderr=%s\n%s", code, stderr, stdout)
	}

	if err := os.Remove("ce-tasks.yaml"); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runTaskOut(t, "validate", "tasks/todo/001-probe.md")
	if code != 0 {
		t.Fatalf("named code=%d stderr=%s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "Validation tool: CE v0.8.4-400-gbb970b24") || !strings.Contains(stdout, "Criterion tree: current tree ") || !strings.Contains(stdout, "Criterion admission:") {
		t.Fatalf("named render:\n%s", stdout)
	}

	code, _, stderr = runTaskOut(t, "validate", "tasks/todo/missing.md")
	if code != 1 || !strings.Contains(stderr, "read task file tasks/todo/missing.md:") {
		t.Fatalf("missing code=%d stderr=%s", code, stderr)
	}

	code, stdout, stderr = runTaskOut(t, "validate", "--all", "--require-cards")
	if code != 0 || strings.Contains(stderr, "--require-cards") {
		t.Fatalf("require with cards code=%d stderr=%s", code, stderr)
	}

	empty := t.TempDir()
	t.Chdir(empty)
	code, stdout, stderr = runTaskOut(t, "validate", "--all")
	if code != 0 || !strings.Contains(stdout, "No task files found\n") {
		t.Fatalf("empty --all code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runTaskOut(t, "validate", "--all", "--require-cards")
	if code != 1 || strings.Contains(stdout, "No task files found") || !strings.Contains(stderr, "no task files found under tasks/, but --require-cards was given; that tree is empty or unreachable") {
		t.Fatalf("require empty code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Validation tool:") || !strings.Contains(stdout, "📋 Validating all tasks...") {
		t.Fatalf("require empty omitted the stamp:\n%s", stdout)
	}

	archived := t.TempDir()
	t.Chdir(archived)
	writeCardFile(t, "tasks/_archive/001-probe.md", probeCard("P2"))
	code, stdout, stderr = runTaskOut(t, "validate", "--all", "--require-cards")
	if code != 0 || !strings.Contains(stdout, "No task files found\n") {
		t.Fatalf("archive-only require code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	t.Chdir(dir)
	gitAddCommit(t, "board")
	writeCardFile(t, "tasks/todo/001-probe.md", "not a card anymore\n")
	t.Setenv("TASKS_DIR", "/var/empty")
	code, stdout, stderr = runTaskOut(t, "validate", "--staged")
	if code != 0 || !strings.Contains(stdout, "Staged task snapshot: valid\n") || strings.Contains(stderr, "TASKS_DIR") {
		t.Fatalf("staged code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, _, stderr = runTaskOut(t, "list")
	if code != 2 || !strings.Contains(stderr, "TASKS_DIR must be a directory name") {
		t.Fatalf("list still subject to TASKS_DIR code=%d stderr=%s", code, stderr)
	}
}

func TestReviewReceiptDigestProperties(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	body := "---\nid: TASK-001\ntitle: Probe\ntype: chore\nquality-review: pass\nquality-review-evidence: note\n---\nbody\n"
	writeCardFile(t, "tasks/todo/001-probe.md", body)
	code, first, stderr := runTaskOut(t, "review-receipt", "tasks/todo/001-probe.md")
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	digest := receiptDigest(t, first)
	if len(digest) != 64 {
		t.Fatalf("digest=%q", digest)
	}
	if !strings.Contains(first, `"tool_version": "v0.8.4-400-gbb970b24"`) || !strings.Contains(first, `"tool_revision": "bb970b24adfc29f75e7555cb3cc45716d6973f3b"`) {
		t.Fatalf("receipt=%s", first)
	}

	changed := strings.Replace(body, "quality-review: pass", "quality-review: waived", 1)
	writeCardFile(t, "tasks/todo/001-probe.md", changed)
	_, second, _ := runTaskOut(t, "review-receipt", "tasks/todo/001-probe.md")
	if receiptDigest(t, second) != digest {
		t.Fatal("quality-review changed the digest")
	}
	changed = strings.Replace(body, "quality-review-evidence: note", "quality-review-evidence: other", 1)
	writeCardFile(t, "tasks/todo/001-probe.md", changed)
	_, third, _ := runTaskOut(t, "review-receipt", "tasks/todo/001-probe.md")
	if receiptDigest(t, third) == digest {
		t.Fatal("quality-review-evidence did not change the digest")
	}
	changed = strings.Replace(body, "body\n", "other\n", 1)
	writeCardFile(t, "tasks/todo/001-probe.md", changed)
	_, fourth, _ := runTaskOut(t, "review-receipt", "tasks/todo/001-probe.md")
	if receiptDigest(t, fourth) == digest {
		t.Fatal("body did not change the digest")
	}

	writeCardFile(t, "tasks/todo/001-probe.md", "no frontmatter\n")
	code, _, stderr = runTaskOut(t, "review-receipt", "tasks/todo/001-probe.md")
	if code != 1 || !strings.Contains(stderr, "generate review receipt for tasks/todo/001-probe.md: cannot compute canonical review digest: card has invalid or missing frontmatter") {
		t.Fatalf("malformed code=%d stderr=%s", code, stderr)
	}
	code, _, stderr = runTaskOut(t, "review-receipt", "missing.md")
	if code != 1 || !strings.Contains(stderr, "read review card missing.md:") {
		t.Fatalf("missing code=%d stderr=%s", code, stderr)
	}
}

func receiptDigest(t *testing.T, payload string) string {
	t.Helper()
	var doc struct {
		Digest string `json:"reviewed-card-sha256"`
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Digest
}

func gitAddCommit(t *testing.T, message string) {
	t.Helper()
	gitRun(t, nil, "add", "-A")
	gitRun(t, nil, "commit", "-qm", message)
}
