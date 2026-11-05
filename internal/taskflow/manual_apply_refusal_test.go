package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

func TestEmptyAuthorityAndAnotherManualLinkWriteNothing(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	before := readTree(t, root)
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, ""); err == nil {
		t.Fatal("empty authority entered manual")
	}
	if readTree(t, root) != before {
		t.Fatal("empty authority changed the tree")
	}
	other := "---\nid: TASK-002\ntitle: other\nstatus: manual\n---\n\nSee [sign](../todo/001-sign.md)\n"
	linked := initBoardForTest(t, map[string]string{
		"tasks/todo/001-sign.md":    section9Card(),
		"tasks/manual/002-other.md": other,
	})
	linkedBefore := readTree(t, linked)
	if _, err := EnterManual(linked, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("entry rewrote another manual card")
	}
	if readTree(t, linked) != linkedBefore {
		t.Fatal("refused manual link changed the tree")
	}
}

func TestExitManualAllowsExcludedReviewFieldsOnly(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	profile := section11()
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(root, "tasks", "manual", "001-sign.md")
	if _, err := stampFrontmatterField(card, "quality-review", "pass"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	again, err := CanonicalReviewDigest(string(body))
	if err != nil || again != digest {
		t.Fatalf("excluded field changed the digest: %s %v", again, err)
	}
	raw, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-review", testAuthority); err != nil {
		t.Fatal(err)
	}

	editedRoot := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	editedDigest, err := EnterManual(editedRoot, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(editedRoot, "tasks", "manual", "001-sign.md")
	current, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(edited, append(current, []byte("\nchanged body\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := manualzone.Encode(doneReceipt(editedDigest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, editedRoot, section9Receipt, stale)
	if err := ExitManual(editedRoot, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-body", testAuthority); err == nil {
		t.Fatal("body edit exited")
	}
	if _, err := os.Stat(edited); err != nil {
		t.Fatal("refused body edit moved the card")
	}
}

func TestExitManualRefusesSymlinkReceiptAndForeignPartial(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	profile := section11()
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(outside, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	receiptAbs := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".json")
	if err := os.Symlink(outside, receiptAbs); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-link", testAuthority); err == nil {
		t.Fatal("symlink receipt exited")
	}
	after, err := os.ReadFile(outside)
	if err != nil || string(before) != string(after) {
		t.Fatal("symlink receipt was written through")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("symlink receipt moved the card")
	}

	special := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	if _, err := EnterManual(special, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	specialReceipt := filepath.Join(special, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".json")
	if err := os.Mkdir(specialReceipt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(special, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-dir", testAuthority); err == nil {
		t.Fatal("directory receipt exited")
	}

	partialRoot := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	partialDigest, err := EnterManual(partialRoot, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	partialRaw, err := manualzone.Encode(doneReceipt(partialDigest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, partialRoot, section9Receipt, partialRaw)
	closeAbs := filepath.Join(partialRoot, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".close.json")
	foreign := evidencePartial(closeAbs, closeRequestKey("other-req", manualGeneration))
	if err := os.WriteFile(foreign, []byte("other"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(partialRoot, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-1", testAuthority); err == nil {
		t.Fatal("foreign partial exited")
	}
	if _, err := os.Stat(filepath.Join(partialRoot, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("foreign partial moved the card")
	}
	ours := evidencePartial(closeAbs, closeRequestKey("req-1", manualGeneration))
	if err := os.WriteFile(ours, []byte("{short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(partialRoot, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-1", testAuthority); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ours); !os.IsNotExist(err) {
		t.Fatal("same-request partial was left behind")
	}
	if _, err := os.Stat(closeAbs); err != nil {
		t.Fatal(err)
	}
}

func TestExitManualResumeRejectsDestinationBodyEdit(t *testing.T) {
	root := resumeBoard(t, section9Card())
	dst := parkManualCard(t, root, "todo")
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, append(body, []byte("\nchanged body\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(root, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-body", testAuthority); err == nil {
		t.Fatal("changed destination body resumed")
	}
	assertNoClose(t, root, "req-body")
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("refused resume recreated the source")
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("refused resume moved the destination")
	}

	plain := resumeBoard(t, section9Card())
	parkManualCard(t, plain, "todo")
	if err := ExitManual(plain, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-plain", testAuthority); err != nil {
		t.Fatal(err)
	}

	reviewed := resumeBoard(t, section9Card())
	reviewedDest := parkManualCard(t, reviewed, "todo")
	if _, err := stampFrontmatterField(reviewedDest, "quality-review", "pass"); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(reviewed, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-review-resume", testAuthority); err != nil {
		t.Fatal(err)
	}

	stamped := resumeBoard(t, section9Card())
	stampedDest := parkManualCard(t, stamped, "todo")
	if _, err := stampFrontmatterField(stampedDest, "status", "todo"); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(stamped, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-stamped", testAuthority); err != nil {
		t.Fatal(err)
	}

	linkedCard := strings.Replace(manualCard("manual", "{kind: human, id: release-owner}", section9Receipt), "body\n", "body\n\nSee [other](002-other.md)\n", 1)
	linked := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": linkedCard})
	linkedFields, err := manualzone.ParseCard(linkedCard)
	if err != nil {
		t.Fatal(err)
	}
	linkedDigest, err := CanonicalReviewDigest(linkedCard)
	if err != nil {
		t.Fatal(err)
	}
	linkedEntry, err := manualzone.NewEntryRecord(section11(), linkedFields, testAuthority, linkedCard, linkedDigest)
	if err != nil {
		t.Fatal(err)
	}
	linkedEntryRaw, err := manualzone.Encode(linkedEntry)
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, linked, "receipts/TASK-001/manual-"+manualGeneration+".entry.json", linkedEntryRaw)
	linkedReceipt, err := manualzone.Encode(doneReceipt(linkedDigest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, linked, section9Receipt, linkedReceipt)
	linkedDest := parkManualCard(t, linked, "todo")
	if _, err := stampFrontmatterField(linkedDest, "status", "todo"); err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(linkedDest)
	if err != nil {
		t.Fatal(err)
	}
	retargeted, rewrites := retargetOutbound(string(current), "tasks/manual", "tasks/todo")
	if len(rewrites) == 0 {
		t.Fatal("fixture link was not retargeted")
	}
	if err := os.WriteFile(linkedDest, []byte(retargeted), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(linked, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-link-resume", testAuthority); err != nil {
		t.Fatal(err)
	}

	blocked := resumeBlockedBoard(t)
	blockedDest := parkManualCard(t, blocked, "blocked")
	if _, err := stampFrontmatterField(blockedDest, "status", "blocked"); err != nil {
		t.Fatal(err)
	}
	if _, err := stampFrontmatterField(blockedDest, "blocked-by", "TASK-009"); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(blocked, "tasks/manual/001-sign.md", section11(), section9Receipt, "blocked", "operator refused", "TASK-009", "req-blocked", testAuthority); err != nil {
		t.Fatal(err)
	}
}

func resumeBoard(t *testing.T, card string) string {
	t.Helper()
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": card})
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	return root
}

func resumeBlockedBoard(t *testing.T) string {
	t.Helper()
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(manualzone.Receipt{
		Version: 1, CardID: "TASK-001", EntryGeneration: manualGeneration, EntrySHA256: digest,
		Action: "sign the release form", Operator: manualzone.Operator{Kind: "human", ID: "release-owner"},
		ActorKind: "human", PerformedAt: "2026-11-05T00:00:00Z", Result: "rejected",
		Facts: "the form was refused", ExpectedFacts: "the form is signed",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	return root
}

func parkManualCard(t *testing.T, root, zone string) string {
	t.Helper()
	src := filepath.Join(root, "tasks", "manual", "001-sign.md")
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "tasks", zone, "001-sign.md")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	return dst
}

func assertNoClose(t *testing.T, root, requestID string) {
	t.Helper()
	closeAbs := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".close.json")
	if _, err := os.Stat(closeAbs); !os.IsNotExist(err) {
		t.Fatal("close was published")
	}
	partial := evidencePartial(closeAbs, closeRequestKey(requestID, manualGeneration))
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("close partial was published")
	}
}
