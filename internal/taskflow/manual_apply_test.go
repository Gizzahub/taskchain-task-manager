package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

const (
	manualGeneration = "0123456789abcdef0123456789abcdef"
	section9Receipt  = "receipts/TASK-001/manual-" + manualGeneration + ".json"
	testAuthority    = "board-test"
)

func manualCard(status, operator, receiptPath string) string {
	return "---\nid: TASK-001\ntitle: sign\nstatus: " + status + "\nmanual-action: sign the release form\noperator: " + operator + "\nexpected-receipt: {path: " + receiptPath + ", facts: the form is signed}\n---\n\n## Summary\n\nbody\n"
}

func section9Card() string {
	return manualCard("todo", "{kind: human, id: release-owner}", "receipts/TASK-001/manual-"+manualGeneration+".json")
}

func section11() manualzone.Profile {
	return manualzone.Profile{Base: "section-9", Amendment: "section-11"}
}

func useHumanWriter(t *testing.T) {
	t.Helper()
	t.Setenv("CE_TASK_ACTOR", "")
	t.Setenv("GIT_WORK_ACTOR", "release-owner")
	t.Setenv("GIT_WORK_HOST", "mbp")
}

func writeBoardReceipt(t *testing.T, root, rel string, raw []byte) {
	t.Helper()
	abs := filepath.Join(root, "tasks", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnterManualWritesEntryAndLeavesUnselectedBoardsUntouched(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	before := readTree(t, root)
	idle := manualzone.RuntimeState{Known: true}
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", manualzone.Profile{Base: "section-9", Amendment: "none"}, idle, testAuthority); err == nil {
		t.Fatal("unselected profile entered manual")
	}
	if got := readTree(t, root); got != before {
		t.Fatalf("unselected profile changed the tree\nbefore %s\nafter %s", before, got)
	}

	digest, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), idle, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(root, "tasks", "manual", "001-sign.md")
	body, err := os.ReadFile(moved)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "status: manual") {
		t.Fatalf("status not stamped: %s", body)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("source card still present")
	}
	entryPath := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	entryRaw, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		t.Fatal(err)
	}
	if entry.EntrySHA256 != digest || entry.Subject != string(body) || entry.BoardAuthority != testAuthority || entry.Scope != "card" {
		t.Fatalf("entry digest does not bind the settled card: %+v", entry)
	}
	again, err := CanonicalReviewDigest(string(body))
	if err != nil || again != digest {
		t.Fatalf("recomputed digest %q err %v, want %q", again, err, digest)
	}
}

func TestEnterManualRefusesActiveRunAndScalarOperatorWithoutWriting(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/doing/001-sign.md": section9Card()})
	before := readTree(t, root)
	profile := section11()
	if _, err := EnterManual(root, "tasks/doing/001-sign.md", profile, manualzone.RuntimeState{Known: true, ActiveRun: true}, testAuthority); err == nil {
		t.Fatal("active run entered manual")
	}
	if _, err := EnterManual(root, "tasks/doing/001-sign.md", profile, manualzone.RuntimeState{}, testAuthority); err == nil {
		t.Fatal("unknown run state entered manual")
	}
	scalar := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": manualCard("todo", "release-owner", "receipts/TASK-001/manual-"+manualGeneration+".json")})
	scalarBefore := readTree(t, scalar)
	if _, err := EnterManual(scalar, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("scalar operator entered manual")
	}
	if readTree(t, root) != before || readTree(t, scalar) != scalarBefore {
		t.Fatal("refused entry changed a board")
	}
}

