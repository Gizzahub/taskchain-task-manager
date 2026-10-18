package taskflow

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func captureTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}
func captureTestWrite(t *testing.T, root, rel, data string) {
	t.Helper()
	name := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
func captureTestRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	captureTestGit(t, root, "init", "-q")
	captureTestWrite(t, root, "tasks/todo/a.md", "ref")
	captureTestGit(t, root, "add", ".")
	captureTestGit(t, root, "commit", "-qm", "fixture")
	return root
}

func TestCardsOnlyCaptureSources(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	projection := "<!-- task-manager:projection-begin format=\"live-card-paths-v1\" owner=\"test\" source-revision=\"r1\" -->\n- todo/a.md\n<!-- task-manager:projection-end format=\"live-card-paths-v1\" owner=\"test\" source-revision=\"r1\" -->\n"
	captureTestWrite(t, root, "tasks/README.md", projection)
	captureTestGit(t, root, "add", "tasks")
	captureTestGit(t, root, "commit", "-qm", "projection")
	captureTestWrite(t, root, "tasks/todo/a.md", "index")
	captureTestGit(t, root, "add", "tasks")
	captureTestWrite(t, root, "tasks/README.md", "invalid index bytes")
	captureTestGit(t, root, "add", "tasks/README.md")
	captureTestWrite(t, root, "tasks/README.md", projection)
	captureTestWrite(t, root, "tasks/todo/a.md", "worktree")
	captureTestWrite(t, root, "tasks/todo/INDEX.md", "residue")
	captureTestWrite(t, root, ".gitignore", "tasks/todo/README.md\n")
	captureTestWrite(t, root, "tasks/todo/README.md", "ignored")
	ref, err := CaptureCardsOnlyRef(ctx, root, "tasks", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	index, err := CaptureCardsOnlyIndex(ctx, root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	work, err := CaptureCardsOnlyWorktree(ctx, root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	if ref.TreeOID == "" || index.IndexDigest == "" || work.IndexDigest != index.IndexDigest {
		t.Fatal("missing source identity")
	}
	// A projection intentionally expecting distinct lists proves that snapshot
	// content differs without exposing mutable entry maps to production callers.
	policy, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{}}, []boardpolicy.FileRoleV5{{Path: "README.md", Kind: boardpolicy.FileRoleProjection, Owner: "test", SourceRevision: "r1", Format: "live-card-paths-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s    boardfiles.CardsOnlySnapshot
		want int
	}{{ref.Snapshot, 0}, {index.Snapshot, 1}, {work.Snapshot, 2}} {
		findings, err := boardfiles.AssessCardsOnlySnapshot(c.s, policy)
		if err != nil {
			t.Fatal(err)
		}
		if len(findings) != c.want {
			t.Fatalf("wrong inventory: %+v", findings)
		}
	}
	if ref.TreeOID != captureTestGit(t, root, "rev-parse", "HEAD^{tree}") {
		t.Fatal("wrong ref tree")
	}
}

func TestCardsOnlyCaptureIndexChangesAndAuthority(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	_, err := captureCardsOnlyIndex(ctx, root, "tasks", func() error {
		captureTestWrite(t, root, "tasks/todo/a.md", "changed")
		captureTestGit(t, root, "add", "tasks")
		return nil
	})
	if err == nil {
		t.Fatal("index modification accepted")
	}
	if _, err := CaptureCardsOnlyIndex(ctx, filepath.Join(root, "tasks"), "todo"); err == nil {
		t.Fatal("non-root repository accepted")
	}
	for _, key := range []string{"GIT_INDEX_FILE", "GIT_CONFIG_COUNT", "GIT_TRACE", "GIT_OBJECT_DIRECTORY", "GIT_REPLACE_REF_BASE"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, "override")
			if _, err := CaptureCardsOnlyIndex(ctx, root, "tasks"); err == nil {
				t.Fatal("override accepted")
			}
		})
	}
	captureTestGit(t, root, "update-index", "--skip-worktree", "tasks/todo/a.md")
	if _, err := CaptureCardsOnlyWorktree(ctx, root, "tasks"); err == nil {
		t.Fatal("skip-worktree accepted")
	}
}

