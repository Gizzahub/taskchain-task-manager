package githistory

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Synthetic", "GIT_AUTHOR_EMAIL=synthetic@example.invalid", "GIT_COMMITTER_NAME=Synthetic", "GIT_COMMITTER_EMAIL=synthetic@example.invalid")
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, raw)
	}
	return strings.TrimSpace(string(raw))
}

func historyRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitTest(t, dir, "init", "-b", "fixture")
	gitTest(t, dir, "config", "commit.gpgsign", "false")
	gitTest(t, dir, "config", "tag.gpgsign", "false")
	return dir
}

func historyCard(t *testing.T, dir, file, raw string) {
	t.Helper()
	file = filepath.Join(dir, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func historyCommit(t *testing.T, dir string) string {
	t.Helper()
	gitTest(t, dir, "add", ".")
	gitTest(t, dir, "commit", "-m", "synthetic history")
	return gitTest(t, dir, "rev-parse", "HEAD")
}

func TestScanRetainsDeletedArchiveAndRemoteHistory(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/misleading-TASK-999.md", "id: TASK-001\n")
	historyCard(t, dir, "tasks/_archive/old.md", "id: 'TASK-002'\n")
	historyCard(t, dir, "tasks/evidence/log.md", "id: TASK-999\n")
	first := historyCommit(t, dir)
	gitTest(t, dir, "checkout", "-b", "side")
	historyCard(t, dir, "tasks/todo/odd\t\"name.md", "id: TASK-003\nid: PLAN-007 # sample\n")
	side := historyCommit(t, dir)
	gitTest(t, dir, "update-ref", "refs/remotes/fixture/unlanded", side)
	gitTest(t, dir, "checkout", "fixture")
	gitTest(t, dir, "branch", "-D", "side")
	gitTest(t, dir, "rm", "tasks/todo/misleading-TASK-999.md")
	historyCommit(t, dir)
	gitTest(t, dir, "tag", "-a", "snapshot", "-m", "synthetic tag", first)
	before := gitTest(t, dir, "status", "--porcelain=v1")
	got, err := Scan(context.Background(), dir, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.IDs, []string{"PLAN-7", "TASK-1", "TASK-2", "TASK-3"}) {
		t.Fatalf("IDs=%v", got.IDs)
	}
	if len(got.Refs) != 3 || got.Blobs != 3 {
		t.Fatalf("snapshot=%+v", got)
	}
	if gitTest(t, dir, "status", "--porcelain=v1") != before {
		t.Fatal("source changed")
	}
}

func TestScanLiteralBoardAndCandidateDedup(t *testing.T) {
	dir := historyRepo(t)
	for _, file := range []string{"evidence/tasks[1]/README.md", "evidence/tasks[1]/todo/a.md", "evidence/tasks[1]/todo/TASK-99.md"} {
		historyCard(t, dir, file, "id: TASK-8\n")
	}
	historyCard(t, dir, "evidence/tasks1/todo/wrong.md", "id: TASK-900\n")
	historyCommit(t, dir)
	got, err := Scan(context.Background(), dir, "evidence/tasks[1]")
	if err != nil || !reflect.DeepEqual(got.IDs, []string{"TASK-8"}) || got.Blobs != 1 {
		t.Fatalf("literal/dedup=%+v %v", got, err)
	}
}

func TestScanEmptyAndRejectedSources(t *testing.T) {
	dir := historyRepo(t)
	got, err := Scan(context.Background(), dir, "tasks")
	if err != nil || len(got.IDs) != 0 || len(got.Refs) != 0 {
		t.Fatalf("empty=%+v %v", got, err)
	}
	for _, board := range []string{"", ".", "../tasks", "/tasks", "tasks/../other", "tasks/"} {
		if _, err := Scan(context.Background(), dir, board); err == nil {
			t.Fatalf("invalid board %q accepted", board)
		}
	}
	t.Setenv("GIT_DIR", filepath.Join(dir, ".git"))
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "GIT_DIR") {
		t.Fatalf("override=%v", err)
	}
}

func TestScanRejectsShallowReplaceGraftAndPartial(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/a.md", "id: TASK-1\n")
	first := historyCommit(t, dir)
	historyCard(t, dir, "tasks/todo/b.md", "id: TASK-2\n")
	second := historyCommit(t, dir)
	shallow := filepath.Join(t.TempDir(), "shallow")
	gitTest(t, dir, "clone", "--depth=1", "file://"+dir, shallow)
	if _, err := Scan(context.Background(), shallow, "tasks"); err == nil || !strings.Contains(err.Error(), "shallow") {
		t.Fatalf("shallow=%v", err)
	}
	gitTest(t, dir, "replace", first, second)
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "replace") {
		t.Fatalf("replace=%v", err)
	}
	gitTest(t, dir, "replace", "-d", first)
	grafts := filepath.Join(dir, ".git/info/grafts")
	if err := os.WriteFile(grafts, []byte(first+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "graft") {
		t.Fatalf("graft=%v", err)
	}
	if err := os.Remove(grafts); err != nil {
		t.Fatal(err)
	}
	gitTest(t, dir, "config", "remote.fixture.promisor", "true")
	if _, err := Scan(context.Background(), dir, "tasks"); err == nil || !strings.Contains(err.Error(), "promisor") {
		t.Fatalf("promisor=%v", err)
	}
}
