package taskstore

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func moduleRelocationRaw(t *testing.T) []byte {
	t.Helper()
	policy, err := boardpolicy.New(boardpolicy.Declaration{
		Relocations: []boardpolicy.Transition{{From: "plan", To: []string{"todo", "doing"}}, {From: "todo", To: []string{"plan"}}},
		KindStatus:  map[string]string{"plan": "pending"},
		Modules:     []string{"backend"},
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func moduleRelocationBoard(t *testing.T) string {
	t.Helper()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleRelocationRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	return board
}

func moduleRelocationRequestAt(t *testing.T, board, source, target string) RelocationRequest {
	t.Helper()
	r, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := r.ReadFile(source)
	if closeErr := r.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return RelocationRequest{
		ID:             "PLAN-2",
		Owner:          "worker",
		RequestID:      strings.Repeat("b", 32),
		Source:         source,
		Target:         target,
		ExpectedSHA256: bytesDigest(raw),
	}
}

func moduleRelocationRequest(t *testing.T, board, target string) RelocationRequest {
	return moduleRelocationRequestAt(t, board, "backend/plan/PLAN-2.md", target)
}

func modulePathWithLength(t *testing.T, length int, zone, filename string) string {
	t.Helper()
	prefix := "backend/" + zone
	remaining := length - len(prefix) - 2 - len(filename)
	if remaining <= 0 {
		t.Fatalf("invalid requested module path length %d", length)
	}
	var categories []string
	for remaining > 0 {
		if len(categories) > 0 {
			remaining--
		}
		part := remaining
		if part > 255 {
			part = 255
		}
		categories = append(categories, strings.Repeat("x", part))
		remaining -= part
	}
	return prefix + "/" + strings.Join(categories, "/") + "/" + filename
}

func moduleRootSnapshot(t *testing.T, board string) map[string]string {
	t.Helper()
	r, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	out := map[string]string{}
	err = fs.WalkDir(r.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode().IsRegular() {
			raw, err := fs.ReadFile(r.FS(), name)
			if err != nil {
				return err
			}
			value += string(raw)
		}
		out[name] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestModuleRelocationMovesCardUsingDeclaredPath(t *testing.T) {
	t.Parallel()
	board := moduleRelocationBoard(t)
	req := moduleRelocationRequest(t, board, "backend/todo/PLAN-2.md")
	result, err := Relocate(board, req, true)
	if err != nil || result.Status != "completed" || result.Target != req.Target {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(board, filepath.FromSlash(req.Source))); !os.IsNotExist(err) {
		t.Fatalf("source still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(board, filepath.FromSlash(req.Target))); err != nil {
		t.Fatalf("target missing: %v", err)
	}
}

func TestModuleRelocationRejectsOverlongTargetWithoutMutation(t *testing.T) {
	t.Parallel()
	board := moduleRelocationBoard(t)
	target := "backend/todo/" + strings.Repeat("x", 256) + "/PLAN-2.md"
	req := moduleRelocationRequest(t, board, target)
	before := boardBytes(t, board)
	if _, err := Relocate(board, req, true); err == nil || !strings.Contains(err.Error(), "255") {
		t.Fatalf("overlong target accepted or wrong error: %v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, board)) {
		t.Fatal("overlong target rejection mutated board")
	}
}

func TestModuleRelocationRejectsTargetBeyondTotalPathLimitWithoutMutation(t *testing.T) {
	t.Parallel()
	source := modulePathWithLength(t, 1023, "plan", "PLAN-3.md")
	if len(source) != 1023 {
		t.Fatalf("source length=%d", len(source))
	}
	policy, err := boardpolicy.Parse(moduleRelocationRaw(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := classifyModulePath(source, policy); err != nil {
		t.Fatalf("valid 1023-byte source rejected: %v", err)
	}
	board := moduleAdoptionBoard(t)
	r, err := os.OpenRoot(board)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.MkdirAll(filepath.ToSlash(filepath.Dir(source)), 0o755); err != nil {
		r.Close()
		t.Fatal(err)
	}
	card := []byte("---\nid: PLAN-3\ntitle: PLAN-3\nstatus: pending\n---\n\n# PLAN-3\n")
	if err := r.WriteFile(source, card, 0o640); err != nil {
		r.Close()
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ActivatePolicy(board, moduleRelocationRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	entries, err := List(board)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range entries {
		if entry.Path == source {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("1023-byte source was not discovered")
	}
	target := strings.Replace(source, "/plan/", "/doing/", 1)
	if len(target) != 1024 {
		t.Fatalf("target length=%d", len(target))
	}
	req := moduleRelocationRequestAt(t, board, source, target)
	req.ID = "PLAN-3"
	before := moduleRootSnapshot(t, board)
	if _, err := Relocate(board, req, true); err == nil || !strings.Contains(err.Error(), "invalid module card path") {
		t.Fatalf("overlong total path accepted or wrong error: %v", err)
	}
	if !reflect.DeepEqual(before, moduleRootSnapshot(t, board)) {
		t.Fatal("overlong total path rejection mutated board")
	}
}
