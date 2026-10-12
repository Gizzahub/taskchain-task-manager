package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGateEvidenceRequiresPassExitZeroAndPositiveBindings(t *testing.T) {
	block := validEvidenceBlock(sampleChangeID, "gate the board")
	bare := t.TempDir()
	if errs, _ := CheckGateEvidence(context.Background(), bare, block); len(errs) != 0 {
		t.Fatalf("valid block errors: %+v", errs)
	}
	if errs, _ := CheckGateEvidence(context.Background(), bare, strings.Replace(block, "PASS (exit 0)", "PASS (exit 00)", 1)); len(errs) != 0 {
		t.Fatalf("numeric zero compatibility errors: %+v", errs)
	}
	cases := []struct {
		name, content, want string
	}{
		{"pass nonzero exit", strings.Replace(block, "PASS (exit 0)", "PASS (exit 3)", 1), "evidence requires exit 0"},
		{"does not gate commit", strings.Replace(strings.Replace(block, "log-finished: 2026-10-12T03:00:02Z", "log-finished: 2026-10-12T03:00:00Z", 1), "gates-the-commit: yes — log finished after the commit, over a clean worktree", "gates-the-commit: no — log finished before the commit; this gates the content, not the tip", 1), "is not yes; evidence must gate the commit"},
		{"does not bind tree", strings.Replace(strings.Replace(block, "gates-the-commit: yes — log finished after the commit, over a clean worktree", "gates-the-commit: no — the ordered CI receipts do not bind this HEAD, tree, and Change-Id", 1), "tested-tree: yes", "tested-tree: no", 1), "is not yes; evidence must bind the tested tree"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := CheckGateEvidence(context.Background(), bare, tc.content)
			if findingHas(errs, "claims ") {
				t.Fatalf("fixture has inconsistent derived verdict: %+v", errs)
			}
			if !findingHas(errs, tc.want) {
				t.Fatalf("findings %+v", errs)
			}
		})
	}
}

func TestBindGateEvidenceRejectsPassNonzeroExit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ce-tasks.yaml"), []byte("gate:\n  name: unit-gate\n  log: logs/run.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitContract(t, root, nil, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, ".gz-git.yaml"), []byte("branch:\n  integrationBranch: main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitContract(t, root, nil, "add", "-A")
	gitContract(t, root, nil, "commit", "-qm", "source")
	gitContract(t, root, nil, "checkout", "-b", "work")
	when := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	commitMsg(t, root, "gate\n\nChange-Id: "+sampleChangeID+"\nModel: test\n", []string{
		"GIT_AUTHOR_DATE=" + when.Format(time.RFC3339),
		"GIT_COMMITTER_DATE=" + when.Format(time.RFC3339),
	})
	head := gitContract(t, root, nil, "rev-parse", "HEAD^{commit}")
	tree := gitContract(t, root, nil, "rev-parse", "HEAD^{tree}")
	path := filepath.Join(root, "logs", "run.log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(ciLog(head, tree, sampleChangeID, "clean", "PASS", "3", true)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, when.Add(time.Minute), when.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "PASS (exit 3)") || !strings.Contains(err.Error(), "requires exit 0") {
		t.Fatalf("error %v", err)
	}
}

func TestGateEvidenceIntegrationExemptionRequiresExactTrailer(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, message string
		integrated    bool
	}{
		{"exact trailer", "source\n\nChange-Id: " + sampleChangeID + "\nModel: test\n", true},
		{"prefix is not trailer", "source\n\nChange-Id: " + sampleChangeID + "x\nModel: test\n", false},
		{"prose is not trailer", "source\n\nmention Change-Id: " + sampleChangeID + "\nModel: test\n", false},
		{"embedded record separator is not a commit boundary", "source\n\nChange-Id: " + sampleChangeID + "\nModel: test\x1e\nprose\n", false},
		{"duplicate trailer is not an exact declaration", "source\n\nChange-Id: " + sampleChangeID + "\nChange-Id: " + sampleChangeID + "\nModel: test\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gitContract(t, root, nil, "init", "-b", "main")
			if err := os.WriteFile(filepath.Join(root, ".gz-git.yaml"), []byte("branch:\n  integrationBranch: main\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			commitMsg(t, root, tc.message, nil)
			gitContract(t, root, nil, "checkout", "-b", "work")
			source, integrated, err := changeIDIntegrated(ctx, root, sampleChangeID)
			if err != nil || source != "main" || integrated != tc.integrated {
				t.Fatalf("source=%q integrated=%v err=%v", source, integrated, err)
			}
		})
	}
}