func TestExitManualDoneAndRejectsCancelled(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	profile := section11()
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	receipt := doneReceipt(digest)
	raw, err := manualzone.Encode(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.Result = "rejected"
	rejected, _ := manualzone.Encode(receipt)
	writeBoardReceipt(t, root, section9Receipt, rejected)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "cancelled", "no", "TASK-9", "req-1", testAuthority); err == nil {
		t.Fatal("cancelled exit was accepted")
	}
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, "receipts/TASK-001/other.json", "todo", "", "", "req-1", testAuthority); err == nil {
		t.Fatal("mismatched receipt path was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("refused exit moved the card")
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-1", testAuthority); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal(err)
	}
	closePath := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".close.json")
	if _, err := os.Stat(closePath); err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(root, "tasks/todo/001-sign.md", profile, section9Receipt, "todo", "", "", "req-2", testAuthority); err == nil {
		t.Fatal("replayed a closed entry")
	}
}

func doneReceipt(digest string) manualzone.Receipt {
	return manualzone.Receipt{
		Version: 1, CardID: "TASK-001", EntryGeneration: manualGeneration, EntrySHA256: digest,
		Action: "sign the release form", Operator: manualzone.Operator{Kind: "human", ID: "release-owner"},
		ActorKind: "human", PerformedAt: "2026-11-05T00:00:00Z", Result: "done",
		Facts: "signed at the desk", ExpectedFacts: "the form is signed",
	}
}

func TestExitManualRejectedRecordsBlockedBy(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/blocked/001-sign.md": manualCard("blocked", "{kind: human, id: release-owner}", "receipts/TASK-001/manual-"+manualGeneration+".json")})
	profile := section11()
	digest, err := EnterManual(root, "tasks/blocked/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	receipt := manualzone.Receipt{
		Version: 1, CardID: "TASK-001", EntryGeneration: manualGeneration, EntrySHA256: digest,
		Action: "sign the release form", Operator: manualzone.Operator{Kind: "human", ID: "release-owner"},
		ActorKind: "human", PerformedAt: "2026-11-05T00:00:00Z", Result: "rejected",
		Facts: "the form was refused", ExpectedFacts: "the form is signed",
	}
	raw, err := manualzone.Encode(receipt)
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, raw)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "blocked", "", "TASK-009", "req-1", testAuthority); err == nil {
		t.Fatal("missing reason was accepted")
	}
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "blocked", "operator refused", "TASK-009", "req-1", testAuthority); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, "tasks", "blocked", "001-sign.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "blocked-by: TASK-009") || !strings.Contains(string(body), "status: blocked") {
		t.Fatalf("blocked exit was not recorded: %s", body)
	}
}

func TestSection10ReceiptStaysOutsideTheBoard(t *testing.T) {
	path := "docs/task-reviews/TASK-001/manual-" + manualGeneration + ".json"
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": manualCard("todo", "{kind: human, id: release-owner}", path)})
	profile := manualzone.Profile{Base: "section-10", Amendment: "section-11"}
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", profile, manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "docs", "task-reviews", "TASK-001", "manual-"+manualGeneration+".entry.json")
	if _, err := os.Stat(entry); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "receipts")); !os.IsNotExist(err) {
		t.Fatal("section-10 entry wrote a board receipt directory")
	}
}

func TestEvidenceSymlinkIsRefused(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "tasks", "receipts")); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "tasks", "todo", "001-sign.md")
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("symlink evidence path was accepted")
	}
	after, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("refused symlink entry rewrote the card")
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatalf("wrote through the symlink: %d entries", len(entries))
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("refused symlink entry lost the source card")
	}
}

