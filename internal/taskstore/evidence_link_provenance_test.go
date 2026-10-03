package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceLinkRejectsUnconfirmedClaims(t *testing.T) {
	for _, reason := range []string{"normal_stop", "unknown", "provider_policy", "approval_denied"} {
		t.Run(reason, func(t *testing.T) {
			raw := strings.Replace(string(evidenceReceipt("proof", "TASK-1")), `"normal_stop"`, `"`+reason+`"`, 1)
			raw = strings.Replace(raw, `"certainty":"suspected"`, `"certainty":"confirmed"`, 1)
			if _, err := LinkEvidence(evidenceBoard(t), "TASK-1", []byte(raw), evidenceDigest([]byte(raw)), ""); err == nil {
				t.Fatal("unsupported confirmed claim accepted")
			}
		})
	}
	raw := strings.Replace(string(evidenceReceipt("proof", "TASK-1")), `"normal_stop"`, `"provider_policy"`, 1)
	raw = strings.Replace(raw, `"certainty":"suspected"`, `"certainty":"confirmed"`, 1)
	raw = strings.Replace(raw, `"payload":{}`, `"payload":{"error":{"code":"cyber_policy"}}`, 1)
	if _, err := LinkEvidence(evidenceBoard(t), "TASK-1", []byte(raw), evidenceDigest([]byte(raw)), ""); err != nil {
		t.Fatal(err)
	}
}

func TestEvidenceLinkRejectsSymlinkReceiptFile(t *testing.T) {
	board := evidenceBoard(t)
	raw := evidenceReceipt("symlink-file", "TASK-1")
	if _, err := LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(board, filepath.FromSlash(evidencePath("TASK-1", "symlink-file")))
	original, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "external.json")
	if err = os.WriteFile(external, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, name); err != nil {
		t.Fatal(err)
	}
	if _, err = EvidenceLinks(board, "TASK-1"); err == nil {
		t.Fatal("symlink accepted by reader")
	}
	if _, err = LinkEvidence(board, "TASK-1", raw, evidenceDigest(raw), ""); err == nil {
		t.Fatal("symlink accepted by replay")
	}
	after, err := os.ReadFile(external)
	if err != nil || string(after) != string(original) {
		t.Fatalf("external changed: %v", err)
	}
}
