package taskflow

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func rolePolicyV5(t *testing.T, guidance string) boardpolicy.Policy {
	t.Helper()
	p, err := boardpolicy.NewV5(boardpolicy.Declaration{Modules: []string{"backend"}}, []boardpolicy.FileRoleV5{{
		Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance, Owner: "guide", SourceRevision: "r1",
		ContentSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(guidance))),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestV5LintAndValidationDiscoverOnlyCards(t *testing.T) {
	root := t.TempDir()
	guide := "---\nid: TASK-99\ntitle: this is guidance\n---\n"
	writeRoleFixture(t, root, "tasks/AGENTS.md", guide)
	writeRoleFixture(t, root, "tasks/backend/todo/auth/001-work.md", "---\nid: TASK-1\ntitle: work\n---\n")
	p := rolePolicyV5(t, guide)
	cards, err := FindCardsWithPolicy(root, true, p)
	if err != nil || len(cards) != 1 || cards[0].ID != "TASK-1" {
		t.Fatalf("cards=%+v err=%v", cards, err)
	}
	report, err := LintWithPolicy(root, p)
	if err != nil || !report.Clean {
		t.Fatalf("lint=%+v err=%v", report, err)
	}
	results, _, err := validateAllWithPolicy(context.Background(), root, "tasks", p, nil)
	if err != nil || len(results) != 1 || results[0].Path != "tasks/backend/todo/auth/001-work.md" {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

func TestV5ReadinessDistinguishesFindingFromUnavailable(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/AGENTS.md", "tampered\n")
	p := rolePolicyV5(t, "approved\n")
	preflight, err := preflightCheckWithPolicy(root, PreflightOptions{}, p)
	if err != nil || PreflightExitError(preflight) == nil || len(preflight.Lint.Structure) != 1 {
		t.Fatalf("preflight=%+v err=%v", preflight, err)
	}
	t.Chdir(root)
	gate := runGateWithPolicy(context.Background(), io.Discard, true, p)
	if gate.Status != "not-ready" || gate.Summary != GateSummaryLint || gate.FailedStep != GateStepLint {
		t.Fatalf("finding gate=%+v", gate)
	}
	if err := os.Rename(filepath.Join(root, "tasks"), filepath.Join(root, "retained")); err != nil {
		t.Fatal(err)
	}
	if _, err := preflightCheckWithPolicy(root, PreflightOptions{}, p); err == nil {
		t.Fatal("missing pinned board was treated as a finding")
	}
	gate = runGateWithPolicy(context.Background(), io.Discard, true, p)
	if gate.Status != "unavailable" {
		t.Fatalf("unavailable gate=%+v", gate)
	}
}
