package taskflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStampFrontmatterFields(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		pairs   [][2]string
		want    string
		wantSet bool
	}{
		{
			name:    "replaces existing in place",
			in:      "---\nid: TASK-1\nstatus: todo\n---\n\nbody\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "---\nid: TASK-1\nstatus: doing\n---\n\nbody\n",
			wantSet: true,
		},
		{
			name:    "inserts before closing fence when absent",
			in:      "---\nid: TASK-1\n---\n\nbody\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "---\nid: TASK-1\nstatus: doing\n---\n\nbody\n",
			wantSet: true,
		},
		{
			name:    "batch keys land inside the block in order",
			in:      "---\nid: TASK-1\n---\n\nbody\n",
			pairs:   [][2]string{{"a", "1"}, {"b", "2"}},
			want:    "---\nid: TASK-1\na: 1\nb: 2\n---\n\nbody\n",
			wantSet: true,
		},
		{
			name:    "byte-equal write is a no-op",
			in:      "---\nstatus: doing\n---\n\nbody\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "---\nstatus: doing\n---\n\nbody\n",
			wantSet: false,
		},
		{
			name:    "no frontmatter fence means no write",
			in:      "just a body\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "just a body\n",
			wantSet: false,
		},
		{
			name:    "unclosed fence means no write",
			in:      "---\nstatus: todo\n\nbody\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "---\nstatus: todo\n\nbody\n",
			wantSet: false,
		},
		{
			name:    "does not rewrite a lookalike key outside frontmatter",
			in:      "---\n---\n\nstatus: todo\n",
			pairs:   [][2]string{{"status", "doing"}},
			want:    "---\nstatus: doing\n---\n\nstatus: todo\n",
			wantSet: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "card.md")
			if err := os.WriteFile(path, []byte(tt.in), 0o644); err != nil {
				t.Fatal(err)
			}
			set, err := stampFrontmatterFields(path, tt.pairs)
			if err != nil {
				t.Fatal(err)
			}
			if set != tt.wantSet {
				t.Fatalf("set = %v, want %v", set, tt.wantSet)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("content = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRewriteStatusCellFirstMatchOnly(t *testing.T) {
	in := "| **Status** | [ ] Pending |\n\n```\n| **Status** | [ ] Pending |\n```\n\n| **Status** | [ ] Pending |\n"
	path := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := rewriteStatusCell(path, StatusInProgress)
	if err != nil {
		t.Fatal(err)
	}
	if !set {
		t.Fatal("status cell not rewritten")
	}
	got, _ := os.ReadFile(path)
	want := "| **Status** | [~] In Progress |\n\n```\n| **Status** | [ ] Pending |\n```\n\n| **Status** | [ ] Pending |\n"
	if string(got) != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestRewriteStatusCellAbsentIsCleanNo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(path, []byte("no table here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	set, err := rewriteStatusCell(path, StatusDone)
	if err != nil || set {
		t.Fatalf("rewriteStatusCell = %v, %v; want false, nil", set, err)
	}
}

func TestRelUnderTasks(t *testing.T) {
	ok := map[string]string{
		"tasks/todo/001-a.md":      "todo/001-a.md",
		"tasks/_archive/done/x.md": "_archive/done/x.md",
		"tasks/a.md":               "a.md",
	}
	for in, want := range ok {
		got, is := relUnderTasks(in)
		if !is || got != want {
			t.Errorf("relUnderTasks(%q) = %q, %v; want %q", in, got, is, want)
		}
	}
	for _, in := range []string{"todo/001-a.md", "../tasks/a.md", "tasks/../etc.md", ".", "..", "tasks"} {
		if got, is := relUnderTasks(in); is {
			t.Errorf("relUnderTasks(%q) = %q, want refusal", in, got)
		}
	}
}

func TestGitMovesFallsBackToRename(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, "todo/a.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	// moveFile relocates a file between directories that already exist; the
	// directory creation is the caller's job.
	if err := os.MkdirAll(filepath.Join(root, "doing"), 0o755); err != nil {
		t.Fatal(err)
	}
	// No git repository here: moveFile must still succeed by rename.
	if err := moveFile(root,
		filepath.Join(root, "todo", "a.md"),
		filepath.Join(root, "doing", "a.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "doing", "a.md")); err != nil {
		t.Fatalf("destination missing after fallback move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "todo", "a.md")); !os.IsNotExist(err) {
		t.Fatal("source still present after move")
	}
}

func TestWriteNewFileRefusesExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "card.md")
	if err := writeNewFile(path, "first\n"); err != nil {
		t.Fatal(err)
	}
	err := writeNewFile(path, "second\n")
	if err == nil || !strings.Contains(err.Error(), "exists") {
		t.Fatalf("second write err = %v, want exists refusal", err)
	}
}
