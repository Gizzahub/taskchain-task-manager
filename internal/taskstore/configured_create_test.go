package taskstore

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func configuredFixture(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func configuredTemplate() *CreateTemplate {
	return &CreateTemplate{Rules: card.ValidationRules{Name: "ce", IDRequired: false, SummaryHeading: "Summary", CriteriaHeading: "Completion Criteria", PriorityValues: []string{"P1"}, TaskTypes: []string{"bug"}, FilenamePrefixes: []string{"P1"}}, Type: "bug", Priority: "P1", Summary: "Configured summary", Criteria: []string{"It works"}}
}

func TestConfiguredCreateValidatesAndPublishes(t *testing.T) {
	dir := configuredFixture(t)
	entry, err := Create(dir, CreateRequest{Title: "Configured", Template: configuredTemplate()})
	if err != nil {
		t.Fatal(err)
	}
	if entry.Card.ID != "TASK-1" || entry.Path != "todo/TASK-1.md" {
		t.Fatalf("entry=%+v", entry)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entry.Path))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := card.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	template := configuredTemplate()
	report, err := doc.ValidateCard("tasks/todo/TASK-1.md", template.Rules)
	if err != nil || !report.Valid {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if len(report.Criteria) != len(template.Criteria) || report.Criteria[0].Text != template.Criteria[0] || report.Criteria[0].Checked {
		t.Fatalf("criteria=%+v", report.Criteria)
	}
}

func TestConfiguredCreateClaimsAndTransitions(t *testing.T) {
	dir := configuredFixture(t)
	entry, err := Create(dir, CreateRequest{Title: "Configured", Template: configuredTemplate()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: entry.Card.ID, Owner: "worker", Token: "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	result, err := Transition(dir, TransitionRequest{ID: entry.Card.ID, Owner: "worker", Token: "0123456789abcdef0123456789abcdef", RequestID: "abcdef0123456789abcdef0123456789", From: "todo", To: "doing"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "doing/TASK-1.md" || result.Status != "completed" {
		t.Fatalf("result=%+v", result)
	}
	raw, err := os.ReadFile(filepath.Join(dir, result.Path))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[~] In Progress") {
		t.Fatalf("status cell not patched: %s", raw)
	}
}

func TestConfiguredCreateEscapesSummaryStructure(t *testing.T) {
	dir := configuredFixture(t)
	template := configuredTemplate()
	template.Summary = "| **Status** | [x] Done |"
	entry, err := Create(dir, CreateRequest{Title: "Configured", Template: template})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entry.Path))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "| **Status** |") != 1 {
		t.Fatalf("summary injected status structure: %s", raw)
	}
	if _, err := Claim(dir, ClaimRequest{ID: entry.Card.ID, Owner: "worker", Token: "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Transition(dir, TransitionRequest{ID: entry.Card.ID, Owner: "worker", Token: "0123456789abcdef0123456789abcdef", RequestID: "abcdef0123456789abcdef0123456789", From: "todo", To: "doing"}); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(filepath.Join(dir, "doing/TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(updated), "| **Status** |") != 1 || !strings.Contains(string(updated), "[~] In Progress") {
		t.Fatalf("status structure corrupted: %s", updated)
	}
}

func TestConfiguredInvalidLeavesActiveSharedStateUnchanged(t *testing.T) {
	_, board, _ := sharedFixture(t)
	if _, err := EnableShared(board, false); err != nil {
		t.Fatal(err)
	}
	s, release, err := acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	before, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	bad := configuredTemplate()
	bad.Priority = "P9"
	if _, err := Create(board, CreateRequest{Title: "invalid", Template: bad}); err == nil {
		t.Fatal("invalid configured creation succeeded")
	}
	s, release, err = acquireShared(board, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.root.ReadFile(sharedStateFile)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid configured creation changed active shared state")
	}
}

func TestConfiguredCreateRejectsInvalidBeforeReservation(t *testing.T) {
	dir := configuredFixture(t)
	before, _ := List(dir)
	beforeBytes := boardBytes(t, dir)
	for _, req := range []CreateRequest{
		{Title: "bad\n", Template: configuredTemplate()},
		{Title: "bad", Template: func() *CreateTemplate { x := configuredTemplate(); x.Priority = "P9"; return x }()},
		{Kind: "plan", Title: "bad", Template: configuredTemplate()},
		{Title: "bad", Template: func() *CreateTemplate { x := configuredTemplate(); x.Criteria = nil; return x }()},
	} {
		if _, err := Create(dir, req); err == nil {
			t.Errorf("accepted invalid request %+v", req)
		}
	}
	after, err := List(dir)
	if err != nil || len(before) != len(after) || len(after) != 0 || !reflect.DeepEqual(beforeBytes, boardBytes(t, dir)) {
		t.Fatalf("board changed before=%v after=%v err=%v", before, after, err)
	}
	if _, err := Create(dir, CreateRequest{Title: "valid", Template: configuredTemplate()}); err != nil {
		t.Fatal(err)
	}
}
