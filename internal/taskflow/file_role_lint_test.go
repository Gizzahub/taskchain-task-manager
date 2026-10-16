package taskflow

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestLintWithPolicyExcludesDeclaredGuidanceAndReportsUnknownFile(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/todo/001-card.md", "---\nid: TASK-1\ntitle: card\n---\n")
	writeRoleFixture(t, root, "tasks/AGENTS.md", "short guidance\n")
	policy := rolePolicy(t)

	report, err := LintWithPolicy(root, policy)
	if err != nil {
		t.Fatalf("LintWithPolicy() error = %v", err)
	}
	if !report.Clean {
		t.Fatalf("LintWithPolicy() unexpectedly unclean: %+v", report)
	}

	writeRoleFixture(t, root, "tasks/unowned.txt", "unowned\n")
	report, err = LintWithPolicy(root, policy)
	if err != nil {
		t.Fatalf("LintWithPolicy() unknown file error = %v", err)
	}
	if report.Clean || len(report.Structure) != 1 || report.Structure[0].Path != "tasks/unowned.txt" {
		t.Fatalf("unknown file report = %+v", report)
	}
}

func TestFindCardsWithPolicyDoesNotReadDeclaredGuidance(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/todo/001-card.md", "---\nid: TASK-1\ntitle: card\n---\n")
	writeRoleFixture(t, root, "tasks/AGENTS.md", "---\nid: TASK-99\ntitle: guidance\n---\n")
	cards, err := FindCardsWithPolicy(root, true, rolePolicy(t))
	if err != nil {
		t.Fatalf("FindCardsWithPolicy() error = %v", err)
	}
	if len(cards) != 1 || cards[0].ID != "TASK-1" {
		t.Fatalf("cards = %+v, want only TASK-1", cards)
	}
}

func rolePolicy(t *testing.T) boardpolicy.Policy {
	t.Helper()
	p, err := boardpolicy.Parse([]byte("schema-version: 4\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: [AGENTS.md]\n    templates: []\n"))
	if err != nil {
		t.Fatalf("parse role policy: %v", err)
	}
	return p
}

func writeRoleFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimPrefix(body, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileRoleStructureFailsGateAndPreflightWithoutReadingSymlink(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/AGENTS.md", "guidance")
	outside := filepath.Join(root, "outside.md")
	if err := os.WriteFile(outside, []byte("untrusted card data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "tasks", "todo"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tasks", "todo", "001-link.md")); err != nil {
		t.Fatal(err)
	}
	policy := rolePolicy(t)
	preflight, err := preflightCheckWithPolicy(root, PreflightOptions{}, policy)
	if err != nil || PreflightExitError(preflight) == nil || len(preflight.Lint.Structure) != 1 {
		t.Fatalf("preflight=%+v err=%v", preflight, err)
	}
	t.Chdir(root)
	gate := runGateWithPolicy(context.Background(), io.Discard, true, policy)
	if gate.Status != "not-ready" || gate.Summary != GateSummaryLint || gate.FailedStep != GateStepLint {
		t.Fatalf("gate=%+v", gate)
	}
	if len(gate.Steps) != 2 || gate.Steps[0].Status != "skipped" {
		t.Fatalf("gate did not report safe validation skip: %+v", gate)
	}
}

func TestFileRoleValidationIgnoresGuidanceAndKeepsModuleCards(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/AGENTS.md", "---\nid: TASK-99\n---\n")
	writeRoleFixture(t, root, "tasks/backend/todo/auth/001-work.md", "---\nid: TASK-1\ntitle: work\n---\n")
	p, err := boardpolicy.Parse([]byte("schema-version: 4\nboard-policy:\n  modules: [backend]\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: [AGENTS.md]\n    templates: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := LintWithPolicy(root, p)
	if err != nil || !report.Clean {
		t.Fatalf("lint=%+v err=%v", report, err)
	}
	if len(report.Dirs) != 1 || report.Dirs[0].Kind != LintDirDeclared {
		t.Fatalf("module census=%+v", report.Dirs)
	}
	results, _, err := validateAllWithPolicy(context.Background(), root, "tasks", p, nil)
	if err != nil || len(results) != 1 || results[0].Path != "tasks/backend/todo/auth/001-work.md" {
		t.Fatalf("results=%+v err=%v", results, err)
	}
}

func TestActiveBoardPolicyDoesNotTreatOrphanPolicyAsDefault(t *testing.T) {
	root := t.TempDir()
	writeRoleFixture(t, root, "tasks/.task-manager-policy.json", "{}")
	if _, err := ActiveBoardPolicy(root); err == nil {
		t.Fatal("orphan policy silently became default")
	}
}
