package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/manualzone"
)

func TestResumeDoesNotPublishADigestNeutralFrontmatterEdit(t *testing.T) {
	stamped, ok := stampFrontmatterContent([]byte(section9Card()), [][2]string{{"status", "manual"}})
	if !ok {
		t.Fatal("fixture stamp failed")
	}
	edited := strings.Replace(string(stamped), "\n---\n", "\nquality-review: pass\n---\n", 1)
	plain, err := CanonicalReviewDigest(string(stamped))
	if err != nil {
		t.Fatal(err)
	}
	neutral, err := CanonicalReviewDigest(edited)
	if err != nil {
		t.Fatal(err)
	}
	if plain != neutral || edited == string(stamped) {
		t.Fatal("quality-review fixture is not a digest-neutral byte edit")
	}
	root := initBoardForTest(t, map[string]string{"tasks/manual/001-sign.md": edited})
	writeEntryBaseline(t, root, section9Card())
	if _, err := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority); err == nil {
		t.Fatal("digest-neutral edit published an entry")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "manual", "001-sign.md")); err != nil {
		t.Fatal("digest-neutral edit left manual")
	}
	if _, err := os.Stat(filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json")); !os.IsNotExist(err) {
		t.Fatal("digest-neutral edit published an entry")
	}
}

func TestFalseFenceDoesNotAdmitTheEarlierReceipt(t *testing.T) {
	const early = "receipts/TASK-001/manual-" + manualGeneration + ".json"
	const late = "receipts/TASK-001/manual-ffffffffffffffffffffffffffffffff.json"
	card := strings.Replace(section9Card(), "\n---\n", "\n---hidden\nexpected-receipt: {path: "+late+", facts: the form is signed}\n---\n", 1)
	fields, err := manualzone.ParseCard(card)
	if err == nil && fields.ReceiptPath != late {
		t.Fatalf("parser receipt %q, want refusal or %q", fields.ReceiptPath, late)
	}
	sum, sumErr := CanonicalReviewDigest(card)
	if sumErr == nil {
		changedLate := strings.Replace(card, late, "receipts/TASK-001/manual-eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee.json", 1)
		lateSum, err := CanonicalReviewDigest(changedLate)
		if err != nil || lateSum == sum {
			t.Fatal("digest ignored the receipt after the false fence")
		}
		changedEarly := strings.Replace(card, early, "receipts/TASK-001/manual-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.json", 1)
		earlySum, err := CanonicalReviewDigest(changedEarly)
		if err != nil || earlySum != sum {
			t.Fatal("digest kept the receipt in front of the false fence")
		}
		if err == nil && fields.ReceiptPath != late {
			t.Fatal("parser and digest disagree on the receipt after the false fence")
		}
	}
	root := initBoardForTest(t, map[string]string{"tasks/todo/001-sign.md": card})
	before := readTree(t, root)
	digest, enterErr := EnterManual(root, "tasks/todo/001-sign.md", section11(), manualzone.RuntimeState{Known: true}, testAuthority)
	if enterErr != nil {
		if readTree(t, root) != before {
			t.Fatal("false fence changed the tree")
		}
		return
	}
	entryRaw, err := os.ReadFile(filepath.Join(root, "tasks", "receipts", "TASK-001", "manual-"+manualGeneration+".entry.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := manualzone.DecodeEntry(entryRaw)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ReceiptPath != late || digest == "" {
		t.Fatalf("entry bound %q", entry.ReceiptPath)
	}
}
