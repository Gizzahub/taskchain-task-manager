package workspacecontext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	if out.Results[0].Matches[0].RepositoryID != "a" || out.Results[0].Matches[1].RepositoryID != "b" {
		t.Fatalf("collision repository identity lost: %#v", out.Results[0].Matches)
	}
	if out.Results[1].Status != "missing" || len(out.Results[1].Matches) != 0 {
		t.Fatalf("missing wrong: %#v", out.Results[1])
	}
}

func TestScanRejectsObservedMutation(t *testing.T) {
	root := testRepo(t)
	previous := scanAfterReadHook
	defer func() { scanAfterReadHook = previous }()
	scanAfterReadHook = func(base string) {
		_ = os.WriteFile(filepath.Join(base, "todo", "changed.md"), []byte("---\nid: TASK-2\n---\n"), 0o644)
	}
	before := treeBytes(t, root)
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("mutation was not rejected: %v", err)
	}
	if after := treeBytes(t, root); bytes.Equal(before, after) {
		t.Fatal("mutation fixture did not change board")
	}
}

func TestCorruptClaimsFailsWithoutWrites(t *testing.T) {
	root, other := testRepo(t), testRepo(t)
	if err := os.WriteFile(filepath.Join(root, "tasks", ".task-manager-claims.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforeRoot, beforeOther := treeBytes(t, root), treeBytes(t, other)
	_, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}, {RepositoryID: "b", Root: other, Board: "tasks"}}, CardIDs: []string{"TASK-1"}})
	if err == nil || !strings.Contains(err.Error(), "claims") {
		t.Fatalf("corrupt claims accepted: %v", err)
	}
	if afterRoot, afterOther := treeBytes(t, root), treeBytes(t, other); !bytes.Equal(beforeRoot, afterRoot) || !bytes.Equal(beforeOther, afterOther) {
		t.Fatal("claims error changed repository state")
	}
}

func TestLookupRejectsLinkedWorktreeCommonDirectoryDuplicate(t *testing.T) {
	root := testRepo(t)
	for _, args := range [][]string{{"config", "user.email", "test@example.invalid"}, {"config", "user.name", "Test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", root, "worktree", "add", "-q", linked, "HEAD").CombinedOutput(); err != nil {
		t.Fatalf("worktree add: %v: %s", err, out)
	}
	defer exec.Command("git", "-C", root, "worktree", "remove", "--force", linked).Run()
	_, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}, {RepositoryID: "b", Root: linked, Board: "tasks"}}, CardIDs: []string{"TASK-1"}})
	if err == nil || !strings.Contains(err.Error(), "common directory") {
		t.Fatalf("linked worktree duplicate accepted: %v", err)
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
	dirInfo, err := os.Stat(filepath.Dir(cardPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cardPath, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cardPath, []byte("---\nid: TASK-001\ntitle: one\npriority: P2\nstatus: pending\n---\n# One\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cardPath, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Dir(cardPath), dirInfo.ModTime(), dirInfo.ModTime()); err != nil {
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
		`{"schemaVersion":1,"SchemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp/x","board":"tasks"}],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"SchemaVersion":1,"repositories":[],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp/x","board":"../tasks"}],"cardIds":["TASK-1"]}`,
		`{"schemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp/x","board":"tasks"}],"cardIds":["TASK-01","TASK-1"]}`,
	} {
		if _, err := DecodeManifest([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid manifest %s", raw)
		}
	}
}

func TestManifestBounds(t *testing.T) {
	base := []byte(`{"schemaVersion":1,"repositories":[{"repositoryId":"a","root":"/tmp","board":"tasks"}],"cardIds":["TASK-1"]}`)
	accepted := append(append([]byte{}, base...), bytes.Repeat([]byte(" "), MaxManifestBytes-len(base))...)
	if _, err := DecodeManifest(accepted); err != nil {
		t.Fatalf("rejected exact manifest bound: %v", err)
	}
	if _, err := DecodeManifest(append(accepted, ' ')); err == nil {
		t.Fatal("accepted oversized manifest")
	}
	tooManyRepos := Manifest{SchemaVersion: 1, Repositories: make([]Repository, MaxRepositories+1), CardIDs: []string{"TASK-1"}}
	if err := validateManifest(tooManyRepos); err == nil {
		t.Fatal("accepted repository bound")
	}
	tooManyIDs := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: "/tmp", Board: "tasks"}}, CardIDs: make([]string, MaxQueryIDs+1)}
	if err := validateManifest(tooManyIDs); err == nil {
		t.Fatal("accepted query bound")
	}
	longID := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: "/tmp", Board: "tasks"}}, CardIDs: []string{"TASK-" + strings.Repeat("1", MaxIDBytes)}}
	if err := validateManifest(longID); err == nil {
		t.Fatal("accepted ID byte bound")
	}
	exactRepos := make([]Repository, MaxRepositories)
	for i := range exactRepos {
		exactRepos[i] = Repository{RepositoryID: fmt.Sprintf("r%d", i), Root: "/tmp", Board: "tasks"}
	}
	if err := validateManifest(Manifest{SchemaVersion: 1, Repositories: exactRepos, CardIDs: []string{"TASK-1"}}); err != nil {
		t.Fatalf("rejected exact repository bound: %v", err)
	}
	exactIDs := make([]string, MaxQueryIDs)
	for i := range exactIDs {
		exactIDs[i] = fmt.Sprintf("TASK-%d", i+1)
	}
	if err := validateManifest(Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: "/tmp", Board: "tasks"}}, CardIDs: exactIDs}); err != nil {
		t.Fatalf("rejected exact query bound: %v", err)
	}
}

