package taskflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestCandidateCaptureCompleteProjectionAndOpaqueSeal(t *testing.T) {
	root := captureTestRepo(t)
	p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{}}, []boardpolicy.FileRoleV5{{Path: "README.md", Kind: boardpolicy.FileRoleProjection, Owner: "test", SourceRevision: "r1", Format: "live-card-paths-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []bool{false, true} {
		if extra {
			captureTestWrite(t, root, "tasks/INDEX.md", "tracked invalid entry")
			captureTestGit(t, root, "add", "tasks")
		}
		capture, err := CaptureCandidateSnapshot(context.Background(), root, "tasks", "index", "")
		if err != nil {
			t.Fatal(err)
		}
		findings, err := capture.Assess(context.Background(), p)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range findings {
			if f.Path == "README.md" && f.Severity == boardfiles.SnapshotIssueError {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing projection hidden by other findings: %+v", findings)
		}
	}
	raw, err := CaptureCardsOnlyIndex(context.Background(), root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	// A supplied snapshot retains its old missing-projection error when no
	// tracked structure finding has already prevented projection comparison.
	empty, _ := boardfiles.NewCardsOnlySnapshot(boardfiles.SnapshotIndex, nil)
	if _, err := boardfiles.AssessCardsOnlySnapshot(empty, p); err == nil || !strings.Contains(err.Error(), "lacks a snapshot entry") {
		t.Fatalf("legacy supplied API changed: %v", err)
	}
	for _, invalid := range []CandidateCapture{{}, {snapshot: raw.Snapshot, source: "index", index: raw.IndexDigest}} {
		if _, err := invalid.Assess(context.Background(), p); err == nil {
			t.Fatal("unsealed caller-supplied capture accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (CandidateCapture{}).Assess(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
