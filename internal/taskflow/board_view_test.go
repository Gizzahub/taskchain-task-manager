package taskflow

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBoardEmptyAndMalformed(t *testing.T) {
	root := t.TempDir()
	var text bytes.Buffer
	RenderBoardText(&text, nil, 24, PlanInventory{})
	if got := text.String(); got != "📋 Task Board\n\n  (no tasks found under tasks/)\n" {
		t.Fatalf("empty text=%q", got)
	}
	var raw bytes.Buffer
	if err := RenderBoardJSON(&raw, nil, 24, PlanInventory{}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if string(doc["zones"]) != "[]" {
		t.Fatalf("zones=%s", doc["zones"])
	}

	if err := writeFileForTest(root, "tasks/todo/001-broken.md", []byte("---\nid: [not\n---\n\n## Summary\n\nstill here\n")); err != nil {
		t.Fatal(err)
	}
	cards, _, err := LoadBoardCards(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 {
		t.Fatalf("malformed card was dropped: %d", len(cards))
	}
	zones := ClassifyBoard(cards, nil, time.Now(), time.Hour)
	var listed bytes.Buffer
	RenderBoardText(&listed, zones, 1, DerivePlanInventory(cards))
	if !strings.Contains(listed.String(), "001-broken.md") {
		t.Fatalf("board hid the malformed card:\n%s", listed.String())
	}
}

func TestBoardStaleDoingClaim(t *testing.T) {
	root := t.TempDir()
	body := "---\nid: TASK-1\ntitle: First card\npriority: P2\nstatus: pending\ncreated: 2026-10-11\n---\n\n## Summary\n\nWork.\n"
	if err := writeFileForTest(root, "tasks/doing/001-first.md", []byte(body)); err != nil {
		t.Fatal(err)
	}
	cards, _, err := LoadBoardCards(root)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	mtimes := map[string]time.Time{cards[0].RepoRel(): when.Add(-3 * time.Hour)}
	zones := ClassifyBoard(cards, mtimes, when, 2*time.Hour)
	var text bytes.Buffer
	RenderBoardText(&text, zones, 2, DerivePlanInventory(cards))
	got := text.String()
	for _, want := range []string{
		"  doing (1)  ← claim board\n",
		"    ⚠ First card",
		"claimed 3 hours ago  ⚠ STALE (>2h)",
		"⚠ 1 stale claim(s) — a session may have died mid-work.",
		"  Recover intentionally: 'ce task move <file> todo'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
}
