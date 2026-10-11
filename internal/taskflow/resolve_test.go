package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveIssueTypeAwareAndPreservesBody(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "todo/002-fix.md", "TASK-2", "feature")
	wantBody := "# Keep\n\nquoted: 'value'\n"
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	original := string(mustResolveRead(t, p))
	if !strings.Contains(original, "disposition: blocked\n") || !strings.Contains(original, wantBody) {
		t.Fatal("fixture bytes missing")
	}
	result, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "TASK-2")
	if err != nil || !result.Changed || !strings.HasSuffix(result.ResolvedAt, "Z") || result.Summary != "Resolved as fixed by TASK-2." {
		t.Fatalf("resolve: %#v %v", result, err)
	}
	updated := string(mustResolveRead(t, p))
	if !strings.Contains(updated, "resolution: fixed\n") || !strings.Contains(updated, "resolution-summary: \"Resolved as fixed by TASK-2.\"\n") {
		t.Fatalf("stamp mismatch:\n%s", updated)
	}
	if !strings.Contains(updated, "disposition: blocked\n") || !strings.Contains(updated, wantBody) || !strings.Contains(updated, "note: 'quoted: value'\n") {
		t.Fatalf("unrelated bytes changed:\n%s", updated)
	}
	if _, err := time.Parse(time.RFC3339, result.ResolvedAt); err != nil {
		t.Fatal(err)
	}
}

func TestResolveIssueRejectsWrongByWithoutWrite(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "todo/002-fix.md", "TASK-2", "feature")
	writeResolveCard(t, root, "issue/003-other.md", "ISSUE-3", "bug")
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before := string(mustResolveRead(t, p))
	cases := []struct{ resolution, by, want string }{
		{"fixed", "ISSUE-1", "not a TASK card"},
		{"duplicate", "TASK-2", "not an ISSUE card"},
		{"fixed", "TASK-9", "not a TASK card"},
		{"duplicate", "ISSUE-9", "not an ISSUE card"},
	}
	for _, tc := range cases {
		_, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", tc.resolution, tc.by)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s/%s: %v", tc.resolution, tc.by, err)
		}
		if got := string(mustResolveRead(t, p)); got != before {
			t.Fatal("type mismatch wrote the card")
		}
	}
	dup, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "duplicate", "ISSUE-3")
	if err != nil || !dup.Changed || dup.Summary != "Resolved as duplicate by ISSUE-3." {
		t.Fatalf("duplicate: %#v %v", dup, err)
	}
}

func TestResolveIssueReresolveStampsCurrentTime(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	writeResolveRaw(t, p, "---\nid: ISSUE-1\ntitle: Test\ntype: bug\ndisposition: blocked\nresolution: fixed\nresolved-at: 2020-01-01T00:00:00Z\nresolution-summary: 'Resolved as fixed.'\n---\n# Keep\n")
	before := string(mustResolveRead(t, p))
	later := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	freezeResolve(t, later)
	got, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "")
	if err != nil || !got.Changed || got.ResolvedAt != "2024-05-06T07:08:09Z" || got.Summary != "Resolved as fixed." {
		t.Fatalf("reresolve: %#v %v", got, err)
	}
	updated := string(mustResolveRead(t, p))
	if strings.Contains(updated, "2020-01-01T00:00:00Z") || !strings.Contains(updated, "resolved-at: 2024-05-06T07:08:09Z\n") {
		t.Fatalf("timestamp not replaced:\n%s", updated)
	}
	if !strings.Contains(updated, "resolution-summary: \"Resolved as fixed.\"\n") || !strings.Contains(updated, "disposition: blocked\n") || !strings.Contains(updated, "# Keep\n") {
		t.Fatalf("reresolve rewrote unrelated bytes:\n%s", updated)
	}
	if strings.Contains(before, "resolved-at: 2024-05-06T07:08:09Z") {
		t.Fatal("fixture already had the new timestamp")
	}
}

func TestResolveIssueIdenticalBytesNoOp(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	when := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	freezeResolve(t, when)
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	first, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "invalid", "")
	if err != nil || !first.Changed {
		t.Fatal(err)
	}
	stamped := string(mustResolveRead(t, p))
	past := time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(p, past, past); err != nil {
		t.Fatal(err)
	}
	second, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "invalid", "")
	if err != nil || second.Changed || second.ResolvedAt != first.ResolvedAt {
		t.Fatalf("noop: %#v %v", second, err)
	}
	if string(mustResolveRead(t, p)) != stamped {
		t.Fatal("noop rewrote bytes")
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(past) {
		t.Fatalf("noop wrote the file, mtime %s", info.ModTime())
	}
}

