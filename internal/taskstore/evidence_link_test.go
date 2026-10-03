package taskstore

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func evidenceReceipt(eventID, taskID string) []byte {
	return []byte(fmt.Sprintf(`{"schema_version":1,"contract":"agent-interruption-v1","event":{"id":%q,"runtime":"test","hook":"stop","session_id":"session","turn_id":"turn","task_id":%q,"original_payload_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{},"received_at":"2026-10-03T10:00:00Z"},"classification":{"event_id":%q,"reason":"normal_stop","certainty":"suspected","evidence":[],"provider":"test","model":"synthetic","identity_source":"unknown"}}`, eventID, taskID, eventID))
}

func claudeStopReceipt() []byte {
	return []byte(`{"schema_version":1,"contract":"agent-interruption-v1","event":{"id":"claude-stop-1","runtime":"claude","hook":"Stop","session_id":"session","original_payload_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{},"received_at":"2026-10-03T10:00:00Z"},"classification":{"event_id":"claude-stop-1","reason":"unknown","certainty":"unknown","provider":"unknown","model":"unknown"}}`)
}

func evidenceDigest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

func TestEvidenceLinkIsImmutableAndLeavesCardBytesUntouched(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	cardPath := filepath.Join(board, "todo", "TASK-1.md")
	before, err := os.ReadFile(cardPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := evidenceReceipt("event-1", "TASK-1")
	got, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), "synthetic-receipt.json")
	if err != nil || !got.Registered || got.EventID != "event-1" {
		t.Fatalf("link=%+v err=%v", got, err)
	}
	after, err := os.ReadFile(cardPath)
	if err != nil || string(before) != string(after) {
		t.Fatalf("card changed: %v", err)
	}
	again, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), "synthetic-receipt.json")
	if err != nil || again.Registered {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
	conflict := evidenceReceipt("event-1", "TASK-1")
	conflict = []byte(strings.Replace(string(conflict), "normal_stop", "check_failed", 1))
	if _, err := LinkEvidence(board, "TASK-1", conflict, evidenceDigest(conflict), "synthetic-receipt.json"); err == nil || !strings.Contains(err.Error(), "different immutable") {
		t.Fatalf("conflict=%v", err)
	}
	links, err := EvidenceLinks(board, "TASK-1")
	if err != nil || len(links) != 1 || links[0].ReceiptRef != "synthetic-receipt.json" {
		t.Fatalf("links=%+v err=%v", links, err)
	}
}

func TestEvidenceLinkRejectsDigestAndIdentityMismatches(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	raw := evidenceReceipt("event-2", "TASK-2")
	if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("task mismatch=%v", err)
	}
	if _, err := LinkEvidence(board, "TASK-1", evidenceReceipt("event-3", "TASK-1"), strings.Repeat("0", 64), ""); err == nil || !strings.Contains(err.Error(), "does not match expected") {
		t.Fatalf("digest mismatch=%v", err)
	}
}

func TestEvidenceLinkAcceptsClaudeStopWithoutTurnAndEmptyEvidence(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	raw := claudeStopReceipt()
	if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err != nil {
		t.Fatal(err)
	}
}
