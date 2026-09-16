package taskstore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestInitListCreateAndAllocateAcrossArchive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "todo")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-4", Title: "Archived"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "todo/TASK-4.md"), filepath.Join(root, "archive/TASK-4.md")); err != nil {
		t.Fatal(err)
	}
	e, err := Create(root, CreateRequest{Title: "Next\nline"})
	if err != nil {
		t.Fatal(err)
	}
	if e.Card.ID != "TASK-5" || e.Path != "todo/TASK-5.md" {
		t.Fatalf("entry = %#v", e)
	}
	entries, err := List(root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("list = %#v err=%v", entries, err)
	}
	for _, item := range entries {
		if item.Path == e.Path && (item.Card.ID != e.Card.ID || item.Card.Title != e.Card.Title || item.Card.Status != e.Card.Status) {
			t.Fatalf("create/list mismatch: %#v vs %#v", e.Card, item.Card)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, e.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "title: |-") {
		t.Fatalf("title was not YAML encoded: %s", raw)
	}
}

func TestCreateRejectsInvalidDuplicateAndNoOverwrite(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-01", Title: "bad"}); err == nil {
		t.Fatal("leading zero accepted")
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "overwrite"}); err == nil {
		t.Fatal("duplicate accepted")
	}
	raw, _ := os.ReadFile(filepath.Join(root, "todo/TASK-1.md"))
	if strings.Contains(string(raw), "overwrite") {
		t.Fatal("existing card overwritten")
	}
}

func TestListRejectsMalformedDuplicateAndSymlink(t *testing.T) {
	for name, setup := range map[string]func(string) error{
		"malformed": func(root string) error {
			return os.WriteFile(filepath.Join(root, "todo/bad.md"), []byte("---\na: [\n---\n"), 0o644)
		},
		"duplicate": func(root string) error {
			for _, d := range []string{"todo", "done"} {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(root, d, "same.md"), []byte("---\nid: TASK-1\ntitle: x\n---\n"), 0o644); err != nil {
					return err
				}
			}
			return nil
		},
		"symlink": func(root string) error { return os.Symlink(filepath.Join(root, "todo"), filepath.Join(root, "doing")) },
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if err := setup(root); err != nil {
				t.Fatal(err)
			}
			if _, err := List(root); err == nil {
				t.Fatal("invalid board accepted")
			}
		})
	}
}

func TestListRejectsUnsupportedRootLayout(t *testing.T) {
	for _, name := range []string{"todos", "root-card.md"} {
		root := filepath.Join(t.TempDir(), "tasks")
		if err := Init(root); err != nil {
			t.Fatal(err)
		}
		if filepath.Ext(name) == ".md" {
			if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
		} else if err := os.Mkdir(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := List(root); err == nil {
			t.Fatalf("unsupported layout accepted: %s", name)
		}
	}
}

func TestLockIsFailFastAndNormalCleanup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".task-manager.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := List(root); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("lock error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, ".task-manager.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{Title: "ok"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".task-manager.lock")); !os.IsNotExist(err) {
		t.Fatalf("lock remained: %v", err)
	}
}

func TestRootSymlinkWithTrailingSeparatorIsRejected(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Init(link + string(os.PathSeparator)); err == nil {
		t.Fatal("Init accepted symlink root with trailing separator")
	}
	if _, err := List(link + string(os.PathSeparator)); err == nil {
		t.Fatal("List accepted symlink root with trailing separator")
	}
}

func TestStageWriteFailureRemovesOwnedStage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := "injected stage failure"
	_, err = stageWith(r, []byte("data"), func(*os.File, []byte) error { return errors.New(want) })
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".task-manager-stage-") {
			t.Fatalf("stage leaked: %s", entry.Name())
		}
	}
}