func TestCardsOnlyCaptureGitRefusesInvalidInputs(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	for _, board := range []string{".", "../tasks", "/tasks", "tasks/../tasks", ".git", "tasks\\todo"} {
		if _, err := CaptureCardsOnlyIndex(ctx, root, board); err == nil {
			t.Fatalf("accepted %q", board)
		}
	}
	if _, err := CaptureCardsOnlyRef(ctx, root, "tasks", "missing-ref"); err == nil {
		t.Fatal("missing ref accepted")
	}
	captureTestWrite(t, root, "tasks/todo/new.md", "intent")
	captureTestGit(t, root, "add", "-N", "tasks/todo/new.md")
	if _, err := CaptureCardsOnlyIndex(ctx, root, "tasks"); err == nil {
		t.Fatal("intent-to-add accepted")
	}
}

func TestCardsOnlyCaptureUnmergedAndUnsupported(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	oid := captureTestGit(t, root, "rev-parse", "HEAD:tasks/todo/a.md")
	captureTestGit(t, root, "update-index", "--force-remove", "tasks/todo/a.md")
	cmd := exec.Command("git", "update-index", "--index-info")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader("100644 " + oid + " 1\ttasks/todo/a.md\n100644 " + oid + " 2\ttasks/todo/a.md\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create conflict: %s %v", out, err)
	}
	if _, err := CaptureCardsOnlyIndex(ctx, root, "tasks"); err == nil {
		t.Fatal("unmerged index accepted")
	}
	for _, raw := range []string{"100644 blob " + oid + "\ttasks/todo/a.md", "160000 commit " + oid + "\ttasks/sub\x00", "100644 blob " + oid + "\tother/a.md\x00"} {
		if _, err := parseCaptureListing([]byte(raw), "tasks", false); err == nil {
			t.Fatalf("accepted malformed listing %q", raw)
		}
	}
}

func TestCardsOnlyCaptureGitLimitsAndLegacyState(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	captureTestWrite(t, root, "tasks/.ce/state", strings.Repeat("x", (1<<20)+1))
	captureTestGit(t, root, "add", "tasks/.ce")
	index, err := CaptureCardsOnlyIndex(ctx, root, "tasks")
	if err != nil {
		t.Fatal("legacy bytes read:", err)
	}
	findings, err := boardfiles.AssessCardsOnlySnapshot(index.Snapshot, boardpolicy.Default())
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Path != ".ce" {
		t.Fatalf("legacy not folded: %+v", findings)
	}
	captureTestWrite(t, root, "tasks/todo/large.md", strings.Repeat("x", (1<<20)+1))
	captureTestGit(t, root, "add", "tasks/todo")
	if _, err := CaptureCardsOnlyIndex(ctx, root, "tasks"); err == nil {
		t.Fatal("oversize blob accepted")
	}
	if _, err := captureGit(ctx, root, 1, "rev-parse", "HEAD"); err == nil {
		t.Fatal("output limit ignored")
	}
}

func TestCardsOnlyCaptureLiteralBoardAndMissingObjects(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	captureTestWrite(t, root, "[board]/todo/a.md", "literal")
	captureTestGit(t, root, "add", "--", "[board]")
	c, err := CaptureCardsOnlyIndex(ctx, root, "[board]")
	if err != nil {
		t.Fatal(err)
	}
	findings, err := boardfiles.AssessCardsOnlySnapshot(c.Snapshot, boardpolicy.Default())
	if err != nil || len(findings) != 0 {
		t.Fatalf("literal path failed: %+v %v", findings, err)
	}
	leaves, _, err := captureIndex(ctx, root, "[board]")
	if err != nil {
		t.Fatal(err)
	}
	if len(leaves) != 1 {
		t.Fatalf("wrong literal inventory: %+v", leaves)
	}
	entries, err := captureBlobs(ctx, root, leaves)
	if err != nil || string(entries[0].Data) != "literal" {
		t.Fatalf("blob bytes not source-bound: %+v %v", entries, err)
	}
	captureTestGit(t, root, "update-index", "--add", "--cacheinfo", "100644,"+strings.Repeat("a", 40)+",tasks/todo/missing.md")
	if _, err := CaptureCardsOnlyIndex(ctx, root, "tasks"); err == nil {
		t.Fatal("missing object accepted")
	}
	if _, err := CaptureCardsOnlyRef(ctx, root, "tasks", "--help"); err == nil {
		t.Fatal("option-like ref accepted")
	}
}

