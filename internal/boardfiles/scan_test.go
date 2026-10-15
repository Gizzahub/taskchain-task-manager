package boardfiles

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func TestScanClassifiesDeclaredRolesCardsAndPrivateTrees(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tasks/README.md", "generated: indexer r1\n")
	write(t, root, "tasks/AGENTS.md", "Use the task skill.\n")
	write(t, root, "tasks/TEMPLATE.md", "---\n")
	write(t, root, "tasks/todo/001-root.md", "card")
	write(t, root, "tasks/backend/todo/auth/002-module.md", "card")
	write(t, root, "tasks/backend/plan/design/003-plan.md", "card")
	write(t, root, "tasks/todo/evidence/proof.bin", "proof")
	write(t, root, "tasks/.ce/journal/state.json", "state")
	write(t, root, "tasks/.task-manager-ids.json", "state")
	if err := os.Mkdir(filepath.Join(root, "tasks", ".task-manager.lock"), 0700); err != nil {
		t.Fatal(err)
	}
	write(t, root, "tasks/.task-manager-context/intents/id/1.json", "state")

	result, err := Scan(root, "tasks", policy(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Enabled || len(result.Findings) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func TestScanFindsUnknownFilesDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tasks/README.md", "wrong marker\n")
	write(t, root, "tasks/todo/001-card.md", "card")
	for _, name := range []string{"README.md", "INDEX.md", "AGENTS.md", "CLAUDE.md", "TEMPLATE.md"} {
		write(t, root, "tasks/todo/"+name, "reserved")
	}
	write(t, root, "tasks/todo/image.png", "not a card")
	write(t, root, "tasks/PROGRESS_SUMMARY.md", "not a card")
	write(t, root, "tasks/.task-manager-surprise.json", "not engine state")
	write(t, root, "tasks/unknown/other.md", "not a card")
	if err := os.Symlink("README.md", filepath.Join(root, "tasks", "AGENTS.md")); err != nil {
		t.Fatal(err)
	}

	result, err := Scan(root, "tasks", policy(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 11 {
		t.Fatalf("findings=%+v", result.Findings)
	}
	joined := findingsText(result)
	for _, want := range []string{"README.md: projection first line", "todo/README.md: reserved non-card", "todo/INDEX.md: reserved non-card", "todo/AGENTS.md: reserved non-card", "todo/CLAUDE.md: reserved non-card", "todo/TEMPLATE.md: reserved non-card", "todo/image.png: card locations", "PROGRESS_SUMMARY.md: file has no declared", ".task-manager-surprise.json: file has no declared", "unknown: directory has no declared", "AGENTS.md: symlinks"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}

func TestScanReturnsIOErrorsSeparatelyFromFindings(t *testing.T) {
	_, err := Scan(t.TempDir(), "missing-tasks", policy(t, true))
	if err == nil || !strings.Contains(err.Error(), "lstat tasks directory") {
		t.Fatalf("err=%v", err)
	}
}

func TestScanRestrictsRootZonesAndStorageToTheirDeclaredLayouts(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tasks/_archive/done/001-archived.md", "card")
	write(t, root, "tasks/archive/2026-10/002-archived.md", "card")
	write(t, root, "tasks/todo/arbitrary/note.md", "not a card")
	write(t, root, "tasks/plan/arbitrary/note.md", "not a card")
	write(t, root, "tasks/archive/arbitrary/note.md", "not a card")

	result, err := Scan(root, "tasks", policy(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 3 {
		t.Fatalf("findings=%+v", result.Findings)
	}
	joined := findingsText(result)
	for _, want := range []string{"todo/arbitrary: directory has no declared", "plan/arbitrary: directory has no declared", "archive/arbitrary: directory has no declared"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}

func TestScanIsExplicitNoOpForLegacyPolicy(t *testing.T) {
	root := t.TempDir()
	write(t, root, "tasks/anything.bin", "legacy")
	legacy := boardpolicy.Default()
	result, err := Scan(root, "tasks", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Enabled || len(result.Findings) != 0 {
		t.Fatalf("result=%+v", result)
	}
}

func policy(t *testing.T, enabled bool) boardpolicy.Policy {
	t.Helper()
	if !enabled {
		return boardpolicy.Default()
	}
	raw := []byte(`schema-version: 4
board-policy:
  modules: [backend]
  file-roles:
    enabled: true
    projections:
      - path: README.md
        owner: indexer
        revision: r1
        marker: "generated: indexer r1"
    guidance: [AGENTS.md]
    templates: [TEMPLATE.md]
`)
	p, err := boardpolicy.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func write(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findingsText(result Result) string {
	var lines []string
	for _, finding := range result.Findings {
		lines = append(lines, finding.Path+": "+finding.Detail)
	}
	return strings.Join(lines, "\n")
}
