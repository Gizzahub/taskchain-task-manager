package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestListReadOnlyMatchesListWithoutPersistentWrites(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-9", Title: "read only"}); err != nil {
		t.Fatal(err)
	}
	want, err := List(board)
	if err != nil {
		t.Fatal(err)
	}
	before := readOnlyTreeState(t, board)
	beforeIdentity := readOnlyTreeIdentity(t, board)
	got, err := ListReadOnly(board)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("read-only entries = %#v, want %#v", got, want)
	}
	if after := readOnlyTreeState(t, board); !reflect.DeepEqual(after, before) {
		t.Fatalf("read-only listing changed board: before=%v after=%v", before, after)
	}
	if after := readOnlyTreeIdentity(t, board); !sameReadOnlyTreeIdentity(beforeIdentity, after) {
		t.Fatal("read-only listing changed file identity or metadata")
	}
	if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
		t.Fatalf("read-only listing left a writer lock: %v", err)
	}
}

func TestListReadOnlyLeavesMalformedBoardUntouched(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	badCard := filepath.Join(board, "todo", "broken.md")
	if err := os.WriteFile(badCard, []byte("---\nid: [\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readOnlyTreeState(t, board)
	beforeIdentity := readOnlyTreeIdentity(t, board)
	if _, err := ListReadOnly(board); err == nil {
		t.Fatal("malformed board was accepted")
	}
	if after := readOnlyTreeState(t, board); !reflect.DeepEqual(after, before) {
		t.Fatalf("failed read-only listing changed board: before=%v after=%v", before, after)
	}
	if after := readOnlyTreeIdentity(t, board); !sameReadOnlyTreeIdentity(beforeIdentity, after) {
		t.Fatal("failed read-only listing changed file identity or metadata")
	}
	if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
		t.Fatalf("failed read-only listing left a writer lock: %v", err)
	}
}

func TestListReadOnlyRefusesExistingLockWithoutRemovingIt(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(board, ".task-manager.lock")
	if err := os.Mkdir(lockPath, 0o700); err != nil {
		t.Fatal(err)
	}
	before := readOnlyTreeState(t, board)
	beforeIdentity := readOnlyTreeIdentity(t, board)
	if _, err := ListReadOnly(board); err == nil || !strings.Contains(strings.ToLower(err.Error()), "locked") {
		t.Fatalf("existing writer lock was bypassed: %v", err)
	}
	if after := readOnlyTreeState(t, board); !reflect.DeepEqual(after, before) {
		t.Fatal("read-only lock refusal changed board")
	}
	if after := readOnlyTreeIdentity(t, board); !sameReadOnlyTreeIdentity(beforeIdentity, after) {
		t.Fatal("read-only lock refusal changed file identity or metadata")
	}
	if info, err := os.Lstat(lockPath); err != nil || !info.IsDir() {
		t.Fatalf("read-only lock refusal removed lock: %v", err)
	}
}

func TestListReadOnlyRejectsPendingTransitionWithoutWrites(t *testing.T) {
	t.Parallel()
	board, request, _, _ := transitionFixture(t)
	stop := errors.New("synthetic interruption")
	if _, err := transitionWithStep(board, request, func(at string) error {
		if at == "after-journal" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("pending transition fixture: %v", err)
	}
	before := readOnlyTreeState(t, board)
	beforeIdentity := readOnlyTreeIdentity(t, board)
	if _, err := ListReadOnly(board); err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("pending transition was accepted: %v", err)
	}
	if after := readOnlyTreeState(t, board); !reflect.DeepEqual(after, before) {
		t.Fatal("pending transition rejection changed board files")
	}
	if after := readOnlyTreeIdentity(t, board); !sameReadOnlyTreeIdentity(beforeIdentity, after) {
		t.Fatal("pending transition rejection changed file identity or metadata")
	}
	if _, err := os.Lstat(filepath.Join(board, ".task-manager.lock")); !os.IsNotExist(err) {
		t.Fatalf("pending transition rejection left writer lock: %v", err)
	}
}

func TestListReadOnlyRejectsMalformedSharedStateWithoutWrites(t *testing.T) {
	t.Parallel()
	_, _, board := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	session, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	namespace := filepath.Join(session.location.CommonDirectory, "taskchain-task-manager", "ids", session.location.NamespaceKey)
	statePath := filepath.Join(namespace, sharedStateFile)
	if err := os.WriteFile(statePath, []byte("{"), 0o600); err != nil {
		_ = release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	boardBefore, commonBefore := readOnlyTreeState(t, board), readOnlyTreeState(t, namespace)
	boardIdentity, commonIdentity := readOnlyTreeIdentity(t, board), readOnlyTreeIdentity(t, namespace)
	if _, err := ListReadOnly(board); err == nil {
		t.Fatal("malformed common state was accepted")
	}
	if after := readOnlyTreeState(t, board); !reflect.DeepEqual(after, boardBefore) {
		t.Fatal("malformed common state rejection changed board")
	}
	if after := readOnlyTreeState(t, namespace); !reflect.DeepEqual(after, commonBefore) {
		t.Fatal("malformed common state rejection changed common state")
	}
	if !sameReadOnlyTreeIdentity(boardIdentity, readOnlyTreeIdentity(t, board)) || !sameReadOnlyTreeIdentity(commonIdentity, readOnlyTreeIdentity(t, namespace)) {
		t.Fatal("malformed common state rejection changed file identity or metadata")
	}
	for _, lockPath := range []string{filepath.Join(board, ".task-manager.lock"), filepath.Join(namespace, ".task-manager.lock")} {
		if _, err := os.Lstat(lockPath); !os.IsNotExist(err) {
			t.Fatalf("read-only shared validation left writer lock %s: %v", lockPath, err)
		}
	}
}

func readOnlyTreeState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			state[filepath.ToSlash(rel)] = "directory"
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		state[filepath.ToSlash(rel)] = string(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func readOnlyTreeIdentity(t *testing.T, root string) map[string]os.FileInfo {
	t.Helper()
	nodes := map[string]os.FileInfo{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		nodes[filepath.ToSlash(rel)] = info
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return nodes
}

func sameReadOnlyTreeIdentity(left, right map[string]os.FileInfo) bool {
	if len(left) != len(right) {
		return false
	}
	for path, leftInfo := range left {
		rightInfo, ok := right[path]
		if !ok || !os.SameFile(leftInfo, rightInfo) || leftInfo.Mode() != rightInfo.Mode() || leftInfo.Size() != rightInfo.Size() || leftInfo.ModTime() != rightInfo.ModTime() {
			return false
		}
	}
	return true
}