func TestConcurrentCreateProducesDistinctIDs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := Create(root, CreateRequest{Title: "concurrent"})
			if err == nil {
				mu.Lock()
				ids[e.Card.ID] = true
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(ids) == 0 {
		t.Fatal("all concurrent writers failed")
	}
	entries, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(ids) {
		t.Fatalf("file/id count mismatch: entries=%d ids=%d", len(entries), len(ids))
	}
}

func TestReadyDAGDonePrerequisiteAndKindExclusion(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "done"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "todo/TASK-1.md"), filepath.Join(root, "done/completed-card.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "ready", DependsOn: []string{"TASK-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-3", Title: "blocked", DependsOn: []string{"TASK-2"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "todo/TASK-3.md"), filepath.Join(root, "plan/TASK-3.md")); err != nil {
		t.Fatal(err)
	}
	ready, err := Ready(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("ready = %#v", ready)
	}
	// A done-status card in a non-actionable kind does not satisfy a dependency.
	if _, err := Create(root, CreateRequest{ID: "TASK-4", Title: "kind prerequisite"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plan"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "todo/TASK-4.md"), filepath.Join(root, "plan/TASK-4.md")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "plan/TASK-4.md"))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n")...)
	raw = []byte(strings.Replace(string(raw), "status: pending", "status: done", 1))
	if err := os.WriteFile(filepath.Join(root, "plan/TASK-4.md"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-5", Title: "blocked by kind", DependsOn: []string{"TASK-4"}}); err != nil {
		t.Fatal(err)
	}
	ready, err = Ready(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range ready {
		if entry.Card.ID == "TASK-5" {
			t.Fatal("kind prerequisite incorrectly satisfied")
		}
	}
}

func TestDependencyValidationRejectsMissingSelfDuplicateAndCycle(t *testing.T) {
	for name, deps := range map[string][]string{
		"missing": {"TASK-99"}, "self": {"TASK-1"}, "duplicate": {"TASK-2", "TASK-2"},
	} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "tasks")
			if err := Init(root); err != nil {
				t.Fatal(err)
			}
			if name == "duplicate" {
				if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "reference"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "x", DependsOn: deps}); err == nil {
				t.Fatal("invalid dependency accepted")
			} else if name == "duplicate" && !strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate error = %v", err)
			}
		})
	}
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "one", DependsOn: []string{"TASK-2"}}); err == nil {
		t.Fatal("missing dependency accepted")
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "two", DependsOn: []string{"TASK-1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-3", Title: "independent ready"}); err != nil {
		t.Fatal(err)
	}
	// Introduce a cycle through a hand-authored card; Ready must reject disconnected cycles too.
	if err := os.WriteFile(filepath.Join(root, "todo/TASK-1.md"), []byte("---\nid: TASK-1\ntitle: one\ndepends-on: [TASK-2]\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Ready(root); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestCreateDependenciesRoundTripAndEmptyReady(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	if ready, err := Ready(root); err != nil || ready == nil {
		t.Fatalf("empty ready = %#v err=%v", ready, err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "one", DependsOn: []string{"TASK-9"}}); err == nil {
		t.Fatal("unknown dependency accepted")
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-2", Title: "two", DependsOn: []string{"TASK-1"}}); err != nil {
		t.Fatal(err)
	}
	entries, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || len(entries[1].Card.DependsOn) != 1 {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestCreateInvalidDependencyLeavesBoardUnchanged(t *testing.T) {
	root := filepath.Join(t.TempDir(), "tasks")
	if err := Init(root); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(filepath.Join(root, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Create(root, CreateRequest{ID: "TASK-1", Title: "invalid", DependsOn: []string{"TASK-99"}}); err == nil {
		t.Fatal("missing dependency accepted")
	}
	after, err := os.ReadDir(filepath.Join(root, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid create changed todo: before=%d after=%d", len(before), len(after))
	}
	rootEntries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range rootEntries {
		if strings.HasPrefix(entry.Name(), ".task-manager-stage-") {
			t.Fatalf("invalid create leaked stage: %s", entry.Name())
		}
	}
}
