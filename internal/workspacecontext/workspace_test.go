package workspacecontext

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	board := filepath.Join(root, "tasks", "todo")
	if err := os.MkdirAll(board, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("---\nid: TASK-001\ntitle: one\npriority: P2\nstatus: pending\n---\n# One\n")
	if err := os.WriteFile(filepath.Join(board, "one.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestLookupIsOrderIndependentAndReadOnly(t *testing.T) {
	root := testRepo(t)
	before := treeBytes(t, root)
	other := testRepo(t)
	m1 := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "z", Root: root, Board: "tasks"}, {RepositoryID: "a", Root: other, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}
	m2 := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: other, Board: "tasks"}, {RepositoryID: "z", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}
	a, err := Lookup(context.Background(), m1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Lookup(context.Background(), m2)
	if err != nil {
		t.Fatal(err)
	}
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if !bytes.Equal(aa, bb) {
		t.Fatalf("output changed: %s vs %s", aa, bb)
	}
	if after := treeBytes(t, root); !bytes.Equal(before, after) {
		t.Fatal("lookup changed repository contents or metadata")
	}
	if a.Results[0].Status != "ambiguous" || a.Results[0].Matches[0].Path != "todo/one.md" {
		t.Fatalf("unexpected result: %#v", a)
	}
}

func TestLookupPreservesCollisionAndMissing(t *testing.T) {
	a, b := testRepo(t), testRepo(t)
	m := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: a, Board: "tasks"}, {RepositoryID: "b", Root: b, Board: "tasks"}}, CardIDs: []string{"TASK-1", "TASK-9"}}
	out, err := Lookup(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if out.Results[0].Status != "ambiguous" || len(out.Results[0].Matches) != 2 {
		t.Fatalf("collision lost: %#v", out.Results[0])
	}
	if out.Results[1].Status != "missing" || len(out.Results[1].Matches) != 0 {
		t.Fatalf("missing wrong: %#v", out.Results[1])
	}
}

func TestFingerprintDetectsSameContentReplacement(t *testing.T) {
	root := testRepo(t)
	before, err := fingerprint(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "tasks", "todo", "one.md")
	info, err := os.Stat(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(t.TempDir(), "one.md.old")
	if err := os.Rename(cardPath, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, []byte("---\nid: TASK-001\ntitle: one\npriority: P2\nstatus: pending\n---\n# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cardPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	after, err := fingerprint(filepath.Join(root, "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	if sameNodes(before.nodes, after.nodes) {
		t.Fatal("accepted replacement with same content and mtime")
	}
}

func TestManifestRejectsDuplicateIdentityAndUnsafeBoard(t *testing.T) {
	for _, raw := range []string{
		`{"schemaVersion":1,"repositories":[],"repositories":[],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"repositories":[{"RepositoryID":"a","root":"/tmp/x","board":"tasks"}],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp/x","board":"../tasks"}],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp/x","board":"tasks"}],"cardIds":["TASK-01","TASK-1"]}`,
	} {
		if _, err := DecodeManifest([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid manifest %s", raw)
		}
	}
}

func TestLookupRejectsAliasMalformedAndSymlinkBoundaries(t *testing.T) {
	root := testRepo(t)
	other := testRepo(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: alias, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted symlink root")
	}
	m := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}, {RepositoryID: "b", Root: alias, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}
	if _, err := Lookup(context.Background(), m); err == nil {
		t.Fatal("accepted canonical root alias")
	}
	bad := filepath.Join(root, "tasks", "todo", "bad.md")
	if err := os.WriteFile(bad, []byte("not a card"), 0o644); err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeOther := treeBytes(t, root), treeBytes(t, other)
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}, {RepositoryID: "b", Root: other, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted malformed card")
	}
	if afterRoot, afterOther := treeBytes(t, root), treeBytes(t, other); !bytes.Equal(beforeRoot, afterRoot) || !bytes.Equal(beforeOther, afterOther) {
		t.Fatal("error lookup changed repository state")
	}
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", ".git"), []byte("metadata"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted nested Git metadata")
	}
	if err := os.Remove(filepath.Join(root, "tasks", ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "tasks"), filepath.Join(root, "board-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "board-link"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted symlink board")
	}
	if err := os.Symlink(filepath.Join(root, "tasks", "todo", "one.md"), filepath.Join(root, "tasks", "todo", "alias.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted symlink card")
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "missing"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("accepted missing board")
	}
}

func treeBytes(t *testing.T, root string) []byte {
	t.Helper()
	var paths []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		paths = append(paths, rel+":"+info.Mode().String()+":"+string(mustRead(t, path, info.IsDir())))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return []byte(stringsJoin(paths))
}
func mustRead(t *testing.T, path string, dir bool) []byte {
	if dir {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func stringsJoin(v []string) string { return strings.Join(v, "\x00") }
