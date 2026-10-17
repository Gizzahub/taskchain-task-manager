package boardfiles

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDiffLiveCardPaths(t *testing.T) {
	for _, tc := range []struct {
		name           string
		before, after  []string
		added, removed []string
	}{
		{name: "empty"},
		{name: "nil and empty", after: []string{}},
		{name: "reordered", before: []string{"todo/b.md", "todo/a.md"}, after: []string{"todo/a.md", "todo/b.md"}},
		{name: "add", after: []string{"todo/b.md", "todo/a.md"}, added: []string{"todo/a.md", "todo/b.md"}},
		{name: "delete", before: []string{"todo/b.md", "todo/a.md"}, removed: []string{"todo/a.md", "todo/b.md"}},
		{name: "move same count", before: []string{"todo/a.md"}, after: []string{"doing/a.md"}, added: []string{"doing/a.md"}, removed: []string{"todo/a.md"}},
		{name: "module category", before: []string{"backend/todo/auth/a.md"}, after: []string{"backend/todo/core/a.md"}, added: []string{"backend/todo/core/a.md"}, removed: []string{"backend/todo/auth/a.md"}},
		{name: "UTF8 exact", after: []string{"todo/가.md", "todo/é.md", "todo/é.md", "todo/A.md"}, added: []string{"todo/A.md", "todo/é.md", "todo/é.md", "todo/가.md"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := slices.Clone(tc.before), slices.Clone(tc.after)
			got, err := DiffLiveCardPaths(tc.before, tc.after)
			if err != nil || !slices.Equal(got.Added, tc.added) || !slices.Equal(got.Removed, tc.removed) {
				t.Fatalf("delta=%+v err=%v", got, err)
			}
			if !reflect.DeepEqual(before, tc.before) || !reflect.DeepEqual(after, tc.after) {
				t.Fatal("inputs changed")
			}
			if len(got.Added) > 0 {
				got.Added[0] = "modified result"
			}
			if len(got.Removed) > 0 {
				got.Removed[0] = "modified result"
			}
			if !reflect.DeepEqual(before, tc.before) || !reflect.DeepEqual(after, tc.after) {
				t.Fatal("result aliases input")
			}
		})
	}
}

func TestDiffLiveCardPathsRejectsInvalidSnapshots(t *testing.T) {
	invalid := [][]string{
		{"todo/a.md", "todo/a.md"}, {""}, {"."}, {"../a.md"}, {"/todo/a.md"},
		{"todo//a.md"}, {"todo/./a.md"}, {"todo\\a.md"}, {"todo/a\n.md"},
		{"todo/a\x00.md"}, {"todo/\xff.md"}, {" todo/a.md"}, {"todo/a.md "},
		{"todo/<!-- task-manager:projection-begin.md"}, {"todo/a-->.md"},
	}
	for _, paths := range invalid {
		for _, side := range []string{"before", "after"} {
			t.Run(side+"/"+strings.Join(paths, ","), func(t *testing.T) {
				original := slices.Clone(paths)
				before, after := paths, []string{}
				if side == "after" {
					before, after = after, before
				}
				got, err := DiffLiveCardPaths(before, after)
				if err == nil || !strings.HasPrefix(err.Error(), side+" inventory:") || got.Added != nil || got.Removed != nil {
					t.Fatalf("delta=%+v err=%v", got, err)
				}
				if !reflect.DeepEqual(paths, original) {
					t.Fatal("rejected input changed")
				}
			})
		}
	}
}
