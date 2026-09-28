package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func snapshotTestRepository(t *testing.T, parent, name string) (string, string) {
	t.Helper()
	root := filepath.Join(parent, name)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	board := filepath.Join(root, "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	return root, board
}

func snapshotManifest(repositories ...Repository) Manifest {
	return Manifest{Repositories: repositories}
}

func TestQuerySnapshotClassifiesAllMatchesInStableOrderWithoutWrites(t *testing.T) {
	root := t.TempDir()
	alphaRoot, alphaBoard := snapshotTestRepository(t, root, "alpha")
	betaRoot, betaBoard := snapshotTestRepository(t, root, "beta")
	if _, err := taskstore.Create(alphaBoard, taskstore.CreateRequest{ID: "TASK-1", Title: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(alphaBoard, taskstore.CreateRequest{ID: "TASK-2", Title: "only alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(betaBoard, taskstore.CreateRequest{ID: "TASK-1", Title: "beta"}); err != nil {
		t.Fatal(err)
	}
	manifest := snapshotManifest(
		Repository{Name: "beta", Path: betaRoot, Board: betaBoard},
		Repository{Name: "alpha", Path: alphaRoot, Board: alphaBoard},
	)
	reversed := snapshotManifest(manifest.Repositories[1], manifest.Repositories[0])
	requested := []string{"TASK-0001", "TASK-2", "TASK-3"}
	beforeAlpha, beforeBeta := snapshotTree(t, alphaRoot), snapshotTree(t, betaRoot)
	got, err := QuerySnapshot(manifest, requested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := QuerySnapshot(reversed, requested)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotJSON, wantJSON) {
		t.Fatalf("repository order changed output: %s vs %s", gotJSON, wantJSON)
	}
	if len(got.Results) != 3 {
		t.Fatalf("output = %#v", got)
	}
	collision := got.Results[0]
	if collision.RequestedID != "TASK-0001" || collision.CardID != "TASK-1" || collision.Status != "ambiguous" || len(collision.Matches) != 2 {
		t.Fatalf("collision = %#v", collision)
	}
	if collision.Matches[0].Repository != "alpha" || collision.Matches[1].Repository != "beta" {
		t.Fatalf("matches are not sorted: %#v", collision.Matches)
	}
	if found := got.Results[1]; found.Status != "found" || found.Matches[0].Repository != "alpha" || found.Matches[0].Path != "todo/TASK-2.md" {
		t.Fatalf("found = %#v", found)
	}
	if missing := got.Results[2]; missing.Status != "missing" || len(missing.Matches) != 0 {
		t.Fatalf("missing = %#v", missing)
	}
	if afterAlpha, afterBeta := snapshotTree(t, alphaRoot), snapshotTree(t, betaRoot); !beforeAlpha.same(afterAlpha) || !beforeBeta.same(afterBeta) {
		t.Fatal("successful snapshot changed repository files or metadata")
	}
	for _, board := range []string{alphaBoard, betaBoard} {
		if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
			t.Fatalf("snapshot left writer lock in %s: %v", board, err)
		}
	}
}

func TestQuerySnapshotErrorsWithoutPartialResultOrWrites(t *testing.T) {
	root := t.TempDir()
	alphaRoot, alphaBoard := snapshotTestRepository(t, root, "alpha")
	betaRoot, betaBoard := snapshotTestRepository(t, root, "beta")
	if _, err := taskstore.Create(alphaBoard, taskstore.CreateRequest{ID: "TASK-1", Title: "valid"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(betaBoard, ".task-manager-claims.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := snapshotManifest(
		Repository{Name: "alpha", Path: alphaRoot, Board: alphaBoard},
		Repository{Name: "beta", Path: betaRoot, Board: betaBoard},
	)
	beforeAlpha, beforeBeta := snapshotTree(t, alphaRoot), snapshotTree(t, betaRoot)
	if got, err := QuerySnapshot(manifest, []string{"TASK-1"}); err == nil || len(got.Results) != 0 || !strings.Contains(err.Error(), "claims") {
		t.Fatalf("corrupt claims returned output=%#v err=%v", got, err)
	}
	if afterAlpha, afterBeta := snapshotTree(t, alphaRoot), snapshotTree(t, betaRoot); !beforeAlpha.same(afterAlpha) || !beforeBeta.same(afterBeta) {
		t.Fatal("error snapshot changed repository files or metadata")
	}
}

func TestQuerySnapshotRejectsInvalidAndAliasedIDsAndBounds(t *testing.T) {
	root, board := snapshotTestRepository(t, t.TempDir(), "repo")
	manifest := snapshotManifest(Repository{Name: "repo", Path: root, Board: board})
	maxLengthID := "TASK-" + strings.Repeat("0", SnapshotMaxIDBytes-len("TASK-")-1) + "1"
	if len(maxLengthID) != SnapshotMaxIDBytes {
		t.Fatalf("fixture ID length = %d", len(maxLengthID))
	}
	if _, err := QuerySnapshot(manifest, []string{maxLengthID}); err != nil {
		t.Fatalf("accepted valid ID at exact byte bound: %v", err)
	}
	withinLimit := make([]string, SnapshotMaxQueryIDs)
	for i := range withinLimit {
		withinLimit[i] = fmt.Sprintf("TASK-%d", i)
	}
	within, err := QuerySnapshot(manifest, withinLimit)
	if err != nil || len(within.Results) != SnapshotMaxQueryIDs {
		t.Fatalf("exact query bound rejected: results=%d err=%v", len(within.Results), err)
	}
	tooManyIDs := append(append([]string(nil), withinLimit...), "TASK-999")
	tooLongID := maxLengthID + "0"
	for _, ids := range [][]string{
		{},
		{"TASK-001", "TASK-1"},
		{tooLongID},
		tooManyIDs,
	} {
		if _, err := QuerySnapshot(manifest, ids); err == nil {
			t.Fatalf("accepted invalid snapshot IDs: %d IDs, first=%q", len(ids), firstSnapshotValue(ids))
		}
	}
	if err := validateSnapshotRepositoryCount(SnapshotMaxRepositories); err != nil {
		t.Fatalf("rejected exact repository count bound: %v", err)
	}
	if err := validateSnapshotRepositoryCount(SnapshotMaxRepositories + 1); err == nil {
		t.Fatal("accepted repository count above the snapshot bound")
	}
}

func TestQuerySnapshotRejectsSymlinkAndEscapingPaths(t *testing.T) {
	root, board := snapshotTestRepository(t, t.TempDir(), "repo")
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := QuerySnapshot(snapshotManifest(Repository{Name: "repo", Path: root, Board: outside}), []string{"TASK-1"}); err == nil {
		t.Fatal("accepted board outside its repository")
	}
	rootAlias := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(root, rootAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := QuerySnapshot(snapshotManifest(Repository{Name: "repo", Path: rootAlias, Board: filepath.Join(rootAlias, "tasks")}), []string{"TASK-1"}); err == nil {
		t.Fatal("accepted symlink repository root")
	}
	boardAlias := filepath.Join(root, "board-link")
	if err := os.Symlink(board, boardAlias); err != nil {
		t.Fatal(err)
	}
	if _, err := QuerySnapshot(snapshotManifest(Repository{Name: "repo", Path: root, Board: boardAlias}), []string{"TASK-1"}); err == nil {
		t.Fatal("accepted symlink board")
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "symlink"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(board, "todo", "TASK-1.md"), filepath.Join(board, "todo", "alias.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := QuerySnapshot(snapshotManifest(Repository{Name: "repo", Path: root, Board: board}), []string{"TASK-1"}); err == nil {
		t.Fatal("accepted symlink card")
	}
}

func TestQuerySnapshotDetectsObservedMutationDuringScan(t *testing.T) {
	root, board := snapshotTestRepository(t, t.TempDir(), "repo")
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "before"}); err != nil {
		t.Fatal(err)
	}
	manifest := snapshotManifest(Repository{Name: "repo", Path: root, Board: board})
	_, err := querySnapshot(manifest, []string{"TASK-1"}, func(boardPath string) {
		_ = os.WriteFile(filepath.Join(boardPath, "todo", "changed.md"), []byte("---\nid: TASK-2\ntitle: after\n---\n"), 0o644)
	})
	if err == nil || !strings.Contains(err.Error(), "changed during inspection") {
		t.Fatalf("observed board mutation was accepted: %v", err)
	}
}

func TestQuerySnapshotDetectsSameContentFileReplacement(t *testing.T) {
	_, board := snapshotTestRepository(t, t.TempDir(), "repo")
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "same"}); err != nil {
		t.Fatal(err)
	}
	before, err := fingerprintSnapshotBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(board, "todo", "TASK-1.md")
	parentPath := filepath.Dir(cardPath)
	parentInfo, err := os.Stat(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old-card.md")
	if err := os.Rename(cardPath, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, raw, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cardPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(parentPath, parentInfo.ModTime(), parentInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := fingerprintSnapshotBoard(board)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.digest, after.digest) {
		t.Fatal("replacement fixture changed the content digest")
	}
	if sameSnapshotNodes(before.nodes, after.nodes) {
		t.Fatal("same-content file replacement was not detected by file identity")
	}
}

func TestQuerySnapshotRejectsUnsafeBoardAndDuplicateGitCommonDir(t *testing.T) {
	root, board := snapshotTestRepository(t, t.TempDir(), "root")
	if err := os.WriteFile(filepath.Join(board, ".git"), []byte("nested metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := QuerySnapshot(snapshotManifest(Repository{Name: "root", Path: root, Board: board}), []string{"TASK-1"}); err == nil {
		t.Fatal("accepted nested Git metadata")
	}
	if err := os.Remove(filepath.Join(board, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "linked"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Snapshot test"}, {"add", "."}, {"commit", "-qm", "snapshot fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", "--detach", linked, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	linked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("git", "-C", root, "worktree", "remove", "--force", linked).Run() })
	manifest := snapshotManifest(
		Repository{Name: "root", Path: root, Board: filepath.Join(root, "tasks")},
		Repository{Name: "linked", Path: linked, Board: filepath.Join(linked, "tasks")},
	)
	if _, err := QuerySnapshot(manifest, []string{"TASK-1"}); err == nil || !strings.Contains(err.Error(), "common directory") {
		t.Fatalf("accepted duplicate Git common directory: %v", err)
	}
}

func TestQuerySnapshotDetectsSameContentReplacementDuringScan(t *testing.T) {
	root, board := snapshotTestRepository(t, t.TempDir(), "repo")
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "same"}); err != nil {
		t.Fatal(err)
	}
	manifest := snapshotManifest(Repository{Name: "repo", Path: root, Board: board})
	before := snapshotTree(t, root)
	result, err := querySnapshot(manifest, []string{"TASK-1"}, func(string) {
		replaceFileWithSameContent(t, filepath.Join(board, "todo", "TASK-1.md"))
	})
	if err == nil || !strings.Contains(err.Error(), "changed during inspection") || len(result.Results) != 0 {
		t.Fatalf("replacement returned partial/accepted output=%#v err=%v", result, err)
	}
	after := snapshotTree(t, root)
	if !bytes.Equal(before.bytes, after.bytes) {
		t.Fatal("same-content replacement fixture changed the content/metadata digest")
	}
	if sameSnapshotNodes(before.nodes, after.nodes) {
		t.Fatal("same-content replacement fixture did not change file identity")
	}
}

func TestPreflightSnapshotBoardEnforcesCardNodeAndByteBounds(t *testing.T) {
	cardBoard := filepath.Join(t.TempDir(), "card-board")
	if err := os.MkdirAll(filepath.Join(cardBoard, "todo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < SnapshotMaxCardsPerBoard; i++ {
		if err := os.WriteFile(filepath.Join(cardBoard, "todo", fmt.Sprintf("%d.md", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := preflightSnapshotBoard(cardBoard); err != nil {
		t.Fatalf("rejected exact card bound: %v", err)
	}
	if _, err := fingerprintSnapshotBoard(cardBoard); err != nil {
		t.Fatalf("fingerprint rejected exact card bound: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cardBoard, "todo", fmt.Sprintf("%d.md", SnapshotMaxCardsPerBoard)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preflightSnapshotBoard(cardBoard); err == nil {
		t.Fatal("accepted card count above the bound")
	}
	if _, err := fingerprintSnapshotBoard(cardBoard); err == nil {
		t.Fatal("fingerprint accepted card count above the bound")
	}

	nodeBoard := filepath.Join(t.TempDir(), "node-board")
	if err := os.Mkdir(nodeBoard, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < SnapshotMaxBoardNodes-1; i++ { // root plus files
		if err := os.WriteFile(filepath.Join(nodeBoard, fmt.Sprintf("%d.txt", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := preflightSnapshotBoard(nodeBoard); err != nil {
		t.Fatalf("rejected exact node bound: %v", err)
	}
	if _, err := fingerprintSnapshotBoard(nodeBoard); err != nil {
		t.Fatalf("fingerprint rejected exact node bound: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodeBoard, fmt.Sprintf("%d.txt", SnapshotMaxBoardNodes-1)), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preflightSnapshotBoard(nodeBoard); err == nil {
		t.Fatal("accepted node count above the bound")
	}
	if _, err := fingerprintSnapshotBoard(nodeBoard); err == nil {
		t.Fatal("fingerprint accepted node count above the bound")
	}

	byteBoard := filepath.Join(t.TempDir(), "byte-board")
	if err := os.Mkdir(byteBoard, 0o755); err != nil {
		t.Fatal(err)
	}
	large, err := os.Create(filepath.Join(byteBoard, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(SnapshotMaxBoardBytes); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	if err := preflightSnapshotBoard(byteBoard); err != nil {
		t.Fatalf("rejected exact byte bound: %v", err)
	}
	if _, err := fingerprintSnapshotBoard(byteBoard); err != nil {
		t.Fatalf("fingerprint rejected exact byte bound: %v", err)
	}
	if err := os.Truncate(filepath.Join(byteBoard, "large.bin"), SnapshotMaxBoardBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := preflightSnapshotBoard(byteBoard); err == nil {
		t.Fatal("accepted bytes above the bound")
	}
	if _, err := fingerprintSnapshotBoard(byteBoard); err == nil {
		t.Fatal("fingerprint accepted bytes above the bound")
	}
}

type snapshotTreeState struct {
	bytes []byte
	nodes map[string]os.FileInfo
}

func snapshotTree(t *testing.T, root string) snapshotTreeState {
	t.Helper()
	var paths []string
	nodes := map[string]os.FileInfo{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		var contents []byte
		if !info.IsDir() {
			contents, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		paths = append(paths, rel+":"+info.Mode().String()+":"+info.ModTime().UTC().String()+":"+string(contents))
		nodes[filepath.ToSlash(rel)] = info
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshotTreeState{bytes: []byte(strings.Join(paths, "\x00")), nodes: nodes}
}

func (state snapshotTreeState) same(other snapshotTreeState) bool {
	return bytes.Equal(state.bytes, other.bytes) && sameSnapshotNodes(state.nodes, other.nodes)
}

func replaceFileWithSameContent(t *testing.T, path string) {
	t.Helper()
	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(t.TempDir(), "old-card.md")
	if err := os.Rename(path, oldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, info.Mode().Perm()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(parent, parentInfo.ModTime(), parentInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func firstSnapshotValue(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
