package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanCILogBoundaries(t *testing.T) {
	head := strings.Repeat("ab", 20)
	tree := strings.Repeat("cd", 20)
	id := sampleChangeID
	good := ciLog(head, tree, id, "clean", "PASS", "0", true)
	good = strings.Replace(good, "ok example\n", "FAIL example\n--- FAIL: Example\n--- SKIP: Example\nok example\n", 1)
	path := writeLog(t, t.TempDir(), good)
	receipt, err := ScanCILog(path)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.FailLines != 2 || receipt.SkipLines != 1 || receipt.OKLines != 1 || receipt.Result != "PASS" {
		t.Fatalf("counts %+v", receipt)
	}

	swapped := "CI git finish: run-id=r head=" + head + " tree=" + tree + " change-id=" + id + " worktree=clean\n" +
		"CI git start: run-id=r head=" + head + " tree=" + tree + " change-id=" + id + " worktree=clean\n" +
		"CI gate result: run-id=r result=PASS exit=0\n"
	_, err = ScanCILog(writeLog(t, t.TempDir(), swapped))
	if err == nil || !strings.Contains(err.Error(), "out of order") || strings.Contains(err.Error(), "disagree") {
		t.Fatalf("order: %v", err)
	}
	disagree := strings.Replace(good, "tree="+tree, "tree="+strings.Repeat("ef", 20), 1)
	_, err = ScanCILog(writeLog(t, t.TempDir(), disagree))
	if err == nil || !strings.Contains(err.Error(), "start and finish disagree") {
		t.Fatalf("disagree: %v", err)
	}
	runID := strings.Replace(good, "CI git finish: run-id=r ", "CI git finish: run-id=other ", 1)
	_, err = ScanCILog(writeLog(t, t.TempDir(), runID))
	if err == nil || !strings.Contains(err.Error(), "run-id disagree") {
		t.Fatalf("run-id: %v", err)
	}
	_, err = ScanCILog(writeLog(t, t.TempDir(), "CI git start: nope\nCI gate result: run-id=r result=PASS exit=0\n"))
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("malformed: %v", err)
	}
	_, err = ScanCILog(writeLog(t, t.TempDir(), strings.Replace(good, "CI gate result: run-id=r result=PASS exit=0\n", "", 1)))
	if err == nil || !strings.Contains(err.Error(), "no valid outer `CI gate result:` line") {
		t.Fatalf("result: %v", err)
	}
	huge := strings.Repeat("a", (4<<20)+8) + "\n"
	_, err = ScanCILog(writeLog(t, t.TempDir(), huge))
	if err == nil || !strings.Contains(err.Error(), "exceeds 4MiB") {
		t.Fatalf("bound: %v", err)
	}
}

