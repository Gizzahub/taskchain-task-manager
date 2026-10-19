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

func TestCandidateCapturePinnedRefSurvivesRefMovement(t *testing.T) {
	root := captureTestRepo(t)
	ctx := context.Background()
	before := captureTestGit(t, root, "rev-parse", "HEAD^{tree}")
	captured, err := CaptureCandidateSnapshot(ctx, root, "tasks", "ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	captureTestWrite(t, root, "tasks/INDEX.md", "new tracked violation")
	captureTestGit(t, root, "add", "tasks")
	captureTestGit(t, root, "commit", "-qm", "move ref after capture")
	if tree, index := captured.Provenance(); tree != before || index != "" {
		t.Fatalf("captured provenance followed moved ref: %q %q", tree, index)
	}
	findings, err := captured.Assess(ctx, boardpolicy.Default())
	if err != nil || len(findings) != 0 {
		t.Fatalf("captured bytes followed moved ref: %+v %v", findings, err)
	}
	current, err := CaptureCandidateSnapshot(ctx, root, "tasks", "ref", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	findings, err = current.Assess(ctx, boardpolicy.Default())
	if err != nil || len(findings) != 1 || findings[0].Path != "INDEX.md" {
		t.Fatalf("fresh capture did not observe moved ref: %+v %v", findings, err)
	}
}
