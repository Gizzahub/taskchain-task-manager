package boardruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/receiptpath"
)

func TestUnadoptedCleanupKeepsTheClosedOwnershipList(t *testing.T) {
	root := cleanupRepo(t, map[string]string{
		"tasks/done/001-done.md":             doneCard("TASK-1"),
		"tasks/receipts/TASK-1/note.json":    "{}\n",
		"docs/task-reviews/TASK-1/note.json": "{}\n",
		"tasks/issue/001-closed.md":          "---\nid: ISSUE-1\ntitle: Closed\nresolution: fixed\npromoted-to: [TASK-1]\n---\n\nClosed.\n",
	})
	unadopted := cleanupEntries(t, root, receiptpath.Profile{})
	if !hasArtifact(unadopted, "TASK-1", "tasks/receipts/TASK-1/note.json") || !hasArtifact(unadopted, "TASK-1", "docs/task-reviews/TASK-1/note.json") {
		t.Fatalf("unadopted roots changed: %+v", unadopted)
	}
	if hasID(unadopted, "ISSUE-1") {
		t.Fatal("unadopted cleanup adopted an issue")
	}
	adopted := cleanupEntries(t, root, receiptpath.Profile{Base: "section-9", Amendment: "section-11"})
	if !sameArtifacts(unadopted, adopted, "TASK-1") {
		t.Fatalf("section-9 task ownership drifted\nunadopted=%v\nadopted=%v", artifactsOf(unadopted, "TASK-1"), artifactsOf(adopted, "TASK-1"))
	}
	if !hasID(adopted, "ISSUE-1") || hasBlock(adopted, "ISSUE-1", "quality-review") {
		t.Fatalf("issue finalized path: %+v", entryOf(adopted, "ISSUE-1"))
	}
	section10 := cleanupEntries(t, root, receiptpath.Profile{Base: "section-10", Amendment: "section-11"})
	if hasArtifact(section10, "TASK-1", "tasks/receipts/TASK-1/note.json") || !hasArtifact(section10, "TASK-1", "docs/task-reviews/TASK-1/note.json") {
		t.Fatalf("section-10 ownership: %v", artifactsOf(section10, "TASK-1"))
	}
	if _, err := os.Lstat(filepath.Join(root, "tasks", "receipts", "TASK-1", "note.json")); err != nil {
		t.Fatal("section-10 cleanup deleted an in-board receipt during preview")
	}
	if _, err := CleanupCandidatesWithProfile(context.Background(), root, "source", receiptpath.Profile{}); err == nil {
		t.Fatal("unselected profile listed cleanup")
	}
}

func TestAdoptedCleanupBlocksCancelledChildAndActiveManual(t *testing.T) {
	root := cleanupRepo(t, map[string]string{
		"tasks/plan/001-parent.md": "---\nid: PLAN-1\ntitle: Parent\nstatus: done\nchildren: [PLAN-2]\n---\n\nParent.\n",
		"tasks/plan/002-child.md":  "---\nid: PLAN-2\ntitle: Child\nstatus: cancelled\n---\n\nChild.\n",
		"tasks/plan/003-bad.md":    "---\nid: PLAN-3\ntitle: Bad\nstatus: manual\n---\n\nBad.\n",
		"tasks/done/001-done.md":   doneCard("TASK-1"),
		"tasks/receipts/TASK-1/manual-ffffffffffffffffffffffffffffffff.entry.json": "{}\n",
	})
	unadopted := cleanupEntries(t, root, receiptpath.Profile{})
	if hasBlock(unadopted, "TASK-1", "active manual generation") {
		t.Fatal("unadopted cleanup blocked an active manual generation")
	}
	adopted := cleanupEntries(t, root, receiptpath.Profile{Base: "section-9", Amendment: "section-11"})
	if !hasBlock(adopted, "PLAN-1", "cancelled child is not finalized: PLAN-2") {
		t.Fatalf("cancelled child: %+v", entryOf(adopted, "PLAN-1"))
	}
	if hasID(adopted, "PLAN-3") {
		t.Fatal("invalid plan status became finalized")
	}
	if !hasBlock(adopted, "TASK-1", "active manual generation ffffffffffffffffffffffffffffffff") {
		t.Fatalf("active manual: %+v", entryOf(adopted, "TASK-1"))
	}
}

func TestAbsentFinalizedHistoryUnblocksTheIssue(t *testing.T) {
	root := cleanupRepo(t, map[string]string{
		"tasks/done/009-old.md": doneCard("TASK-9"),
	})
	if err := os.Remove(filepath.Join(root, "tasks", "done", "009-old.md")); err != nil {
		t.Fatal(err)
	}
	writeCleanupFile(t, root, "tasks/issue/009-follow.md", "---\nid: ISSUE-9\ntitle: Follow\nresolution: fixed\npromoted-to: [TASK-9]\n---\n\nFollow.\n")
	gitRun(t, root, "add", "-A")
	gitCommit(t, root)
	adopted := cleanupEntries(t, root, receiptpath.Profile{Base: "section-9", Amendment: "section-11"})
	if hasBlock(adopted, "ISSUE-9", "cannot prove finalized history") {
		t.Fatalf("finalized history was refused: %+v", entryOf(adopted, "ISSUE-9"))
	}
	if !entryOf(adopted, "ISSUE-9").Removable {
		t.Fatalf("issue stayed blocked: %+v", entryOf(adopted, "ISSUE-9"))
	}
}

func cleanupRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	for _, key := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		if value, ok := os.LookupEnv(key); ok {
			t.Cleanup(func() { _ = os.Setenv(key, value) })
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Setenv("TASKS_DIR", "tasks")
	root := t.TempDir()
	gitRun(t, root, "init", "-q", "-b", "source")
	for rel, body := range files {
		writeCleanupFile(t, root, rel, body)
	}
	gitRun(t, root, "add", "-A")
	gitCommit(t, root)
	return root
}

func writeCleanupFile(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v %s", strings.Join(args, " "), err, out)
	}
}

func gitCommit(t *testing.T, root string) {
	t.Helper()
	cmd := exec.Command("git", "-c", "user.email=dev@example.com", "-c", "user.name=dev", "commit", "-q", "-m", "fixture")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git commit: %v %s", err, out)
	}
}

func doneCard(id string) string {
	return "---\nid: " + id + "\ntitle: Done\nquality-review: pass\nquality-review-evidence: reviewed\n---\n\nDone.\n"
}

func cleanupEntries(t *testing.T, root string, profile receiptpath.Profile) []CleanupEntry {
	t.Helper()
	var entries []CleanupEntry
	var err error
	if profile.Selected() {
		entries, err = CleanupCandidatesWithProfile(context.Background(), root, "source", profile)
	} else {
		entries, err = CleanupCandidates(context.Background(), root, "source")
	}
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func hasArtifact(entries []CleanupEntry, id, artifact string) bool {
	for _, artifactPath := range artifactsOf(entries, id) {
		if artifactPath == artifact {
			return true
		}
	}
	return false
}

func artifactsOf(entries []CleanupEntry, id string) []string {
	entry := entryOf(entries, id)
	return entry.Artifacts
}

func hasID(entries []CleanupEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func hasBlock(entries []CleanupEntry, id, text string) bool {
	for _, reason := range entryOf(entries, id).Blocked {
		if strings.Contains(reason, text) {
			return true
		}
	}
	return false
}

func entryOf(entries []CleanupEntry, id string) CleanupEntry {
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	return CleanupEntry{}
}

func sameArtifacts(left, right []CleanupEntry, id string) bool {
	got, want := artifactsOf(left, id), artifactsOf(right, id)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