func TestGateEvidenceRefusalsAndDurableCopy(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ce-tasks.yaml"), []byte("gate:\n  name: unit-gate\n  log: logs/run-*.log\n"), 0o644); err != nil {
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
	commitMsg(t, root, "gate the board\n\nChange-Id: "+sampleChangeID+"\nModel: test-1\n", []string{
		"GIT_AUTHOR_DATE=" + when.Format(time.RFC3339),
		"GIT_COMMITTER_DATE=" + when.Format(time.RFC3339),
	})
	head := gitContract(t, root, nil, "rev-parse", "HEAD^{commit}")
	tree := gitContract(t, root, nil, "rev-parse", "HEAD^{tree}")
	logPath := filepath.Join(root, "logs", "run-1.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(body string, mtime time.Time) {
		t.Helper()
		if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(logPath, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	base := ciLog(head, tree, sampleChangeID, "clean", "PASS", "0", true)
	write(base, when)
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "within the commit's second") {
		t.Fatalf("same second: %v", err)
	}
	write(base, when.Add(-time.Hour))
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "log finished before the commit") {
		t.Fatalf("before: %v", err)
	}
	write(ciLog(head, tree, sampleChangeID, "dirty", "PASS", "0", true), when.Add(time.Minute))
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "worktree was dirty") {
		t.Fatalf("dirty: %v", err)
	}
	write(ciLog(head, tree, sampleChangeID, "clean", "FAIL", "3", true), when.Add(time.Minute))
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "CI log result is FAIL (exit 3)") {
		t.Fatalf("fail: %v", err)
	}
	write(ciLog(head, tree, "none", "clean", "PASS", "0", false), when.Add(time.Minute))
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "does not match branch Change-Id") {
		t.Fatalf("change-id: %v", err)
	}
	write(ciLog(strings.Repeat("ab", 20), tree, sampleChangeID, "clean", "PASS", "0", true), when.Add(time.Minute))
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "does not match current HEAD and tree") {
		t.Fatalf("head: %v", err)
	}

	write(base, when.Add(time.Minute))
	block, dest, err := PrepareGateEvidence(ctx, root, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dest, string(filepath.Separator)+"taskchain"+string(filepath.Separator)+"gates"+string(filepath.Separator)) || !strings.Contains(block, "gate: unit-gate\n") || !strings.Contains(block, "change-id: "+sampleChangeID) {
		t.Fatalf("dest %s block %s", dest, block)
	}
	again, dest2, err := PrepareGateEvidence(ctx, root, "")
	if err != nil || dest2 != dest || again != block {
		t.Fatalf("idempotent: %v %s", err, dest2)
	}
	if err := os.WriteFile(dest, []byte("different\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareGateEvidence(ctx, root, ""); err == nil || !strings.Contains(err.Error(), "refuse to overwrite different gate evidence") {
		t.Fatalf("overwrite: %v", err)
	}
	if err := os.WriteFile(dest, []byte(block), 0o644); err != nil {
		t.Fatal(err)
	}
	errs, warns := CheckGateEvidence(ctx, root, block)
	if len(errs) != 0 || len(warns) != 0 {
		t.Fatalf("shared store errs=%v warns=%v", errs, warns)
	}
	name, err := gateReceiptName(block)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, ".git", "other", "gates", name)
	if err := os.MkdirAll(filepath.Dir(other), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(dest, other); err != nil {
		t.Fatal(err)
	}
	errs, _ = CheckGateEvidence(ctx, root, block)
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "no gate receipt "+name) {
		t.Fatalf("outside store: %+v", errs)
	}

	if err := WriteReadyGateReceipt(ctx, root); err != nil {
		t.Fatal(err)
	}
	write(ciLog(head, tree, sampleChangeID, "clean", "FAIL", "1", true), when.Add(time.Minute))
	if err := WriteReadyGateReceipt(ctx, root); err == nil || !strings.Contains(err.Error(), "FAIL") {
		t.Fatalf("ready receipt: %v", err)
	}
}

func TestGateEvidenceCatalogueAndValidator(t *testing.T) {
	ctx := context.Background()
	block := validEvidenceBlock(sampleChangeID, "gate the board")
	bare := t.TempDir()
	_, warns := CheckGateEvidence(ctx, bare, block)
	if len(warns) != 1 || !strings.Contains(warns[0].Message, "keeps no gate receipts") {
		t.Fatalf("no store: %+v", warns)
	}
	cases := []struct {
		name string
		edit func(string) string
		want string
	}{
		{"marker", func(s string) string { return strings.Replace(s, gateEvidenceMarker, "other-marker", 1) }, "must be taskchain-gate-evidence"},
		{"schema", func(s string) string { return strings.Replace(s, "schema-version: 2", "schema-version: 3", 1) }, "must be supported schema version 2: 3"},
		{"unknown", func(s string) string { return s + "extra: 1\n" }, "is unknown; gate evidence schemas fail closed"},
		{"commit hash", func(s string) string {
			return strings.Replace(s, "commit: gate the board", "commit: "+strings.Repeat("ab", 20), 1)
		}, "is a commit hash"},
		{"change-id", func(s string) string {
			return strings.Replace(s, sampleChangeID, "taskchain-gate/ZZ", 1)
		}, "is not a safe Change-Id"},
		{"duplicate", func(s string) string {
			return strings.Replace(s, "gate: unit-gate\n", "gate: unit-gate\ngate: unit-gate\n", 1)
		}, "is written more than once"},
		{"fail", func(s string) string { return strings.Replace(s, "PASS (exit 0)", "FAIL (exit 1)", 1) }, "is not `PASS (exit N)`"},
		{"dirty", func(s string) string { return strings.Replace(s, "worktree: clean", "worktree: dirty", 1) }, "is not clean"},
		{"pass with failures", func(s string) string { return strings.Replace(s, "fail-lines: 0", "fail-lines: 2", 1) }, "says PASS beside 2 counted failures"},
		{"timestamp", func(s string) string {
			return strings.Replace(s, "log-finished: ", "log-finished: yesterday\nignore: ", 1)
		}, "is not an RFC3339 timestamp"},
		{"verdict", func(s string) string {
			return strings.Replace(s, "gates-the-commit: yes", "gates-the-commit: no", 1)
		}, "but the block derives:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs, _ := CheckGateEvidence(ctx, bare, tc.edit(block))
			if !findingHas(errs, tc.want) {
				t.Fatalf("errs %+v", errs)
			}
		})
	}

	root := t.TempDir()
	card := "---\nid: TASK-1\ntitle: Probe\ntype: chore\nstatus: pending\n---\n\n" + strings.Replace(block, "worktree: clean", "worktree: dirty", 1)
	if err := writeFileForTest(root, "tasks/todo/001-probe.md", []byte(card)); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "tasks/decision/note.md", []byte("# Note\n\n"+strings.Replace(block, gateEvidenceMarker, "other-marker", 1))); err != nil {
		t.Fatal(err)
	}
	results, _, err := ValidateAllIn(ctx, root, "tasks")
	if err != nil {
		t.Fatal(err)
	}
	var sawCard, sawDecision bool
	for _, result := range results {
		blob := findingsText(result.Errors)
		if strings.Contains(result.Path, "001-probe.md") && strings.Contains(blob, "is not clean") {
			sawCard = true
		}
		if strings.Contains(result.Path, "decision/note.md") && strings.Contains(blob, "must be taskchain-gate-evidence") {
			sawDecision = true
		}
	}
	if !sawCard || !sawDecision {
		t.Fatalf("validator path card=%v decision=%v results=%d", sawCard, sawDecision, len(results))
	}
}

