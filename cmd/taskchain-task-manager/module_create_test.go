package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func TestModuleCreateCLI(t *testing.T) {
	board, policy := moduleAdoptionFixture(t)
	var out, diag bytes.Buffer
	if code := runPolicyActivation([]string{"activate-policy", policy, "--dir", board, "--adopt-modules", "--json"}, &out, &diag); code != 0 {
		t.Fatalf("activate: %s", &diag)
	}
	out.Reset()
	diag.Reset()
	args := []string{"create", "--dir", board, "--title", "scoped", "--module", "backend", "--category", "auth/api", "--json"}
	if code := runStore(args, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("create code=%d diag=%s", code, &diag)
	}
	var entry taskstore.Entry
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil || entry.Path != "backend/todo/auth/api/TASK-2.md" {
		t.Fatalf("entry=%+v err=%v", entry, err)
	}
	before := snapshotModuleAdoptionBoard(t, board)
	out.Reset()
	diag.Reset()
	if code := runStore([]string{"create", "--dir", board, "--title", "invalid", "--category", "auth", "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("invalid code=%d out=%s diag=%s", code, &out, &diag)
	}
	if !bytes.Equal(before, snapshotModuleAdoptionBoard(t, board)) {
		t.Fatal("invalid CLI request changed board")
	}
}
