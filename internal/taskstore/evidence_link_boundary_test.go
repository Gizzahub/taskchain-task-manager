package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func evidenceBoard(t *testing.T) string {
	t.Helper()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	return board
}

func TestEvidenceLinkRejectsMalformedReceipts(t *testing.T) {
	for name, mutate := range map[string]func(string) string{
		"duplicate": func(s string) string {
			return strings.Replace(s, `"contract":"agent-interruption-v1"`, `"contract":"agent-interruption-v1","contract":"agent-interruption-v1"`, 1)
		},
		"unknown":  func(s string) string { return strings.Replace(s, `"event":{`, `"extra":true,"event":{`, 1) },
		"trailing": func(s string) string { return s + "x" },
		"mismatched ID": func(s string) string {
			return strings.Replace(s, `"event_id":"event-malformed"`, `"event_id":"other"`, 1)
		},
		"wrong enum":     func(s string) string { return strings.Replace(s, `"reason":"normal_stop"`, `"reason":"bad"`, 1) },
		"wrong evidence": func(s string) string { return strings.Replace(s, `"evidence":[]`, `"evidence":{}`, 1) },
		"bad timestamp":  func(s string) string { return strings.Replace(s, `2026-10-03T10:00:00Z`, `not-a-time`, 1) },
	} {
		t.Run(name, func(t *testing.T) {
			board := evidenceBoard(t)
			raw := []byte(mutate(string(evidenceReceipt("event-malformed", "TASK-1"))))
			if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err == nil {
				t.Fatal("malformed receipt accepted")
			}
		})
	}
}

func TestEvidenceLinkRejectsMissingAndAmbiguousTask(t *testing.T) {
	board := evidenceBoard(t)
	raw := evidenceReceipt("event-missing", "TASK-1")
	if _, err := LinkEvidence(board, "TASK-2", raw, evidenceDigest(raw), ""); err == nil {
		t.Fatal("missing task accepted")
	}
	card, err := os.ReadFile(filepath.Join(board, "todo", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	card = []byte(strings.Replace(string(card), "TASK-1", "TASK-01", 1))
	if err := os.WriteFile(filepath.Join(board, "todo", "TASK-01.md"), card, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err == nil {
		t.Fatal("ambiguous task accepted")
	}
}

func TestEvidenceLinkRejectsSymlinkAndPreservesExternalData(t *testing.T) {
	board := evidenceBoard(t)
	external := filepath.Join(t.TempDir(), "external")
	if err := os.Mkdir(external, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(external, "marker")
	if err := os.WriteFile(marker, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(board, ".task-manager-context"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(board, ".task-manager-context", "evidence-links")); err != nil {
		t.Fatal(err)
	}
	raw := evidenceReceipt("event-symlink", "TASK-1")
	if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err == nil {
		t.Fatal("symlink path accepted")
	}
	got, err := os.ReadFile(marker)
	if err != nil || string(got) != "unchanged" {
		t.Fatalf("external changed: %q %v", got, err)
	}
}

func TestEvidenceLinkConcurrentFirstWriter(t *testing.T) {
	board := evidenceBoard(t)
	raw := evidenceReceipt("event-concurrent", "TASK-1")
	var wg sync.WaitGroup
	results := make(chan EvidenceLinkResult, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), "")
			if err == nil {
				results <- result
			}
		}()
	}
	wg.Wait()
	close(results)
	registered := 0
	for result := range results {
		if result.Registered {
			registered++
		}
	}
	if registered != 1 {
		t.Fatalf("registered=%d want 1", registered)
	}
}
