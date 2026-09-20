package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func publicationRequest(t *testing.T, board string) []byte {
	t.Helper()
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	req, _ := bundleFixture(t)
	req.Tasks[0].DependsOn = []intentdoc.TaskReference{{Key: "second"}}
	req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: "second", Title: "Second", DependsOn: []intentdoc.TaskReference{}})
	raw, err := parsedBundle(t, req).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBundlePublishAndHistoricalReplay(t *testing.T) {
	t.Parallel()
	board := configuredFixture(t)
	raw := publicationRequest(t, board)
	before := boardBytes(t, board)
	if _, err := PublishBundle(board, raw, BundleOptions{}); err == nil {
		t.Fatal("implicit protocol upgrade accepted")
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("implicit adoption changed board")
	}
	result, err := PublishBundle(board, raw, BundleOptions{Adopt: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Replayed || len(result.Tasks) != 2 || result.Tasks[0].ID != "TASK-1" || result.Tasks[1].ID != "TASK-2" {
		t.Fatalf("result=%+v", result)
	}
	entries, err := List(board)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
	ready, err := Ready(board)
	if err != nil || len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
	var batch intentdoc.Batch
	if err := json.Unmarshal(result.Batch, &batch); err != nil {
		t.Fatal(err)
	}
	stored, err := ShowContext(board, "batch", batch.ID, batch.Revision)
	if err != nil || !bytes.Equal(stored.Canonical, result.Batch) {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	// Historical replay is receipt retrieval, not current graph validation.
	if err := os.Remove(filepath.Join(board, result.Tasks[1].Path)); err != nil {
		t.Fatal(err)
	}
	before = boardBytes(t, board)
	replayed, err := PublishBundle(board, raw, BundleOptions{})
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Tasks, result.Tasks) {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("historical replay recreated cards")
	}
	changed := bytes.Replace(raw, []byte(`"First"`), []byte(`"Changed"`), 1)
	if bytes.Equal(raw, changed) {
		t.Fatal("request mutation did not apply")
	}
	if _, err := PublishBundle(board, changed, BundleOptions{}); err == nil {
		t.Fatal("request ID reused with different content")
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("conflicting request changed board")
	}
}

func TestBundlePublishResumesEveryPhase(t *testing.T) {
	t.Parallel()
	phases := []string{"after-empty-journal", "after-local-protocol", "after-pending-journal", "after-shared-reservation", "after-local-reservation", "after-card-0", "after-card-1", "after-batch", "after-completed-receipt", "after-common-clear"}
	for _, shared := range []bool{false, true} {
		for _, phase := range phases {
			t.Run(map[bool]string{false: "local", true: "shared"}[shared]+"/"+phase, func(t *testing.T) {
				var board string
				if shared {
					_, board, _ = sharedFixture(t)
					if _, err := EnableShared(board, false); err != nil {
						t.Fatal(err)
					}
				} else {
					board = configuredFixture(t)
				}
				raw := publicationRequest(t, board)
				stop := errors.New("synthetic interruption")
				_, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
					if at == phase {
						return stop
					}
					return nil
				})
				if !errors.Is(err, stop) {
					t.Fatalf("phase not reached: %v", err)
				}
				adoptionOnly := phase == "after-empty-journal" || phase == "after-local-protocol"
				if !adoptionOnly && phase != "after-completed-receipt" && phase != "after-common-clear" {
					before := boardBytes(t, board)
					if _, err := PublishBundle(board, raw, BundleOptions{}); err == nil || !strings.Contains(err.Error(), "explicit resume") {
						t.Fatalf("implicit pending resume: %v", err)
					}
					if _, err := List(board); err == nil {
						t.Fatal("partially published board exposed")
					}
					if !reflect.DeepEqual(before, boardBytes(t, board)) {
						t.Fatal("blocked operation changed board")
					}
				}
				result, err := PublishBundle(board, raw, BundleOptions{Adopt: adoptionOnly, Resume: !adoptionOnly})
				if err != nil || result.Status != "completed" || len(result.Tasks) != 2 {
					t.Fatalf("resume=%+v err=%v", result, err)
				}
				entries, err := List(board)
				want := 2
				if shared {
					want++
				}
				if err != nil || len(entries) != want {
					t.Fatalf("entries=%v err=%v", entries, err)
				}
			})
		}
	}
}

