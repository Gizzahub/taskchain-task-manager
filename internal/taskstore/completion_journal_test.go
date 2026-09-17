package taskstore

import (
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func TestCompletionJournalNeverExposesPendingOrWrongScope(t *testing.T) {
	j, r, b, raw := archiveJournalFixture(t)
	entries := []Entry{{Path: b.Target, Card: card.View{ID: b.ID, Status: "done"}}}
	read := func(Entry) ([]byte, error) { return raw, nil }
	p := boardpolicy.Default()
	if got, err := completionForJournal(entries, p, &j, j.BoardPath, j.Namespace, read); err == nil || got != nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("pending leaked completion: %v %v", got, err)
	}
	r.State, r.Original, r.Patched = "completed", nil, nil
	j.Records = []archiveRecord{r}
	got, err := completionForJournal(entries, p, &j, j.BoardPath, j.Namespace, read)
	if err != nil || !got.done("TASK-1") {
		t.Fatalf("valid completed journal: %v %v", got, err)
	}
	for _, scope := range []struct{ board, namespace string }{{"/different/tasks", ""}, {j.BoardPath, strings.Repeat("b", 32)}} {
		if got, err := completionForJournal(entries, p, &j, scope.board, scope.namespace, read); err == nil || got != nil || !strings.Contains(err.Error(), "current board session") {
			t.Fatalf("wrong scope: %v %v", got, err)
		}
	}
	got, err = completionForJournal(entries, p, nil, j.BoardPath, "", nil)
	if err != nil || got.done("TASK-1") {
		t.Fatalf("legacy archive inferred completion: %v %v", got, err)
	}
}