func TestTypedManifestRoundTripAndDirectLookupValidation(t *testing.T) {
	root := testRepo(t)
	original := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-1"}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Repositories[0].RepositoryID != "a" {
		t.Fatal("repository JSON tags did not round-trip")
	}
	for _, invalid := range []Manifest{{SchemaVersion: 2, Repositories: original.Repositories, CardIDs: original.CardIDs}, {SchemaVersion: 1, Repositories: nil, CardIDs: original.CardIDs}, {SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "../tasks"}}, CardIDs: original.CardIDs}, {SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: root, Board: "tasks"}}, CardIDs: []string{"TASK-" + string([]byte{0xff})}}} {
		if _, err := Lookup(context.Background(), invalid); err == nil {
			t.Fatal("direct Lookup accepted invalid manifest")
		}
	}
	if _, err := Lookup(context.Background(), Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: strings.Repeat("/", MaxManifestBytes+1), Board: "tasks"}}, CardIDs: []string{"TASK-1"}}); err == nil {
		t.Fatal("direct Lookup accepted oversized root")
	}
	heavy := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: "/" + strings.Repeat("&", 100000), Board: "tasks"}}, CardIDs: []string{"TASK-1"}}
	if _, err := Lookup(context.Background(), heavy); err == nil {
		t.Fatal("direct Lookup accepted HTML-heavy oversized manifest")
	}
}

func TestManifestJSONLengthMatchesMarshal(t *testing.T) {
	m := Manifest{SchemaVersion: 1, Repositories: []Repository{{RepositoryID: "a", Root: `/tmp/<>\\&`, Board: "tasks"}}, CardIDs: []string{"TASK-1", "TASK-2"}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := manifestJSONLen(m); got != len(raw) {
		t.Fatalf("manifestJSONLen=%d, marshal=%d, raw=%s", got, len(raw), raw)
	}
	for _, value := range []string{"quote\"", "slash\\", "control\n", "html<&>", "\u2028\u2029"} {
		if got := jsonStringLen(value); got != len(mustJSONQuote(t, value)) {
			t.Fatalf("jsonStringLen(%q)=%d", value, got)
		}
	}
}

func mustJSONQuote(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBoardScanBounds(t *testing.T) {
	tooLarge := t.TempDir()
	if err := os.Mkdir(filepath.Join(tooLarge, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(tooLarge, "tasks", "large.bin")
	if err := os.WriteFile(large, []byte{0}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, MaxBoardBytes); err != nil {
		t.Fatal(err)
	}
	if err := preflightBoard(filepath.Join(tooLarge, "tasks")); err != nil {
		t.Fatalf("rejected exact board byte bound: %v", err)
	}
	if _, err := fingerprint(filepath.Join(tooLarge, "tasks")); err != nil {
		t.Fatalf("fingerprint rejected exact board byte bound: %v", err)
	}
	if err := os.Truncate(large, MaxBoardBytes+1); err != nil {
		t.Fatal(err)
	}
	if err := preflightBoard(filepath.Join(tooLarge, "tasks")); err == nil {
		t.Fatal("accepted board byte bound")
	}
	tooManyCards := t.TempDir()
	cardDir := filepath.Join(tooManyCards, "tasks", "todo")
	if err := os.MkdirAll(cardDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxCardsPerBoard; i++ {
		if err := os.WriteFile(filepath.Join(cardDir, fmt.Sprintf("%d.md", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := preflightBoard(filepath.Join(tooManyCards, "tasks")); err != nil {
		t.Fatalf("rejected exact card bound: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cardDir, fmt.Sprintf("%d.md", MaxCardsPerBoard)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflightBoard(filepath.Join(tooManyCards, "tasks")); err == nil {
		t.Fatal("accepted card bound")
	}
	tooManyNodes := t.TempDir()
	nodeDir := filepath.Join(tooManyNodes, "tasks")
	if err := os.Mkdir(nodeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxBoardNodes-1; i++ {
		if err := os.WriteFile(filepath.Join(nodeDir, fmt.Sprintf("%d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := preflightBoard(nodeDir); err != nil {
		t.Fatalf("rejected exact node bound: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodeDir, fmt.Sprintf("%d.txt", MaxBoardNodes)), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := preflightBoard(nodeDir); err == nil {
		t.Fatal("accepted node bound")
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
		paths = append(paths, rel+":"+info.Mode().String()+":"+info.ModTime().UTC().String()+":"+string(mustRead(t, path, info.IsDir())))
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