func TestBundleResumePreservesPublicationConflict(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"card", "card-mode", "card-symlink", "batch", "ledger", "intent-missing"} {
		t.Run(scenario, func(t *testing.T) {
			board := configuredFixture(t)
			raw := publicationRequest(t, board)
			stop := errors.New("synthetic interruption")
			_, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
				if at == "after-card-0" {
					return stop
				}
				return nil
			})
			if !errors.Is(err, stop) {
				t.Fatal(err)
			}
			switch scenario {
			case "card":
				err = os.WriteFile(filepath.Join(board, "todo/TASK-1.md"), []byte("external conflict"), 0o600)
			case "card-mode":
				err = os.Chmod(filepath.Join(board, "todo/TASK-1.md"), 0o644)
			case "card-symlink":
				name := filepath.Join(board, "todo/TASK-1.md")
				if err := os.Rename(name, name+".backup"); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink("TASK-1.md.backup", name)
			case "batch":
				req, _ := bundleFixture(t)
				name, pathErr := contextPath("batch", req.Batch.ID, req.Batch.Revision)
				if pathErr != nil {
					t.Fatal(pathErr)
				}
				if err := os.MkdirAll(filepath.Dir(filepath.Join(board, name)), 0o755); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(filepath.Join(board, name), []byte("external batch conflict"), 0o600)
			case "ledger":
				err = os.WriteFile(filepath.Join(board, idsFile), []byte(`{"schemaVersion":2,"reserved":["TASK-99"]}`), 0o600)
			case "intent-missing":
				req, _ := bundleFixture(t)
				name, pathErr := contextPath("intent", req.Batch.Intent.ID, req.Batch.Intent.Revision)
				if pathErr != nil {
					t.Fatal(pathErr)
				}
				err = os.Remove(filepath.Join(board, name))
			}
			if err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			if _, err := PublishBundle(board, raw, BundleOptions{Resume: true}); err == nil {
				t.Fatal("conflicting resume accepted")
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("conflicting recovery changed board")
			}
		})
	}
}

func TestBundleCleanupFailurePreservesReceiptAndReplay(t *testing.T) {
	t.Parallel()
	board := configuredFixture(t)
	raw := publicationRequest(t, board)
	result, err := publishBundleWithStep(board, raw, BundleOptions{Adopt: true}, func(at string) error {
		if at == "after-common-clear" {
			return os.Remove(filepath.Join(board, ".task-manager.lock"))
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "retry identical input") || result.Status != "completed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	before := boardBytes(t, board)
	replay, err := PublishBundle(board, raw, BundleOptions{})
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Tasks, result.Tasks) {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("cleanup retry changed completed board")
	}
}

func TestBundleCopiedPendingJournalCannotAcquireCommonReservation(t *testing.T) {
	t.Parallel()
	_, owner, other := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	raw := publicationRequest(t, owner)
	publicationRequest(t, other)
	stop := errors.New("synthetic interruption")
	_, err := publishBundleWithStep(owner, raw, BundleOptions{Adopt: true}, func(at string) error {
		if at == "after-pending-journal" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	for _, name := range []string{bundlesFile, transitionsFile} {
		data, err := os.ReadFile(filepath.Join(owner, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(other, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	before := boardBytes(t, other)
	if _, err := PublishBundle(other, raw, BundleOptions{Resume: true}); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("copied pending accepted: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, other)) {
		t.Fatal("copied owner attempt changed board")
	}
	if _, err := PublishBundle(owner, raw, BundleOptions{Resume: true}); err != nil {
		t.Fatalf("original owner cannot recover: %v", err)
	}
}

func TestBundlePendingAllocationCannotChangeAfterForeignReservation(t *testing.T) {
	t.Parallel()
	_, owner, other := sharedFixture(t)
	if _, err := EnableShared(owner, false); err != nil {
		t.Fatal(err)
	}
	raw := publicationRequest(t, owner)
	stop := errors.New("synthetic interruption")
	_, err := publishBundleWithStep(owner, raw, BundleOptions{Adopt: true}, func(at string) error {
		if at == "after-pending-journal" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	foreign, err := Create(other, CreateRequest{Title: "Foreign allocation"})
	if err != nil || foreign.Card.ID != "TASK-2" {
		t.Fatalf("foreign=%+v err=%v", foreign, err)
	}
	before := boardBytes(t, owner)
	if _, err := PublishBundle(owner, raw, BundleOptions{Resume: true}); err == nil || !strings.Contains(err.Error(), "already reserved") {
		t.Fatalf("frozen allocation was changed: %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, owner)) {
		t.Fatal("conflicting reservation changed owner board")
	}
}

func TestBundlePublicExample(t *testing.T) {
	t.Parallel()
	board := configuredFixture(t)
	intent, err := os.ReadFile("../../examples/context/intent.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, intent); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../examples/context/bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	result, err := PublishBundle(board, raw, BundleOptions{Adopt: true})
	if err != nil || len(result.Tasks) != 2 {
		t.Fatalf("example=%+v err=%v", result, err)
	}
}
