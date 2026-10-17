package boardfiles

import (
	"io/fs"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestCardsOnlyRejectsContradictorySnapshotInput(t *testing.T) {
	for _, entries := range [][]SnapshotEntry{
		{{Path: "../escape.md"}}, {{Path: "a\\b"}}, {{Path: "a//b"}}, {{Path: "."}}, {{Path: "/root"}},
		{{Path: "todo/\xff.md"}}, {{Path: "todo/a\n.md"}}, {{Path: "todo/a\x00.md"}},
		{{Path: "todo/a.md"}, {Path: "todo/a.md"}},
		{{Path: "todo"}, {Path: "todo/a.md"}},
		{{Path: "todo", Mode: fs.ModeDir, Data: []byte("body")}},
		{{Path: "todo", Mode: fs.ModeDir | fs.ModeSymlink}},
		{{Path: "todo/a.md", Missing: true}},
		{{Path: "todo/a.md", Missing: true, Tracked: true, Data: []byte("body")}},
	} {
		if _, err := NewCardsOnlySnapshot(SnapshotWorktree, entries); err == nil {
			t.Fatalf("invalid snapshot accepted: %+v", entries)
		}
	}
	for _, source := range []SnapshotSource{SnapshotIndex, SnapshotRef} {
		if _, err := NewCardsOnlySnapshot(source, []SnapshotEntry{{Path: "todo", Mode: fs.ModeDir}}); err == nil {
			t.Fatal("tree snapshot accepted empty directory")
		}
	}
	if _, err := NewCardsOnlySnapshot(0, nil); err == nil {
		t.Fatal("unknown source accepted")
	}
	if _, err := AssessCardsOnlySnapshot(CardsOnlySnapshot{}, boardpolicy.Default()); err == nil {
		t.Fatal("unconstructed snapshot accepted")
	}
	if _, err := NewCardsOnlySnapshot(SnapshotIndex, []SnapshotEntry{{Path: "todo/a.md", Missing: true, Tracked: true}}); err == nil {
		t.Fatal("index accepted a missing worktree entry")
	}
}