func TestResolveIssueCRLFPreservesNewline(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	raw := "---\r\nid: ISSUE-1\r\ntitle: Test\r\ntype: bug\r\ndisposition: blocked\r\nnote: 'quoted: value'\r\n---\r\n# Keep\r\n\r\nquoted: 'value'\r\n"
	writeResolveRaw(t, p, raw)
	got, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "wontfix", "")
	if err != nil || !got.Changed {
		t.Fatal(err)
	}
	updated := string(mustResolveRead(t, p))
	if strings.Contains(updated, "\n") && strings.Contains(strings.ReplaceAll(updated, "\r\n", ""), "\n") {
		t.Fatalf("CRLF card gained a bare LF:\n%q", updated)
	}
	if !strings.Contains(updated, "disposition: blocked\r\n") || !strings.Contains(updated, "note: 'quoted: value'\r\n") || !strings.Contains(updated, "# Keep\r\n\r\nquoted: 'value'\r\n") {
		t.Fatalf("CRLF unrelated bytes changed:\n%q", updated)
	}
	if !strings.Contains(updated, "resolution: wontfix\r\n") || !strings.Contains(updated, "resolution-summary: \"Resolved as wontfix.\"\r\n") {
		t.Fatalf("CRLF stamp missing:\n%q", updated)
	}
}

func TestResolveIssueMalformedDoesNotWrite(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	cases := []string{
		"---\nid: [oops\n---\nbody\n",
		"---\nid: ISSUE-1\n",
		"no frontmatter\n",
	}
	for _, raw := range cases {
		writeResolveRaw(t, p, raw)
		before := string(mustResolveRead(t, p))
		if _, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", ""); err == nil || !strings.Contains(err.Error(), "malformed frontmatter") {
			t.Fatalf("malformed accepted: %q", raw)
		}
		if got := string(mustResolveRead(t, p)); got != before {
			t.Fatalf("malformed wrote:\n%s", got)
		}
	}
}

func TestResolveIssueArchivedByTarget(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "_archive/done/002-fix.md", "TASK-2", "feature")
	writeResolveCard(t, root, "archive/issue/003-old.md", "ISSUE-3", "bug")
	fixed, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "TASK-2")
	if err != nil || !fixed.Changed || fixed.Summary != "Resolved as fixed by TASK-2." {
		t.Fatalf("archived task: %#v %v", fixed, err)
	}
	dup, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "duplicate", "ISSUE-3")
	if err != nil || !dup.Changed || dup.Summary != "Resolved as duplicate by ISSUE-3." {
		t.Fatalf("archived issue: %#v %v", dup, err)
	}
}

func TestResolveIssueAmbiguousBy(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "todo/002-a.md", "TASK-2", "feature")
	writeResolveCard(t, root, "done/002-b.md", "TASK-2", "feature")
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before := string(mustResolveRead(t, p))
	_, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "TASK-2")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity: %v", err)
	}
	if got := string(mustResolveRead(t, p)); got != before {
		t.Fatal("ambiguity wrote the card")
	}
	writeResolveCard(t, root, "issue/004-a.md", "ISSUE-4", "bug")
	writeResolveCard(t, root, "_archive/issue/004-b.md", "ISSUE-4", "bug")
	_, err = ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "duplicate", "ISSUE-4")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("duplicate ambiguity: %v", err)
	}
	if got := string(mustResolveRead(t, p)); got != before {
		t.Fatal("duplicate ambiguity wrote the card")
	}
}

func TestResolveIssueCheckOrderAndPathEscape(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "todo/002-fix.md", "TASK-2", "feature")
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before := string(mustResolveRead(t, p))
	_, err := ResolveIssue(context.Background(), root, "tasks/issue/missing.md", "nope", "")
	if err == nil || !strings.Contains(err.Error(), "invalid resolution") {
		t.Fatalf("resolution order: %v", err)
	}
	_, err = ResolveIssueWithByFlag(context.Background(), root, "tasks/issue/missing.md", "wontfix", "", true)
	if err == nil || !strings.Contains(err.Error(), "file not found") || strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("file order: %v", err)
	}
	_, err = ResolveIssue(context.Background(), root, "tasks/todo/002-fix.md", "fixed", "")
	if err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("kind: %v", err)
	}
	outside := filepath.Join(root, "outside.md")
	const outsideBody = "---\nid: ISSUE-9\ntitle: Out\ntype: bug\n---\nkeep\n"
	if err := os.WriteFile(outside, []byte(outsideBody), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveIssue(context.Background(), root, "tasks/issue/../../outside.md", "invalid", "")
	if err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("escape: %v", err)
	}
	if got := string(mustResolveRead(t, outside)); got != outsideBody {
		t.Fatalf("escape wrote outside: %q", got)
	}
	if got := string(mustResolveRead(t, p)); got != before {
		t.Fatal("refusals wrote the issue")
	}
	link := filepath.Join(root, "tasks/issue/link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	_, err = ResolveIssue(context.Background(), root, "tasks/issue/link.md", "invalid", "")
	if err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("symlink escape: %v", err)
	}
	if got := string(mustResolveRead(t, outside)); got != outsideBody {
		t.Fatalf("symlink wrote outside: %q", got)
	}
}

