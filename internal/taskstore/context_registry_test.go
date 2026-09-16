package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const testContextIntent = `{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":"Publish the release","outcome":"A reviewed release is available","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"published","text":"The release is published."}]}`

const testContextBatch = `{"schemaVersion":1,"kind":"batch","id":"BATCH-abcdef0123456789abcdef0123456789","revision":1,"intent":{"id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"digest":"04edcffd5de255854f0bdd4f68cc24e21ee8ad1d3a39b4645382f0d9066ad508"},"gap":"Publish the reviewed release","taskIds":["TASK-1"],"constraints":[],"authorizationRefs":[]}`

func TestContextRegistrySparseReplaysAndConflicts(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	rev7 := strings.Replace(testContextIntent, `"revision":1`, `"revision":7`, 1)
	if _, err := RegisterContext(board, []byte(rev7)); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	got, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 7)
	if err != nil || got.Status != "stored" || got.Revision != 7 {
		t.Fatalf("sparse show=%+v err=%v", got, err)
	}
	conflict := strings.Replace(testContextIntent, "Publish the release", "Different content", 1)
	if _, err := RegisterContext(board, []byte(conflict)); err == nil || !strings.Contains(err.Error(), "different immutable") {
		t.Fatalf("content conflict=%v", err)
	}
	if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 3); err == nil {
		t.Fatal("missing sparse revision was found")
	}
}

func TestContextRegistryBatchReferencesAndHistoricalShow(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "publish"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	batch, err := RegisterContext(board, []byte(testContextBatch))
	if err != nil || batch.ReferenceValidation != "verified" {
		t.Fatalf("batch=%+v err=%v", batch, err)
	}
	if batch.Path != ".task-manager-context/batches/BATCH-abcdef0123456789abcdef0123456789/1.json" {
		t.Fatalf("batch path=%q", batch.Path)
	}
	if err := os.Remove(filepath.Join(board, "todo", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	shown, err := ShowContext(board, "batch", "BATCH-abcdef0123456789abcdef0123456789", 1)
	if err != nil || shown.ReferenceValidation != "not_rechecked" || !bytes.Equal(shown.Canonical, batch.Canonical) {
		t.Fatalf("historical batch=%+v err=%v", shown, err)
	}
	if _, err := RegisterContext(board, []byte(testContextBatch)); err != nil {
		t.Fatal("batch replay should not recheck deleted task: ", err)
	}
	beforeFailedRegistration := boardBytes(t, board)
	newRevision := strings.Replace(testContextBatch, `"revision":1`, `"revision":2`, 1)
	if _, err := RegisterContext(board, []byte(newRevision)); err == nil || !strings.Contains(err.Error(), "TASK") {
		t.Fatalf("new batch revision without task accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeFailedRegistration, boardBytes(t, board)) {
		t.Fatal("missing task registration changed board")
	}
	badDigest := strings.Replace(testContextBatch, "04edcffd5de255854f0bdd4f68cc24e21ee8ad1d3a39b4645382f0d9066ad508", strings.Repeat("0", 64), 1)
	badDigest = strings.Replace(badDigest, `"revision":1`, `"revision":3`, 1)
	if _, err := RegisterContext(board, []byte(badDigest)); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("bad intent digest accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeFailedRegistration, boardBytes(t, board)) {
		t.Fatal("digest mismatch registration changed board")
	}
	missingBoard := filepath.Join(t.TempDir(), "tasks")
	if err := Init(missingBoard); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(missingBoard, []byte(testContextBatch)); err == nil || !strings.Contains(err.Error(), "intent") {
		t.Fatalf("missing intent accepted: %v", err)
	}
}

func TestContextRegistryRejectsCorruptNoncanonicalAndSymlinkedPaths(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	path, err := contextPath("intent", "INTENT-0123456789abcdef0123456789abcdef", 1)
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(board, filepath.FromSlash(path))
	if err := os.WriteFile(full, []byte(" {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 1); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt context accepted: %v", err)
	}
	if err := os.WriteFile(full, append([]byte(testContextIntent), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 1); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("noncanonical context accepted: %v", err)
	}
	wrongID := strings.Replace(testContextIntent, "INTENT-0123456789abcdef0123456789abcdef", "INTENT-abcdef0123456789abcdef0123456789", 1)
	if err := os.WriteFile(full, []byte(wrongID), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ShowContext(board, "intent", "INTENT-0123456789abcdef0123456789abcdef", 1); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("path identity mismatch accepted: %v", err)
	}

	for _, tc := range []struct {
		name  string
		setup func(string) error
	}{
		{"registry-root", func(board string) error {
			return os.Symlink(t.TempDir(), filepath.Join(board, contextDirectory))
		}},
		{"kind-directory", func(board string) error {
			root := filepath.Join(board, contextDirectory)
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			return os.Symlink(t.TempDir(), filepath.Join(root, "intents"))
		}},
		{"id-directory", func(board string) error {
			root := filepath.Join(board, contextDirectory, "intents")
			if err := os.MkdirAll(root, 0o755); err != nil {
				return err
			}
			return os.Symlink(t.TempDir(), filepath.Join(root, "INTENT-0123456789abcdef0123456789abcdef"))
		}},
		{"final-file", func(board string) error {
			file := filepath.Join(board, contextDirectory, "intents", "INTENT-0123456789abcdef0123456789abcdef", "1.json")
			if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
				return err
			}
			target := filepath.Join(t.TempDir(), "target.json")
			if err := os.WriteFile(target, []byte(testContextIntent), 0o600); err != nil {
				return err
			}
			return os.Symlink(target, file)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			board := filepath.Join(t.TempDir(), "tasks")
			if err := Init(board); err != nil {
				t.Fatal(err)
			}
			if err := tc.setup(board); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			if _, err := RegisterContext(board, []byte(testContextIntent)); err == nil {
				t.Fatal("symlink registry component accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("rejected symlink component changed board")
			}
		})
	}
}
