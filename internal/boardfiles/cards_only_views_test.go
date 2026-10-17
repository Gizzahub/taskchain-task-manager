package boardfiles

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestCardsOnlyWorktreeProjectionSeparatesTrackedAndResidue(t *testing.T) {
	role := v5Projection()
	p := v5Policy(t, role)
	entries := []SnapshotEntry{
		{Path: "README.md", Tracked: true, Data: []byte(projectionFile(role, "todo/a.md"))},
		{Path: "todo/a.md", Tracked: true}, {Path: "todo/local.md"},
	}
	if got := assessCardsOnly(t, SnapshotWorktree, entries, p); len(got) != 0 {
		t.Fatalf("residue polluted tracked projection: %+v", got)
	}
	entries[0].Data = []byte(projectionFile(role, "todo/a.md", "todo/local.md"))
	got := assessCardsOnly(t, SnapshotWorktree, entries, p)
	if len(got) != 1 || got[0].Severity != SnapshotIssueError {
		t.Fatalf("tracked projection compared physical inventory: %+v", got)
	}
	entries[0].Tracked = false
	if got := assessCardsOnly(t, SnapshotWorktree, entries, p); len(got) != 0 {
		t.Fatalf("residue projection did not compare supplied physical view: %+v", got)
	}
	entries[0].Data = []byte("bad projection")
	got = assessCardsOnly(t, SnapshotWorktree, entries, p)
	if len(got) != 1 || got[0].Severity != SnapshotIssueResidue {
		t.Fatalf("residue projection became tracked: %+v", got)
	}
	entries[0].Tracked = true
	entries = append(entries, SnapshotEntry{Path: "evidence/local.bin"})
	got = assessCardsOnly(t, SnapshotWorktree, entries, p)
	if len(got) != 2 || got[0].Path != "README.md" || got[0].Severity != SnapshotIssueError {
		t.Fatalf("residue failure hid tracked projection drift: %+v", got)
	}
}

func TestCardsOnlyMissingTrackedLeafAndEmptyDirectories(t *testing.T) {
	entries := []SnapshotEntry{{Path: "todo/a.md", Tracked: true, Missing: true}}
	got := assessCardsOnly(t, SnapshotWorktree, entries, boardpolicy.Default())
	if len(got) != 1 || got[0].Path != "todo/a.md" || got[0].Severity != SnapshotIssueError {
		t.Fatalf("missing leaf manufactured physical empty dir or was ignored: %+v", got)
	}
	entries = []SnapshotEntry{{Path: "todo", Mode: fs.ModeDir}, {Path: "todo/image.png", Tracked: true}}
	got = assessCardsOnly(t, SnapshotWorktree, entries, boardpolicy.Default())
	if len(got) != 1 || got[0].Path != "todo/image.png" {
		t.Fatalf("nonempty directory mislabeled empty: %+v", got)
	}
	role := v5Projection()
	if got := assessCardsOnly(t, SnapshotWorktree, []SnapshotEntry{{Path: "README.md", Missing: true, Tracked: true}}, v5Policy(t, role)); len(got) != 1 {
		t.Fatalf("missing tracked README did not block comparison: %+v", got)
	}
}

func TestCardsOnlyTrackedFileReplacedByDirectory(t *testing.T) {
	for _, rel := range []string{"README.md", "todo"} {
		got := assessCardsOnly(t, SnapshotWorktree, []SnapshotEntry{{Path: rel, Mode: fs.ModeDir, Tracked: true}}, boardpolicy.Default())
		if len(got) != 1 || got[0].Severity != SnapshotIssueError {
			t.Fatalf("tracked type replacement became residue: %+v", got)
		}
	}
}

func TestCardsOnlyReservedAndOwnerPrecedenceBeforeUnsafeMode(t *testing.T) {
	for _, tc := range []struct{ rel, detail string }{
		{"todo/README.md", "reserved non-card"},
		{"evidence", "non-card directory component"},
		{".task-manager-ids.json", "engine state"},
		{".ce", "engine state"},
	} {
		got := assessCardsOnly(t, SnapshotRef, []SnapshotEntry{{Path: tc.rel, Mode: fs.ModeSymlink}}, boardpolicy.Default())
		if len(got) != 1 || !strings.Contains(got[0].Detail, tc.detail) {
			t.Fatalf("precedence changed for %s: %+v", tc.rel, got)
		}
	}
}

func TestCardsOnlyRejectsManualTopologyCollision(t *testing.T) {
	for _, declaration := range []boardpolicy.Declaration{
		{Modules: []string{"manual"}},
		{Zones: []string{"manual"}, ZoneStatus: map[string]string{"manual": "pending"}},
	} {
		p, err := boardpolicy.New(declaration)
		if err != nil {
			t.Fatal(err)
		}
		s, err := NewCardsOnlySnapshot(SnapshotIndex, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := AssessCardsOnlySnapshot(s, p); err == nil {
			t.Fatal("manual topology collision ignored")
		}
	}
}

func TestCardsOnlyLegacyCEStateIsDeniedNotOwned(t *testing.T) {
	for _, name := range []string{".ce-metrics", ".batch-state.yaml", ".ce-task-cleanup.lock", ".ce-task-cleanup-1234567890"} {
		got := assessCardsOnly(t, SnapshotRef, []SnapshotEntry{{Path: name + "/state"}}, boardpolicy.Default())
		if len(got) != 1 || got[0].Path != name || !strings.Contains(got[0].Detail, "engine state") {
			t.Fatalf("legacy state not denied: %+v", got)
		}
	}
	for _, name := range []string{".ce-task-cleanup-", ".ce-task-cleanup-notes", ".ce-task-cleanup-12345678901"} {
		if legacyEngineName(name) {
			t.Fatalf("broad legacy engine matcher: %s", name)
		}
	}
}

func TestCardsOnlyLegacyQuarantineMatchesRuntimeName(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), ".ce-task-cleanup-")
	if err != nil {
		t.Fatal(err)
	}
	if !legacyEngineName(filepath.Base(dir)) {
		t.Fatalf("runtime quarantine name not recognized: %s", filepath.Base(dir))
	}
}
