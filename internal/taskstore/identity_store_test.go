package taskstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestKindAllocationPreservesExplicitSpelling(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		req      CreateRequest
		id, path string
	}{
		{CreateRequest{ID: "TASK-009", Title: "padded"}, "TASK-009", "todo/TASK-009.md"},
		{CreateRequest{Kind: "plan", Title: "plan"}, "PLAN-1", "plan/PLAN-1.md"},
		{CreateRequest{ID: "ISSUE-007", Title: "issue"}, "ISSUE-007", "issue/ISSUE-007.md"},
		{CreateRequest{Kind: "issue", Title: "next issue"}, "ISSUE-8", "issue/ISSUE-8.md"},
		{CreateRequest{Kind: "backlog", Title: "backlog"}, "BACKLOG-1", "backlog/BACKLOG-1.md"},
		{CreateRequest{Title: "next task"}, "TASK-10", "todo/TASK-10.md"},
	} {
		e, err := Create(dir, tc.req)
		if err != nil || e.Card.ID != tc.id || e.Path != tc.path {
			t.Fatalf("%+v: %v %v", tc.req, e, err)
		}
	}
	before := boardBytes(t, dir)
	for _, req := range []CreateRequest{{ID: "TASK-9", Title: "duplicate alias"}, {Kind: "task", ID: "PLAN-8", Title: "mismatch"}, {Kind: "unknown", Title: "unknown"}} {
		if _, err := Create(dir, req); err == nil {
			t.Fatalf("invalid create accepted: %+v", req)
		}
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("invalid create changed board")
	}
	ready, err := Ready(dir)
	if err != nil || len(ready) != 2 {
		t.Fatalf("kind cards offered as execution tasks: %v %v", ready, err)
	}
}

func TestNumericDuplicateCardsAndDependencies(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate": "---\nid: TASK-01\n---\n",
		"self":      "---\nid: TASK-2\ndepends-on: [TASK-002]\n---\n",
		"repeated":  "---\nid: TASK-2\ndepends-on: [TASK-1, TASK-001]\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tasks")
			if err := Init(dir); err != nil {
				t.Fatal(err)
			}
			if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "one"}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "todo/manual.md"), []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, dir)
			if _, err := Ready(dir); err == nil {
				t.Fatal("numeric conflict accepted")
			}
			if _, err := Create(dir, CreateRequest{Title: "no"}); err == nil {
				t.Fatal("numeric conflict allowed create")
			}
			if !reflect.DeepEqual(before, boardBytes(t, dir)) {
				t.Fatal("rejection modified board")
			}
		})
	}
}

func TestV1UpgradeAndZeroReservation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	legacy := []byte("{\"schemaVersion\":1,\"reserved\":[\"TASK-9\"]}\n")
	if err := os.WriteFile(filepath.Join(dir, idsFile), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{ID: "TASK-009", Title: "duplicate"}); err == nil {
		t.Fatal("legacy reservation alias reused")
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("failed create upgraded ledger")
	}
	result, err := ReserveIDs(dir, []string{"PLAN-000", "TASK-0009"}, false)
	if err != nil || result.ReservedCount != 2 || result.MaxID != "TASK-9" || result.MaxIDs["PLAN"] != "PLAN-0" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, idsFile))
	if err != nil {
		t.Fatal(err)
	}
	var ledger idLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		t.Fatal(err)
	}
	if ledger.SchemaVersion != 2 || !reflect.DeepEqual(ledger.Reserved, []string{"PLAN-0", "TASK-9"}) {
		t.Fatalf("ledger=%v", ledger)
	}
	e, err := Create(dir, CreateRequest{Kind: "plan", Title: "after zero"})
	if err != nil || e.Card.ID != "PLAN-1" {
		t.Fatalf("zero floor: %v %v", e, err)
	}
}

func TestKindOverflowDoesNotExhaustOtherPrefixes(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ReserveIDs(dir, []string{"PLAN-18446744073709551615"}, false); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Create(dir, CreateRequest{Kind: "plan", Title: "overflow"}); err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("overflow=%v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, dir)) {
		t.Fatal("overflow changed board")
	}
	e, err := Create(dir, CreateRequest{Title: "task unaffected"})
	if err != nil || e.Card.ID != "TASK-1" {
		t.Fatalf("cross prefix floor: %v %v", e, err)
	}
}
