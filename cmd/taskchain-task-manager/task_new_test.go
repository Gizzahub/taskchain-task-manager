package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// initGitRepo turns the test's working directory into a repository, because
// the reservation ledger lives in the git common dir and a caller without a
// repository falls back to tree-scan numbering.
func initGitRepo(t *testing.T) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "test@example.invalid"},
		{"config", "user.name", "test"},
	} {
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func writeCardFile(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(".", rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTaskNewReservesThroughLedgerFloor(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".git", "ce", "card-id-reservations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "ce", "card-id-reservations", "highest.json"), []byte("{\"TASK\": 5}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCardFile(t, "tasks/todo/001-real.md", "---\nid: TASK-001\ntitle: real\ntype: chore\npriority: P3\nstatus: todo\ncreated: 2026-09-29\n---\n\n## Summary\n\nbody quoting an old probe card:\n\n```bash\nid: TASK-999\n```\n")

	var out, diag bytes.Buffer
	code := runTask([]string{"new", "task", "--title", "Floor probe",
		"--criterion", "bound | verify: `test -f absent-floor-probe-marker.txt`"}, &out, &diag)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, diag.String())
	}
	if got := out.String(); got != "✅ Created tasks/todo/006-floor-probe.md (TASK-006)\n" {
		t.Fatalf("stdout=%q", got)
	}
	// The quoted TASK-999 is prose; the floor is frontmatter 1 under ledger 5.
	created := filepath.Join(dir, "tasks", "todo", "006-floor-probe.md")
	b, err := os.ReadFile(created)
	if err != nil {
		t.Fatal(err)
	}
	want := "---\nid: TASK-006\ntitle: \"Floor probe\"\ntype: feature\npriority: P2\ncreated: " +
		time.Now().Format("2006-01-02") +
		"\n---\n\n## Summary\n\n<!-- One paragraph: what changes and why. -->\n\n## Completion Criteria\n\n- [ ] bound | verify: `test -f absent-floor-probe-marker.txt`\n"
	if string(b) != want {
		t.Fatalf("created card bytes:\n%q\nwant:\n%q", string(b), want)
	}
	ledger, err := os.ReadFile(filepath.Join(dir, ".git", "ce", "card-id-reservations", "highest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(ledger) != "{\n  \"TASK\": 6\n}\n" {
		t.Fatalf("ledger=%q", string(ledger))
	}
}

func TestTaskNewRefusesFullNumberSpace(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/999-ceiling.md", "---\nid: TASK-999\ntitle: ceiling\ntype: chore\npriority: P3\nstatus: todo\ncreated: 2026-09-29\n---\n\n## Summary\n\ntop of the number space\n")

	var out, diag bytes.Buffer
	code := runTask([]string{"new", "task", "--title", "Over the top"}, &out, &diag)
	if code != 1 || out.Len() != 0 {
		t.Fatalf("code=%d stdout=%q", code, out.String())
	}
	if got := diag.String(); got != "Error: the dialect number space is full\n" {
		t.Fatalf("stderr=%q", got)
	}
	// A refusal must not leave a ledger behind: the number was not reserved.
	if _, err := os.Stat(filepath.Join(dir, ".git", "ce", "card-id-reservations", "highest.json")); !os.IsNotExist(err) {
		t.Fatalf("ledger exists after refusal: %v", err)
	}
}

func TestTaskNewFloorIgnoresBodyQuotedIDWithoutLedger(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	writeCardFile(t, "tasks/todo/001-real.md", "---\nid: TASK-001\n---\n\n```bash\nid: TASK-999\n```\n")

	var out, diag bytes.Buffer
	if code := runTask([]string{"new", "task", "--title", "Second"}, &out, &diag); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, diag.String())
	}
	// Frontmatter 1, ledger absent: the next number is 2. Counting the quoted
	// 999 would have spelled 1000, which is no card number at all. The
	// placeholder hint follows the Created line because no criterion was given.
	want := "✅ Created tasks/todo/002-second.md (TASK-002)\n" +
		"   next: replace the placeholder criterion with real ones, each bound with `| verify:`\n"
	if got := out.String(); got != want {
		t.Fatalf("stdout=%q", got)
	}
}

func TestTaskNewUsageAndInputRefusals(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	initGitRepo(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"new", "task", "--bogus"}, "unknown argument --bogus"},
		{"kind required", []string{"new"}, "kind is required"},
		{"zone positional", []string{"new", "todo", "--title", "x"}, "is a zone name, not a kind"},
		{"unknown kind", []string{"new", "story", "--title", "x"}, "unknown kind"},
		{"title required", []string{"new", "task"}, "--title is required"},
		{"unbound criterion", []string{"new", "task", "--title", "x", "--criterion", "no binding"}, "needs a backtick command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, diag bytes.Buffer
			if code := runTask(tc.args, &out, &diag); code != 2 || out.Len() != 0 {
				t.Fatalf("code=%d stdout=%q", code, out.String())
			}
			if !strings.Contains(diag.String(), tc.want) {
				t.Fatalf("stderr=%q want substring %q", diag.String(), tc.want)
			}
		})
	}
}