func TestCardsOnlyCaptureBoardShapeAndOIDGrammar(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	captureTestWrite(t, root, "board.md", "file")
	captureTestGit(t, root, "add", "board.md")
	captureTestGit(t, root, "commit", "-qm", "file board")
	for _, capture := range []func(context.Context, string, string) (CardsOnlyCapture, error){CaptureCardsOnlyIndex, func(c context.Context, r, b string) (CardsOnlyCapture, error) {
		return CaptureCardsOnlyRef(c, r, b, "HEAD")
	}} {
		if _, err := capture(ctx, root, "board.md"); err == nil {
			t.Fatal("file board accepted")
		}
		if _, err := capture(ctx, root, "absent"); err != nil {
			t.Fatal("empty logical subtree:", err)
		}
	}
	commit := captureTestGit(t, root, "rev-parse", "HEAD")
	captureTestGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+commit+",submodule-board")
	if _, err := CaptureCardsOnlyIndex(ctx, root, "submodule-board"); err == nil {
		t.Fatal("gitlink board accepted")
	}
	for _, oid := range []string{strings.Repeat("b", 64), strings.Repeat("a", 40)} {
		if !captureOID(oid) {
			t.Fatal("valid OID rejected")
		}
	}
	for _, oid := range []string{strings.Repeat("0", 64), strings.Repeat("a", 39), strings.Repeat("A", 40)} {
		if captureOID(oid) {
			t.Fatal("invalid OID accepted")
		}
	}
	if _, err := parseCaptureListing([]byte("100644 blob "+strings.Repeat("b", 64)+"\ttasks/todo/a.md\x00"), "tasks", false); err != nil {
		t.Fatal(err)
	}
}

func TestCardsOnlyCaptureIndexIdentityAndGitDirectoryChange(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	binding, err := newCaptureBinding(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = captureCardsOnlyIndex(ctx, root, "tasks", func() error {
		data, err := os.ReadFile(binding.index)
		if err != nil {
			return err
		}
		if err := os.WriteFile(binding.index+".test-swap", data, 0600); err != nil {
			return err
		}
		return os.Rename(binding.index+".test-swap", binding.index)
	})
	if err == nil {
		t.Fatal("identical index bytes with replaced identity accepted")
	}
	binding, err = newCaptureBinding(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, "git-storage")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: git-storage\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := binding.verify(ctx, true); err == nil {
		t.Fatal("changed Git directory accepted")
	}
}

func TestCardsOnlyCaptureOpaqueGitMetadataAndSHA256(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	oid := captureTestGit(t, root, "rev-parse", "HEAD:tasks/todo/a.md")
	entries, err := captureBlobs(ctx, root, []captureLeaf{{name: ".git/private", mode: "100644", oid: oid}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != ".git" || !entries[0].DescendantTracked || len(entries[0].Data) != 0 {
		t.Fatalf("not opaque: %+v", entries)
	}
	shaRoot := t.TempDir()
	captureTestGit(t, shaRoot, "init", "-q", "--object-format=sha256")
	captureTestWrite(t, shaRoot, "tasks/todo/a.md", "sha256")
	captureTestGit(t, shaRoot, "add", ".")
	captureTestGit(t, shaRoot, "commit", "-qm", "fixture")
	c, err := CaptureCardsOnlyRef(ctx, shaRoot, "tasks", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.TreeOID) != 64 {
		t.Fatal("wrong SHA-256 identity")
	}
}

func TestCardsOnlyCaptureMetadataBudget(t *testing.T) {
	var listing strings.Builder
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("tasks/%03d/", i) + strings.Repeat("a/", 500) + "leaf.md"
		fmt.Fprintf(&listing, "100644 blob %s\t%s\x00", strings.Repeat("a", 40), name)
	}
	if _, err := parseCaptureListing([]byte(listing.String()), "tasks", false); err == nil {
		t.Fatal("inferred parent path metadata budget ignored")
	}
}
