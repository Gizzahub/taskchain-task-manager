package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/receiptpath"
)

func TestAdoptedDiscoverySkipsReceiptCards(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	card := func(id string) string {
		return "---\nid: " + id + "\ntitle: Example\n---\n\n- [x] run | verify: `false`\n"
	}
	write("tasks/todo/001-live.md", card("TASK-1"))
	write("tasks/receipts/TASK-1/note.md", card("TASK-2"))
	write("tasks/todo/receipts/TASK-3/nested.md", card("TASK-3"))

	live, err := FindCards(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCard(live, "TASK-1") || !hasCard(live, "TASK-2") || !hasCard(live, "TASK-3") {
		t.Fatalf("unadopted discovery dropped a receipt card: %s", receiptIDs(live))
	}
	profile := receiptpath.Profile{Base: "section-9", Amendment: "section-11"}
	adopted, err := FindCardsAdopted(root, false, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !hasCard(adopted, "TASK-1") || hasCard(adopted, "TASK-2") || hasCard(adopted, "TASK-3") {
		t.Fatalf("adopted discovery rebound receipt cards: %s", receiptIDs(adopted))
	}
	if _, err := FindCardsAdopted(root, false, receiptpath.Profile{}); err == nil {
		t.Fatal("unselected profile walked the board")
	}
	loaded, err := LoadLiveCardsAdopted(root, profile)
	if err != nil || !hasCard(loaded, "TASK-1") || hasCard(loaded, "TASK-2") {
		t.Fatalf("live adopted cards: %s %v", receiptIDs(loaded), err)
	}

	report, err := ReexecCheckedBindings(context.Background(), filepath.Join(root, "tasks"), root)
	if err != nil {
		t.Fatal(err)
	}
	if report.CardsExamined != 3 || len(report.Failures) != 3 {
		t.Fatalf("unadopted reexec: examined %d failures %d", report.CardsExamined, len(report.Failures))
	}
	adoptedReport, err := ReexecCheckedBindingsAdopted(context.Background(), filepath.Join(root, "tasks"), root, profile)
	if err != nil {
		t.Fatal(err)
	}
	if adoptedReport.CardsExamined != 1 || len(adoptedReport.Failures) != 1 {
		t.Fatalf("adopted reexec: examined %d failures %d", adoptedReport.CardsExamined, len(adoptedReport.Failures))
	}
}

func hasCard(cards []*Card, id string) bool {
	for _, card := range cards {
		if card != nil && card.ID == id {
			return true
		}
	}
	return false
}

func receiptIDs(cards []*Card) string {
	out := ""
	for _, card := range cards {
		if card != nil {
			out += card.ID + " "
		}
	}
	return out
}