func TestResolveIssueWontfixInvalidBy(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCard(t, root, "todo/002-fix.md", "TASK-2", "feature")
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before := string(mustResolveRead(t, p))
	for _, resolution := range []string{"wontfix", "invalid"} {
		_, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", resolution, "TASK-2")
		if err == nil || !strings.Contains(err.Error(), "not accepted with "+resolution) {
			t.Fatalf("%s: %v", resolution, err)
		}
		_, err = ResolveIssueWithByFlag(context.Background(), root, "tasks/issue/001-bug.md", resolution, "  ", true)
		if err == nil || !strings.Contains(err.Error(), "not accepted with "+resolution) {
			t.Fatalf("%s empty: %v", resolution, err)
		}
		if got := string(mustResolveRead(t, p)); got != before {
			t.Fatal("wontfix/invalid --by wrote the card")
		}
	}
	ok, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "wontfix", "")
	if err != nil || !ok.Changed || ok.Summary != "Resolved as wontfix." {
		t.Fatalf("wontfix without by: %#v %v", ok, err)
	}
}

func TestResolveIssueEmptyBy(t *testing.T) {
	root := t.TempDir()
	writeResolveCard(t, root, "issue/001-bug.md", "ISSUE-1", "bug")
	p := filepath.Join(root, "tasks/issue/001-bug.md")
	before := string(mustResolveRead(t, p))
	_, err := ResolveIssueWithByFlag(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "", true)
	if err == nil || !strings.Contains(err.Error(), "--by needs a TASK id") {
		t.Fatalf("fixed empty: %v", err)
	}
	_, err = ResolveIssueWithByFlag(context.Background(), root, "tasks/issue/001-bug.md", "duplicate", " ", true)
	if err == nil || !strings.Contains(err.Error(), "--by needs an ISSUE id") {
		t.Fatalf("duplicate empty: %v", err)
	}
	if got := string(mustResolveRead(t, p)); got != before {
		t.Fatal("empty --by wrote the card")
	}
	ok, err := ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "fixed", "")
	if err != nil || !ok.Changed || ok.Summary != "Resolved as fixed." {
		t.Fatalf("omitted by: %#v %v", ok, err)
	}
}

func TestResolveIssueStemFileAndConfiguredDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TASKS_DIR", "board")
	writeResolveCardAt(t, root, "board", "issue/001-bug.md", "ISSUE-1", "bug")
	writeResolveCardAt(t, root, "board", "todo/010-stem.md", "TASK-10", "feature")
	writeResolveRaw(t, filepath.Join(root, "board/issue/012-orig.md"), "---\ntitle: Origin\ntype: bug\n---\nbody\n")
	writeResolveCardAt(t, root, "tasks", "issue/001-bug.md", "ISSUE-1", "bug")
	other := string(mustResolveRead(t, filepath.Join(root, "tasks/issue/001-bug.md")))
	byStem, err := ResolveIssue(context.Background(), root, "board/issue/001-bug.md", "fixed", "010-stem")
	if err != nil || byStem.Summary != "Resolved as fixed by 010-stem." {
		t.Fatalf("stem: %#v %v", byStem, err)
	}
	byFile, err := ResolveIssue(context.Background(), root, "board/issue/001-bug.md", "duplicate", "file:012-orig.md")
	if err != nil || byFile.Summary != "Resolved as duplicate by file:012-orig.md." {
		t.Fatalf("file: %#v %v", byFile, err)
	}
	if got := string(mustResolveRead(t, filepath.Join(root, "tasks/issue/001-bug.md"))); got != other {
		t.Fatal("configured dir wrote the default tasks tree")
	}
	_, err = ResolveIssue(context.Background(), root, "tasks/issue/001-bug.md", "invalid", "")
	if err == nil || !strings.Contains(err.Error(), "is not under issue/") {
		t.Fatalf("default tree while TASKS_DIR=board: %v", err)
	}
}

func freezeResolve(t *testing.T, at time.Time) {
	t.Helper()
	prev := resolveNow
	resolveNow = func() time.Time { return at }
	t.Cleanup(func() { resolveNow = prev })
}

func writeResolveCard(t *testing.T, root, rel, id, typ string) {
	t.Helper()
	writeResolveCardAt(t, root, "tasks", rel, id, typ)
}

func writeResolveCardAt(t *testing.T, root, tasksDir, rel, id, typ string) {
	t.Helper()
	full := filepath.Join(root, tasksDir, filepath.FromSlash(rel))
	raw := "---\nid: " + id + "\ntitle: Test\ntype: " + typ + "\ndisposition: blocked\nnote: 'quoted: value'\n---\n# Keep\n\nquoted: 'value'\n"
	writeResolveRaw(t, full, raw)
}

func writeResolveRaw(t *testing.T, full, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustResolveRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
