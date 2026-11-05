package taskflow

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

func TestEnterAndExitRefuseAFenceTheStamperCannotSee(t *testing.T) {
	loose := strings.Replace(section9Card(), "\n---\n", "\n--- \n", 1)
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": loose})
	before := readTree(t, root)
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("unstampable fence entered manual")
	}
	if readTree(t, root) != before {
		t.Fatal("unstampable entry changed the tree")
	}

	entered := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	digest, err := EnterManual(entered, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(entered, "tasks", "manual", "001-sign.md")
	body, err := os.ReadFile(manual)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manual, []byte(strings.Replace(string(body), "\n---\n", "\n--- \n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}
	writeBoardReceipt(t, entered, section9Receipt, raw)
	if err := ExitManual(entered, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-fence", testAuthority); err == nil {
		t.Fatal("unstampable fence exited")
	}
	if _, err := os.Stat(manual); err != nil {
		t.Fatal("unstampable exit moved the card")
	}
	closeAbs := filepath.Join(entered, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".close.json")
	if _, err := os.Stat(closeAbs); !os.IsNotExist(err) {
		t.Fatal("unstampable exit published a close")
	}
	partial := evidencePartial(closeAbs, closeRequestKey("req-fence", manualGeneration))
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatal("unstampable exit staged a close")
	}
}

func TestResumeManualEntryRestoresOrFinishesWithoutAPartial(t *testing.T) {
	unstamped := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": section9Card()})
	if _, err := EnterManual(unstamped, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("pre-move resume published an entry")
	}
	if _, err := os.Stat(filepath.Join(unstamped, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("pre-move card was not restored")
	}
	if _, err := os.Stat(filepath.Join(unstamped, "tasks", "manual", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("pre-move card stayed in manual")
	}
	entryAbs := filepath.Join(unstamped, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	if _, err := os.Stat(entryAbs); !os.IsNotExist(err) {
		t.Fatal("pre-move resume published an entry")
	}

	settledCard := strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1)
	settled := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": settledCard})
	if _, err := EnterManual(settled, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("status manual without a baseline published an entry")
	}
	if _, err := os.Stat(filepath.Join(settled, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("unbaselined card left manual")
	}
	if _, err := os.Stat(filepath.Join(settled, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); !os.IsNotExist(err) {
		t.Fatal("unbaselined card published an entry")
	}

	tampered := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": settledCard})
	other := settledCard + "\nchanged body\n"
	otherFields, err := manualzone.ParseCard(other)
	if err != nil {
		t.Fatal(err)
	}
	otherDigest, err := CanonicalReviewDigest(other)
	if err != nil {
		t.Fatal(err)
	}
	otherRecord, err := manualzone.NewEntryRecord(section11(), otherFields, testAuthority, other, otherDigest)
	if err != nil {
		t.Fatal(err)
	}
	otherRaw, err := manualzone.Encode(otherRecord)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(tampered, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	partial := evidencePartial(final, entryRequestKey("TASK-001", manualGeneration))
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, otherRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := EnterManual(tampered, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("partial for a different body published an entry")
	}
	if _, err := os.Stat(final); !os.IsNotExist(err) {
		t.Fatal("mismatched partial published the entry")
	}
	if _, err := os.Stat(filepath.Join(tampered, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("mismatched partial moved the card")
	}

	preMovePartial := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": section9Card()})
	plantEntryPartial(t, preMovePartial, otherRaw)
	if _, err := EnterManual(preMovePartial, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("pre-move card with a different partial published an entry")
	}
	if _, err := os.Stat(filepath.Join(preMovePartial, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("pre-move card with a different partial was not restored")
	}
	if _, err := os.Stat(filepath.Join(preMovePartial, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); !os.IsNotExist(err) {
		t.Fatal("pre-move card with a different partial published an entry")
	}
}

func TestResumeManualEntryPublishesOnlyTheStagedBaseline(t *testing.T) {
	already := strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1)
	preMove := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": already})
	writeEntryBaseline(t, preMove, already)
	if _, err := EnterManual(preMove, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("pre-move card whose status is already manual published an entry")
	}
	if _, err := os.Stat(filepath.Join(preMove, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("pre-move status manual card was not restored")
	}
	if _, err := os.Stat(filepath.Join(preMove, "tasks", "manual", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("pre-move status manual card stayed in manual")
	}

	linked := strings.Replace(section9Card(), "body\n", "body\n\nSee [other](002-other.md)\n", 1)
	stamped, ok := stampFrontmatterContent([]byte(linked), [][2]string{{"status", "manual"}})
	if !ok {
		t.Fatal("fixture stamp failed")
	}
	resume := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": string(stamped)})
	writeEntryBaseline(t, resume, linked)
	if _, err := EnterManual(resume, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(resume, "tasks", "manual", "001-sign.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "../todo/002-other.md") {
		t.Fatalf("stamped baseline was not retargeted: %s", body)
	}
	if _, err := os.Stat(filepath.Join(resume, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); err != nil {
		t.Fatal("stamped baseline did not publish the entry")
	}

	edited := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": string(stamped) + "\nchanged body\n"})
	writeEntryBaseline(t, edited, linked)
	if _, err := EnterManual(edited, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("edited body published an entry")
	}
	if _, err := os.Stat(filepath.Join(edited, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("edited body left manual")
	}
	if _, err := os.Stat(filepath.Join(edited, "tasks", "todo", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("edited body was restored")
	}
	if _, err := os.Stat(filepath.Join(edited, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); !os.IsNotExist(err) {
		t.Fatal("edited body published an entry")
	}
}

func writeEntryBaseline(t *testing.T, root, card string) {
	t.Helper()
	final := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	partial := evidencePartial(final, entryRequestKey("TASK-001", manualGeneration))
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResumeRefusesAnUnstampableFenceWithoutMoving(t *testing.T) {
	loose := strings.Replace(section9Card(), "\n---\n", "\n--- \n", 1)
	root := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": loose})
	before := readTree(t, root)
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("unstampable entry resume published")
	}
	if readTree(t, root) != before {
		t.Fatal("unstampable entry resume changed the tree")
	}

	manualText := strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1)
	looseManual := strings.Replace(manualText, "\n---\n", "\n--- \n", 1)
	matched := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": looseManual})
	digest, err := CanonicalReviewDigest(manualText)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := manualzone.ParseCard(manualText)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manualzone.NewEntryRecord(section11(), fields, testAuthority, manualText, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	plantEntryPartial(t, matched, raw)
	before = readTree(t, matched)
	if _, err := EnterManual(matched, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("unstampable matching partial published an entry")
	}
	if readTree(t, matched) != before {
		t.Fatal("unstampable matching partial changed the tree")
	}

	exited := resumeBoard(t, section9Card())
	dst := parkManualCard(t, exited, "todo")
	body, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	loosened := strings.Replace(string(body), "\n---\n", "\n--- \n", 1)
	if loosened == string(body) {
		t.Fatal("exit fixture fence did not change")
	}
	if err := os.WriteFile(dst, []byte(loosened), 0o644); err != nil {
		t.Fatal(err)
	}
	before = readTree(t, exited)
	if err := ExitManual(exited, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-loose-resume", testAuthority); err == nil {
		t.Fatal("unstampable exit resume published a close")
	}
	if readTree(t, exited) != before {
		t.Fatal("unstampable exit resume changed the tree")
	}
}

func TestResumeManualEntryRecoversACRLFFence(t *testing.T) {
	crlf := strings.ReplaceAll(section9Card(), "\n", "\r\n")
	root := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": crlf})
	writeEntryBaseline(t, root, crlf)
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("crlf pre-move resume published an entry")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("crlf pre-move card was not restored")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("crlf pre-move card stayed in manual")
	}

	settled := strings.ReplaceAll(strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1), "\n", "\r\n")
	done := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": settled})
	digest, err := CanonicalReviewDigest(settled)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := manualzone.ParseCard(strings.ReplaceAll(settled, "\r\n", "\n"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := manualzone.NewEntryRecord(section11(), fields, testAuthority, settled, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	final := filepath.Join(done, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := EnterManual(done, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	if got != digest {
		t.Fatalf("crlf entry digest %s", got)
	}
	if _, err := os.Stat(filepath.Join(done, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("acknowledged crlf entry moved the card")
	}
}

func plantEntryPartial(t *testing.T, root string, raw []byte) {
	t.Helper()
	final := filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")
	partial := evidencePartial(final, entryRequestKey("TASK-001", manualGeneration))
	if err := os.MkdirAll(filepath.Dir(partial), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(partial, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCRLFManualCardCanExitAndReissue(t *testing.T) {
	settled := strings.ReplaceAll(strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1), "\n", "\r\n")
	digest, err := CanonicalReviewDigest(settled)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := manualzone.ParseCard(settled)
	if err != nil {
		t.Fatal(err)
	}
	record, err := manualzone.NewEntryRecord(section11(), fields, testAuthority, settled, digest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := manualzone.Encode(record)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := manualzone.Encode(doneReceipt(digest))
	if err != nil {
		t.Fatal(err)
	}

	exitRoot := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": settled})
	writeBoardReceipt(t, exitRoot, "receipts/TASK-001/manual-"+manualGeneration+".entry.json", raw)
	writeBoardReceipt(t, exitRoot, section9Receipt, receipt)
	if err := ExitManual(exitRoot, "tasks/manual/001-sign.md", section11(), section9Receipt, "todo", "", "", "req-crlf", testAuthority); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(exitRoot, "tasks", "todo", "001-sign.md")); err != nil {
		t.Fatal("crlf exit did not move the card")
	}

	reissueRoot := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": settled})
	writeBoardReceipt(t, reissueRoot, "receipts/TASK-001/manual-"+manualGeneration+".entry.json", raw)
	useHumanWriter(t)
	req := manualzone.ReissueRequest{
		ActorKind: "human", Reason: "crlf card", RequestID: "reissue-crlf",
		PreviousGeneration: manualGeneration, PreviousDigest: digest, CurrentDigest: digest,
	}
	if _, err := ReissueManual(reissueRoot, "tasks/manual/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority, req); err != nil {
		t.Fatal(err)
	}
}

func TestReissueRefusesAnUnstampableFence(t *testing.T) {
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": section9Card()})
	digest, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(root, "tasks", "manual", "001-sign.md")
	body, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	loose := strings.Replace(string(body), "\n---\n", "\n--- \n", 1)
	if loose == string(body) {
		t.Fatal("fixture fence did not change")
	}
	if err := os.WriteFile(cardPath, []byte(loose), 0o644); err != nil {
		t.Fatal(err)
	}
	before := readTree(t, root)
	useHumanWriter(t)
	current, err := CanonicalReviewDigest(loose)
	if err != nil {
		t.Fatal(err)
	}
	req := manualzone.ReissueRequest{
		ActorKind: "human", Reason: "loose fence", RequestID: "reissue-fence",
		PreviousGeneration: manualGeneration, PreviousDigest: digest, CurrentDigest: current,
	}
	if _, err := ReissueManual(root, "tasks/manual/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority, req); err == nil {
		t.Fatal("unstampable fence reissued")
	}
	if readTree(t, root) != before {
		t.Fatal("unstampable reissue changed the tree")
	}
}

func TestResumePublishesACRLFStatusStamp(t *testing.T) {
	crlf := strings.ReplaceAll(strings.Replace(section9Card(), "status: todo\n", "status: manual\n", 1), "\n", "\r\n")
	stamped, ok := stampFrontmatterContent([]byte(crlf), [][2]string{{"status", "manual"}})
	if !ok {
		t.Fatal("crlf status stamp failed")
	}
	if bytes.Equal(stamped, []byte(crlf)) {
		t.Fatal("crlf status stamp did not rewrite the line")
	}
	before, err := CanonicalReviewDigest(crlf)
	if err != nil {
		t.Fatal(err)
	}
	after, err := CanonicalReviewDigest(string(stamped))
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("crlf status stamp changed the digest")
	}
	root := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": string(stamped)})
	writeEntryBaseline(t, root, crlf)
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("crlf status stamp was moved back")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "todo", "001-sign.md")); !os.IsNotExist(err) {
		t.Fatal("crlf status stamp restored the source")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); err != nil {
		t.Fatal("crlf status stamp did not publish the entry")
	}
}