func TestGateEvidenceMissingDeclarationAndNonRepo(t *testing.T) {
	dir := t.TempDir()
	if err := WriteReadyGateReceipt(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if _, _, err := PrepareGateEvidence(context.Background(), dir, ""); err == nil || !strings.Contains(err.Error(), "does not declare gate.name and gate.log") {
		t.Fatalf("declaration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ce-tasks.yaml"), []byte("gate:\n  name: unit-gate\n  log: logs/run-*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := writeLog(t, dir, ciLog(strings.Repeat("ab", 20), strings.Repeat("cd", 20), sampleChangeID, "clean", "PASS", "0", true))
	if _, _, err := PrepareGateEvidence(context.Background(), dir, logPath); err == nil || !strings.Contains(err.Error(), "durable copy has nowhere outside a working tree") {
		t.Fatalf("non-repo: %v", err)
	}
	if _, _, err := PrepareGateEvidence(context.Background(), dir, ""); err == nil || !strings.Contains(err.Error(), "no CI log under logs/run-*.log") || !strings.Contains(err.Error(), "unit-gate") {
		t.Fatalf("missing log: %v", err)
	}
}

func ciLog(head, tree, changeID, worktree, result, exitCode string, withID bool) string {
	id := ""
	if withID || changeID == "none" {
		id = " change-id=" + changeID
	}
	return "[12:00:00] CI git start: run-id=r head=" + head + " tree=" + tree + id + " worktree=" + worktree + "\n" +
		"ok example\n" +
		"CI git finish: run-id=r head=" + head + " tree=" + tree + id + " worktree=" + worktree + "\n" +
		"CI gate result: run-id=r result=" + result + " exit=" + exitCode + "\n"
}

func validEvidenceBlock(changeID, subject string) string {
	logAt := time.Date(2026, 10, 12, 3, 0, 2, 0, time.UTC)
	commitAt := time.Date(2026, 10, 12, 3, 0, 1, 0, time.UTC)
	verdict := deriveGatesTheCommit(logAt, commitAt, "clean", "yes")
	return "gate-evidence: " + gateEvidenceMarker + "\n" +
		"schema-version: 2\n" +
		"gate: unit-gate\n" +
		"commit: " + subject + "\n" +
		"change-id: " + changeID + "\n" +
		"result: PASS (exit 0)\n" +
		"fail-lines: 0\n" +
		"ok-lines: 1\n" +
		"skip-lines: 0\n" +
		"log-finished: " + logAt.Format(time.RFC3339) + "\n" +
		"commit-dated: " + commitAt.Format(time.RFC3339) + "\n" +
		"gates-the-commit: " + verdict + "\n" +
		"worktree: clean\n" +
		"tested-tree: yes\n" +
		"source: logs/run.log:3 — machine-local, reclaimed with its worktree\n"
}

func writeLog(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "ci.log")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func findingHas(findings []Finding, want string) bool {
	for _, finding := range findings {
		if strings.Contains(finding.Message, want) {
			return true
		}
	}
	return false
}

func findingsText(findings []Finding) string {
	var b strings.Builder
	for _, finding := range findings {
		b.WriteString(finding.Message)
		b.WriteByte('\n')
	}
	return b.String()
}
