package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestCLILinkEvidenceJSONOnlyAndNoCardMutation(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.Create(board, taskstore.CreateRequest{ID: "TASK-1", Title: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema_version":1,"contract":"agent-interruption-v1","event":{"id":"cli-event","runtime":"test","hook":"Stop","original_payload_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","payload":{},"received_at":"2026-10-03T10:00:00Z"},"classification":{"event_id":"cli-event","reason":"unknown","certainty":"unknown","provider":"unknown","model":"unknown"}}`)
	file := filepath.Join(t.TempDir(), "receipt.json")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	card := filepath.Join(board, "todo", "TASK-1.md")
	before, err := os.ReadFile(card)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	var out, errOut bytes.Buffer
	if code := run([]string{"link-evidence", file, "--dir", board, "--task-id", "TASK-1", "--sha256", digest, "--json"}, &out, &errOut); code != 0 || errOut.Len() != 0 || !bytes.Contains(out.Bytes(), []byte(`"registered":true`)) {
		t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
	after, err := os.ReadFile(card)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("card changed: %v", err)
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"link-evidence", file, "--dir", board, "--task-id", "TASK-1", "--sha256", string(bytes.Repeat([]byte("0"), 64)), "--json"}, &out, &errOut); code == 0 || out.Len() != 0 || errOut.Len() == 0 {
		t.Fatalf("mismatch code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	errOut.Reset()
	if code := run([]string{"evidence-links", "--dir", board, "--task-id", "TASK-1", "--json"}, &out, &errOut); code != 0 || errOut.Len() != 0 || !bytes.Contains(out.Bytes(), []byte("cli-event")) {
		t.Fatalf("list code=%d out=%s err=%s", code, out.String(), errOut.String())
	}
}
