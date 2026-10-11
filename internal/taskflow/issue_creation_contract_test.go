package taskflow

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssueCreationRejectsBeforeFileOrReservation(t *testing.T) {
	bound := "bound | verify: `test -f absent-new-marker.txt`"
	cases := []struct {
		name    string
		opts    NewCardOptions
		wantErr string
	}{
		{
			name:    "plan criteria",
			opts:    NewCardOptions{Kind: "plan", Title: "plan work", Criteria: []string{bound}},
			wantErr: "new card: invalid input: --criterion applies to task and issue cards; a plan card has no Completion Criteria section",
		},
		{
			name:    "backlog criteria",
			opts:    NewCardOptions{Kind: "backlog", Title: "idea", Criteria: []string{bound}},
			wantErr: "new card: invalid input: --criterion applies to task and issue cards; a backlog card has no Completion Criteria section",
		},
		{
			name:    "plan criteria with line break",
			opts:    NewCardOptions{Kind: "plan", Title: "plan work", Criteria: []string{bound + "\nextra"}},
			wantErr: "new card: invalid input: --criterion applies to task and issue cards; a plan card has no Completion Criteria section",
		},
		{
			name: "criterion line break",
			opts: NewCardOptions{Kind: "issue", Title: "broken", Criteria: []string{bound + "\npriority: P0"}},
			wantErr: fmt.Sprintf("new card: invalid input: criterion %q spans several lines; pass each criterion as its own --criterion",
				bound+"\npriority: P0"),
		},
		{
			name: "criterion carriage return",
			opts: NewCardOptions{Kind: "task", Title: "broken", Criteria: []string{"ok\r" + bound}},
			wantErr: fmt.Sprintf("new card: invalid input: criterion %q spans several lines; pass each criterion as its own --criterion",
				"ok\r"+bound),
		},
		{
			name:    "discovered-in injection",
			opts:    NewCardOptions{Kind: "issue", Title: "titled", DiscoveredIn: "file.go\nseverity: critical"},
			wantErr: "new card: invalid input: --discovered-in must be a single-line reference",
		},
		{
			name:    "discovered-in carriage return",
			opts:    NewCardOptions{Kind: "issue", Title: "titled", DiscoveredIn: "file.go\rseverity: critical"},
			wantErr: "new card: invalid input: --discovered-in must be a single-line reference",
		},
		{
			name:    "task discovered-in",
			opts:    NewCardOptions{Kind: "task", Title: "titled", DiscoveredIn: "file.go:1"},
			wantErr: "new card: invalid input: --discovered-in is only valid for an issue",
		},
		{
			name:    "plan discovered-in",
			opts:    NewCardOptions{Kind: "plan", Title: "titled", DiscoveredIn: "file.go:1"},
			wantErr: "new card: invalid input: --discovered-in is only valid for an issue",
		},
		{
			name:    "backlog discovered-in",
			opts:    NewCardOptions{Kind: "backlog", Title: "titled", DiscoveredIn: "file.go:1"},
			wantErr: "new card: invalid input: --discovered-in is only valid for an issue",
		},
		{
			name:    "task explicit empty discovered-in",
			opts:    NewCardOptions{Kind: "task", Title: "titled", DiscoveredInSet: true},
			wantErr: "new card: invalid input: --discovered-in is only valid for an issue",
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			gitInitForTest(t, root)
			tt.opts.Root = root
			tt.opts.Created = "2026-10-12"
			_, err := Create(context.Background(), tt.opts)
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("Create err = %v, want %q", err, tt.wantErr)
			}
			if _, statErr := os.Stat(filepath.Join(root, "tasks")); !os.IsNotExist(statErr) {
				t.Fatal("rejection wrote a card file")
			}
			ledger := filepath.Join(root, ".git", "ce", "card-id-reservations")
			if _, statErr := os.Stat(ledger); !os.IsNotExist(statErr) {
				t.Fatal("rejection reserved an id")
			}
		})
	}
}

func TestIssueCriteriaAreOptional(t *testing.T) {
	root := t.TempDir()
	bare, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "no criteria", Created: "2026-10-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bare.Content, "## Completion Criteria") {
		t.Fatalf("issue without criteria rendered a criteria heading:\n%s", bare.Content)
	}
	criterion := "seen | verify: `test -f absent-new-marker.txt`"
	with, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "with criteria", Created: "2026-10-12",
		Criteria: []string{criterion},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with.Content, "## Completion Criteria\n\n- [ ] "+criterion+"\n") {
		t.Fatalf("issue criteria missing:\n%s", with.Content)
	}
}

func TestIssueDiscoveredInRoundTrip(t *testing.T) {
	root := t.TempDir()
	title := "Visible title"
	ref := "internal/taskflow/newcard.go:340"
	result, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: title, Created: "2026-10-12", DiscoveredIn: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "title: \"Visible title\"\n") {
		t.Fatalf("title missing:\n%s", result.Content)
	}
	if !strings.Contains(result.Content, "discovered-in: \"internal/taskflow/newcard.go:340\"\n") {
		t.Fatalf("discovered-in = missing ref:\n%s", result.Content)
	}
	if strings.Contains(result.Content, "discovered-in: \"Visible title\"") {
		t.Fatal("discovered-in used the title")
	}

	omitted, err := Create(context.Background(), NewCardOptions{
		Root: root, Kind: "issue", Title: "another", Created: "2026-10-12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(omitted.Content, "discovered-in: \"\"\n") {
		t.Fatalf("omitted discovered-in placeholder missing:\n%s", omitted.Content)
	}
}