func TestReissueManualRetiresTheEntryAndRepeatsTheSameRequest(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	profile := section11()
	idle := manualzone.RuntimeState{Known: true}
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", profile, idle, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "tasks", "manual", "001-sign.md")
	body, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := string(body) + "\nedited outside the writer\n"
	if err := os.WriteFile(cardPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err := CanonicalReviewDigest(edited)
	if err != nil {
		t.Fatal(err)
	}
	if current == digest {
		t.Fatal("edit did not change the digest")
	}
	stale := manualzone.Receipt{
		Version: 1, CardID: "TASK-001", EntryGeneration: manualGeneration, EntrySHA256: digest,
		Action: "sign the release form", Operator: manualzone.Operator{Kind: "human", ID: "release-owner"},
		ActorKind: "human", PerformedAt: "2026-11-05T00:00:00Z", Result: "done",
		Facts: "signed at the desk", ExpectedFacts: "the form is signed",
	}
	staleRaw, err := manualzone.Encode(stale)
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, root, section9Receipt, staleRaw)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-stale", testAuthority); err == nil {
		t.Fatal("stale receipt exited after an external edit")
	}
	req := manualzone.ReissueRequest{
		ActorKind: "agent", Reason: "operator edited the card", RequestID: "reissue-1",
		PreviousGeneration: manualGeneration, PreviousDigest: digest, CurrentDigest: current,
	}
	before := readTree(t, root)
	t.Setenv("CE_TASK_ACTOR", "claude")
	t.Setenv("GIT_WORK_ACTOR", "celee")
	t.Setenv("GIT_WORK_HOST", "mbp")
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req); err == nil {
		t.Fatal("conflicting writer sources reissued")
	}
	t.Setenv("GIT_WORK_ACTOR", "")
	req.ActorKind = "human"
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req); err == nil {
		t.Fatal("agent writer reissued")
	}
	useHumanWriter(t)
	req.ActorKind = "agent"
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req); err == nil {
		t.Fatal("agent request kind reissued")
	}
	req.ActorKind = "human"
	req.CurrentDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, manualzone.RuntimeState{}, testAuthority, req); err == nil {
		t.Fatal("unreadable run state reissued")
	}
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req); err == nil {
		t.Fatal("mismatched current digest reissued")
	}
	if readTree(t, root) != before {
		t.Fatal("refused reissue changed the tree")
	}
	req.CurrentDigest = current
	post, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req)
	if err != nil {
		t.Fatal(err)
	}
	again, err := ReissueManual(root, "tasks/manual/001-sign.md", profile, idle, testAuthority, req)
	if err != nil {
		t.Fatal(err)
	}
	if again != post {
		t.Fatalf("same request issued another digest %s, want %s", again, post)
	}
	settled, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(settled), "manual-") || strings.Contains(string(settled), manualGeneration) {
		t.Fatalf("card still binds the retired generation: %s", settled)
	}
	fields, err := manualzone.ParseCard(string(settled))
	if err != nil {
		t.Fatal(err)
	}
	_, entryRel, _, err := manualzone.ReceiptNames(profile, fields)
	if err != nil {
		t.Fatal(err)
	}
	entryRaw, err := os.ReadFile(filepath.Join(root, "tasks", filepath.FromSlash(entryRel)))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Subject != string(settled) || entry.EntrySHA256 != post {
		t.Fatal("new entry does not bind the reissued card")
	}
	oldEntry := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	if _, err := os.Stat(oldEntry); err != nil {
		t.Fatal("reissue removed the previous entry")
	}
	oldClose := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".close.json")
	closeRaw, err := os.ReadFile(oldClose)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(closeRaw), `"result":"reissued"`) {
		t.Fatalf("close record: %s", closeRaw)
	}
	fresh := stale
	fresh.EntryGeneration = entry.EntryGeneration
	fresh.EntrySHA256 = post
	freshRaw, err := manualzone.Encode(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, section9Receipt, "todo", "", "", "req-old", testAuthority); err == nil {
		t.Fatal("retired receipt was accepted")
	}
	writeBoardReceipt(t, root, fields.ReceiptPath, freshRaw)
	if err := ExitManual(root, "tasks/manual/001-sign.md", profile, fields.ReceiptPath, "todo", "", "", "req-new", testAuthority); err != nil {
		t.Fatal(err)
	}
}

func TestUnadoptedVocabularyDoesNotRecognizeManual(t *testing.T) {
	if _, ok := ResolveMoveDestination("manual"); ok {
		t.Fatal("unadopted move vocabulary recognized manual")
	}
}
