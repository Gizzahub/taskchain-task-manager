package boardfiles

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func assessCardsOnly(t *testing.T, source SnapshotSource, entries []SnapshotEntry, p boardpolicy.Policy) []SnapshotFinding {
	t.Helper()
	s, err := NewCardsOnlySnapshot(source, entries)
	if err != nil {
		t.Fatal(err)
	}
	got, err := AssessCardsOnlySnapshot(s, p)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestCardsOnlyClosedLayout(t *testing.T) {
	p := v5Policy(t)
	paths := []string{"README.md", "todo/a.md", "manual/b.md", "backend/todo/auth/c.md", "backend/manual/auth/d.md", "_archive/done/e.md", "archive/2026-10/f.md", "_archive/manual/g.md", "backend/todo/archive/h.md"}
	entries := []SnapshotEntry{}
	for _, rel := range paths {
		entries = append(entries, SnapshotEntry{Path: rel})
	}
	if got := assessCardsOnly(t, SnapshotIndex, entries, p); len(got) != 0 {
		t.Fatalf("valid shape: %+v", got)
	}
	for _, rel := range []string{"todo/README.md", "backend/todo/auth/InDeX.MD", "todo/AGENTS.md", "TEMPLATE.md", ".gitkeep", "todo/.GITKEEP", "readme.md", "unknown/a.md", "todo/image.png", "stray.md"} {
		t.Run(rel, func(t *testing.T) {
			got := assessCardsOnly(t, SnapshotIndex, []SnapshotEntry{{Path: rel}}, p)
			if len(got) == 0 || got[0].Severity != SnapshotIssueError {
				t.Fatalf("missing structural error: %+v", got)
			}
		})
	}
	// The specification folds ASCII basename letters only, not Unicode S.
	if got := assessCardsOnly(t, SnapshotIndex, []SnapshotEntry{{Path: "todo/AGENTſ.md"}}, p); len(got) != 0 {
		t.Fatalf("unexpected Unicode filename exemption rule: %+v", got)
	}
}

func TestCardsOnlyPrunesForbiddenDirectoriesAndEngineState(t *testing.T) {
	for _, dir := range []string{"evidence", "receipts", "decision", "decisions", "adr", "adrs", "reference", "templates", ".ce", ".task-manager-context", ".task-manager.lock"} {
		t.Run(dir, func(t *testing.T) {
			entries := []SnapshotEntry{{Path: dir + "/deep/body.md"}, {Path: dir + "/link", Mode: fs.ModeSymlink}}
			got := assessCardsOnly(t, SnapshotIndex, entries, boardpolicy.Default())
			if len(got) != 1 || got[0].Path != dir || got[0].Severity != SnapshotIssueError {
				t.Fatalf("subtree not collapsed: %+v", got)
			}
		})
	}
	got := assessCardsOnly(t, SnapshotIndex, []SnapshotEntry{{Path: ".task-manager-ids.json"}}, boardpolicy.Default())
	if len(got) != 1 || !strings.Contains(got[0].Detail, "engine state") {
		t.Fatalf("engine file allowed: %+v", got)
	}
}

func TestCardsOnlySnapshotSeverity(t *testing.T) {
	for _, source := range []SnapshotSource{SnapshotWorktree, SnapshotIndex, SnapshotRef, SnapshotNonGit} {
		entries := []SnapshotEntry{{Path: "todo/INDEX.md", Tracked: true}, {Path: "todo/AGENTS.md"}, {Path: "todo/image.png"}}
		got := assessCardsOnly(t, source, entries, boardpolicy.Default())
		for _, f := range got {
			want := SnapshotIssueError
			if source == SnapshotWorktree && (f.Path != "todo/INDEX.md") {
				want = SnapshotIssueResidue
			}
			if f.Severity != want || f.Contract == "" || f.Action == "" {
				t.Fatalf("source=%v finding=%+v want=%v", source, f, want)
			}
		}
	}
	for _, source := range []SnapshotSource{SnapshotWorktree, SnapshotNonGit} {
		got := assessCardsOnly(t, source, []SnapshotEntry{{Path: "todo", Mode: fs.ModeDir}}, boardpolicy.Default())
		want := SnapshotIssueResidue
		if source == SnapshotNonGit {
			want = SnapshotIssueError
		}
		if len(got) != 1 || got[0].Severity != want {
			t.Fatalf("empty directory: %+v", got)
		}
	}
}

func TestCardsOnlyUnsafeNodeAndDeclaration(t *testing.T) {
	for _, mode := range []fs.FileMode{fs.ModeSymlink, fs.ModeNamedPipe, fs.ModeSocket, fs.ModeDevice} {
		got := assessCardsOnly(t, SnapshotRef, []SnapshotEntry{{Path: "README.md", Mode: mode}}, boardpolicy.Default())
		if len(got) != 1 || !strings.Contains(got[0].Detail, "reserved non-card") {
			t.Fatalf("unsafe root README: %+v", got)
		}
		got = assessCardsOnly(t, SnapshotRef, []SnapshotEntry{{Path: "todo/a.md", Mode: mode}}, boardpolicy.Default())
		if len(got) != 1 || !strings.Contains(got[0].Detail, "symlinks and special") {
			t.Fatalf("unsafe card leaf: %+v", got)
		}
	}
	s, err := NewCardsOnlySnapshot(SnapshotIndex, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []boardpolicy.Policy{
		{}, policy(t, true),
		v5Policy(t, boardpolicy.FileRoleV5{Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance, Owner: "owner", SourceRevision: "r1", ContentSHA256: strings.Repeat("a", 64)}),
		v5Policy(t, boardpolicy.FileRoleV5{Path: "INDEX.md", Kind: boardpolicy.FileRoleProjection, Owner: "owner", SourceRevision: "r1", Format: "live-card-paths-v1"}),
	} {
		if _, err := AssessCardsOnlySnapshot(s, p); err == nil {
			t.Fatal("unsupported policy interpreted as cards-only")
		}
	}
	p, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{"reference"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AssessCardsOnlySnapshot(s, p); err == nil {
		t.Fatal("forbidden module accepted")
	}
}

func TestCardsOnlyProjectionUsesOwnedSnapshotBytes(t *testing.T) {
	role := v5Projection()
	p := v5Policy(t, role)
	for _, source := range []SnapshotSource{SnapshotIndex, SnapshotRef, SnapshotWorktree} {
		data := []byte(projectionFile(role, "todo/a.md"))
		entries := []SnapshotEntry{{Path: "README.md", Tracked: true, Data: data}, {Path: "todo/a.md", Tracked: true}}
		s, err := NewCardsOnlySnapshot(source, entries)
		if err != nil {
			t.Fatal(err)
		}
		data[0] = '!'
		entries[0].Path = "INDEX.md"
		got, err := AssessCardsOnlySnapshot(s, p)
		if err != nil || len(got) != 0 {
			t.Fatalf("snapshot alias: %+v %v", got, err)
		}
		changed := []SnapshotEntry{{Path: "README.md", Data: []byte("different source bytes")}, {Path: "todo/a.md"}}
		before := append([]SnapshotEntry(nil), changed...)
		got = assessCardsOnly(t, source, changed, p)
		if len(got) != 1 || got[0].Path != "README.md" || !reflect.DeepEqual(before, changed) {
			t.Fatalf("source bytes/input changed: %+v", got)
		}
	}
}
