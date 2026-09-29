package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	got := slugify("Fixture scaffold card")
	if got != "fixture-scaffold-card" {
		t.Fatalf("slugify = %q", got)
	}
	if got := slugify("  --Already--kebab--  "); got != "already-kebab" {
		t.Fatalf("slugify = %q", got)
	}
}

func TestHasNonASCIITitleWord(t *testing.T) {
	if hasNonASCIITitleWord("plain title") {
		t.Fatal("ASCII title flagged")
	}
	if !hasNonASCIITitleWord("완료 조건") {
		t.Fatal("Korean title not flagged")
	}
}

func TestCreateRefusesBeforeWriting(t *testing.T) {
	root := t.TempDir() // no tasks/ tree: nothing may be written on refusal
	cases := []struct {
		name    string
		opts    NewCardOptions
		wantErr string
	}{
		{"no title", NewCardOptions{Kind: "task"}, "--title is required"},
		{"unknown kind", NewCardOptions{Kind: "epic", Title: "x"}, "unknown kind"},
		{"zone as kind", NewCardOptions{Kind: "doing", Title: "x"}, "is a zone name"},
		{"storage as kind", NewCardOptions{Kind: "_archive", Title: "x"}, "long-term storage"},
		{"bad explicit id", NewCardOptions{Kind: "task", Title: "x", ID: "ISSUE-9"}, "must look like"},
		{"digits-only slug", NewCardOptions{Kind: "task", Title: "123", Slug: "123"}, "carries no ASCII letter"},
		{"bad slug shape", NewCardOptions{Kind: "task", Title: "x", Slug: "Kebab"}, "not lowercase-kebab"},
		{"non-ASCII title", NewCardOptions{Kind: "task", Title: "완료"}, "needs --slug"},
		{"bad type", NewCardOptions{Kind: "task", Title: "x", Type: "epic"}, "not valid for a task"},
		{"bad effort", NewCardOptions{Kind: "task", Title: "x", Effort: "XXL"}, "--effort"},
		{"bad exec tier", NewCardOptions{Kind: "task", Title: "x", ExecTier: "fast"}, "--exec-tier"},
		{"unbound criterion", NewCardOptions{Kind: "task", Title: "x", Criteria: []string{"just text"}}, "| verify"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Create(context.Background(), tt.opts)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Create err = %v, want %q", err, tt.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, "tasks")); !os.IsNotExist(statErr) {
				t.Fatal("refusal created tasks/ tree")
			}
		})
	}
}

func TestCreateWritesScaffoldAndReservesID(t *testing.T) {
	root := t.TempDir() // no repository: the creator falls back to its own tree scan
	result, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "Fixture scaffold card", Created: "2026-09-29",
		Criteria: []string{"bound | verify: `test -f absent-new-marker.txt`"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "tasks/todo/001-fixture-scaffold-card.md" {
		t.Fatalf("path = %q", result.Path)
	}
	if result.ID != "TASK-001" {
		t.Fatalf("id = %q, want TASK-001 (three digits)", result.ID)
	}
	content, err := os.ReadFile(filepath.Join(root, result.Path))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"id: TASK-001\n", "title: \"Fixture scaffold card\"\n", "type: feature\n",
		"priority: P2\n", "created: 2026-09-29\n", "- [ ] bound | verify: `test -f absent-new-marker.txt`\n",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("scaffold missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(string(content), "<observable condition>") {
		t.Fatal("explicit criteria must suppress the placeholder")
	}
	if strings.Contains(string(content), "status:") {
		t.Fatal("fresh task card carries no status line; the zone is the state")
	}
}

func TestCreateReservesIDThroughLedgerInRepository(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "tasks/todo/004-existing.md",
		[]byte("---\nid: TASK-004\n---\n\nolder card\n")); err != nil {
		t.Fatal(err)
	}
	gitInitForTest(t, root)
	result, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "next card", Created: "2026-09-29",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "TASK-005" {
		t.Fatalf("id = %q, want TASK-005 (tree floor 4, ledger empty)", result.ID)
	}
	ledger, err := os.ReadFile(filepath.Join(root, ".git", "ce", "card-id-reservations", "highest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ledger), "\"TASK\": 5") {
		t.Fatalf("ledger = %s", ledger)
	}
}

func TestCreatePlaceholderBodyWithoutCriteria(t *testing.T) {
	root := t.TempDir()
	result, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "task", Title: "bare", Created: "2026-09-29",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "<observable condition>") {
		t.Fatal("no criteria: placeholder must be present")
	}
	if !strings.Contains(result.Content, "| verify:") {
		t.Fatal("placeholder must explain the binding requirement")
	}
}

func TestResolveFieldsDefaults(t *testing.T) {
	f, err := resolveFields("task", NewCardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != "feature" || f.Priority != "P2" {
		t.Fatalf("task defaults = %#v, want feature/P2", f)
	}
	if f, err = resolveFields("issue", NewCardOptions{}); err != nil || f.Priority != "P1" {
		t.Fatalf("issue default priority = %#v, %v; want P1", f, err)
	}
	if f, err = resolveFields("plan", NewCardOptions{}); err != nil || f.Priority != "" {
		t.Fatalf("plan priority = %#v, %v; want none", f, err)
	}
}
