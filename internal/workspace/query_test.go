package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const queryIntent = `{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":"Query","outcome":"A result is available","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"result","text":"A result exists."}]}`

func queryFixture(t *testing.T) (Manifest, map[string]string) {
	t.Helper()
	root := t.TempDir()
	boards := map[string]string{}
	repositories := make([]Repository, 0, 2)
	for _, name := range []string{"alpha", "beta"} {
		path := filepath.Join(root, name)
		board := filepath.Join(path, "board")
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := taskstore.Init(board); err != nil {
			t.Fatal(err)
		}
		boards[name] = board
		repositories = append(repositories, Repository{Name: name, Path: path, Board: board})
	}
	return Manifest{Repositories: repositories}, boards
}

func TestQueryCardAliasAndScopedLookup(t *testing.T) {
	manifest, boards := queryFixture(t)
	if _, err := taskstore.Create(boards["alpha"], taskstore.CreateRequest{ID: "TASK-1", Title: "alpha"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(boards["beta"], taskstore.CreateRequest{ID: "TASK-2", Title: "beta"}); err != nil {
		t.Fatal(err)
	}

	result, err := Query(manifest, Selector{CardID: "TASK-0001"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Repository != "alpha" || result.Entry == nil || result.Entry.Card.ID != "TASK-1" || result.Context != nil {
		t.Fatalf("result = %#v", result)
	}
	if filepath.IsAbs(result.Entry.Path) || result.Entry.Path != "todo/TASK-1.md" {
		t.Fatalf("card path = %q", result.Entry.Path)
	}

	result, err = Query(manifest, Selector{Repository: "beta", CardID: "TASK-2"})
	if err != nil || result.Repository != "beta" || result.Entry == nil || result.Entry.Card.Title != "beta" {
		t.Fatalf("scoped result = %#v, %v", result, err)
	}
}

func TestQueryReportsDeterministicCollisionAndMissingResults(t *testing.T) {
	manifest, boards := queryFixture(t)
	for _, board := range boards {
		if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "same"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Query(manifest, Selector{CardID: "TASK-1"}); err == nil || !strings.Contains(err.Error(), "alpha, beta") {
		t.Fatalf("collision error = %v", err)
	}
	result, err := Query(manifest, Selector{Repository: "beta", CardID: "TASK-1"})
	if err != nil || result.Repository != "beta" || result.Entry == nil {
		t.Fatalf("scoped collision result = %#v, %v", result, err)
	}
	for _, selector := range []Selector{{CardID: "TASK-9"}, {Repository: "missing", CardID: "TASK-1"}} {
		if _, err := Query(manifest, selector); err == nil {
			t.Fatalf("missing selector accepted: %#v", selector)
		}
	}
}

func TestQueryContextAndInvalidSelectors(t *testing.T) {
	manifest, boards := queryFixture(t)
	if _, err := taskstore.RegisterContext(boards["beta"], []byte(queryIntent)); err != nil {
		t.Fatal(err)
	}
	selector := Selector{ContextKind: "intent", ContextID: "INTENT-0123456789abcdef0123456789abcdef", ContextRevision: 1}
	result, err := Query(manifest, selector)
	if err != nil {
		t.Fatal(err)
	}
	if result.Repository != "beta" || result.Context == nil || result.Entry != nil || filepath.IsAbs(result.Context.Path) {
		t.Fatalf("context result = %#v", result)
	}
	if _, err := Query(manifest, Selector{CardID: "TASK-1", ContextKind: "intent", ContextID: selector.ContextID, ContextRevision: 1}); err == nil {
		t.Fatal("combined selector accepted")
	}
	for _, bad := range []Selector{{}, {CardID: "task-1"}, {ContextKind: "intent", ContextID: selector.ContextID}, {ContextKind: "invalid", ContextID: "x", ContextRevision: 1}} {
		if _, err := Query(manifest, bad); err == nil {
			t.Fatalf("invalid selector accepted: %#v", bad)
		}
	}
}

func TestQueryPropagatesBoardFailures(t *testing.T) {
	manifest, boards := queryFixture(t)
	if err := os.WriteFile(filepath.Join(boards["beta"], ".task-manager-ids.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Query(manifest, Selector{CardID: "TASK-1"}); err == nil || !strings.Contains(err.Error(), `query repository "beta"`) {
		t.Fatalf("board failure = %v", err)
	}
}
