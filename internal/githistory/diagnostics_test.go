package githistory

import (
	"context"
	"strings"
	"testing"
)

func TestBrokenRefCannotYieldPartialHistory(t *testing.T) {
	dir := historyRepo(t)
	historyCard(t, dir, "tasks/todo/card.md", "id: TASK-1\n")
	historyCommit(t, dir)
	historyCard(t, dir, ".git/refs/heads/broken", "invalid-object-id\n")
	got, err := Scan(context.Background(), dir, "tasks")
	if err == nil || !strings.Contains(err.Error(), "broken") || len(got.IDs) != 0 {
		t.Fatalf("partial scan accepted: %+v %v", got, err)
	}
}

func TestParentBoardRejectedOnEmptyRepository(t *testing.T) {
	if _, err := Scan(context.Background(), historyRepo(t), ".."); err == nil {
		t.Fatal("parent board accepted")
	}
}
