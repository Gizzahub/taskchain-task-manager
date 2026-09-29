package cardid

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHighestFrontmatterID(t *testing.T) {
	cases := []struct {
		name, content string
		want          int
	}{
		{
			name:    "frontmatter id is the claim",
			content: "---\nid: TASK-001\ntitle: x\n---\n\nbody\n",
			want:    1,
		},
		{
			name:    "quoted spelling is the same claim",
			content: "---\nid: 'TASK-012'\n---\n",
			want:    12,
		},
		{
			name:    "commented spelling is the same claim",
			content: "---\nid: TASK-013 # renumbered from 499\n---\n",
			want:    13,
		},
		{
			name:    "a body id line is prose about another card",
			content: "---\nid: TASK-001\n---\n\n```bash\nid: TASK-999\n```\n",
			want:    1,
		},
		{
			name:    "a later frontmatter block is body",
			content: "---\nid: TASK-001\n---\n\n---\nid: TASK-050\n---\n",
			want:    1,
		},
		{
			name:    "other prefixes contribute nothing",
			content: "---\nid: PLAN-090\n---\n",
			want:    0,
		},
		{
			name:    "a sentence starting with id is not a declaration",
			content: "---\nid: TASK-1 and PLAN-2\n---\n",
			want:    0,
		},
		{
			name:    "no frontmatter at all",
			content: "just prose\nid: TASK-999\n",
			want:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HighestFrontmatterID(tc.content, "TASK"); got != tc.want {
				t.Fatalf("HighestFrontmatterID=%d want=%d", got, tc.want)
			}
		})
	}
}

func TestScanFloor(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{
		"tasks/todo/001-a.md",
		"tasks/todo/042-b.md",
		"tasks/done/007-c.md",
		"tasks/todo/notes.txt",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte("---\nid: TASK-001\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 042-b carries the real claim; every file was written with TASK-001, so
	// the scan must find 1, not a number invented from filenames.
	if got, err := ScanFloor(filepath.Join(root, "tasks"), "TASK"); err != nil || got != 1 {
		t.Fatalf("ScanFloor=%d err=%v", got, err)
	}
	if got, err := ScanFloor(filepath.Join(root, "missing"), "TASK"); err != nil || got != 0 {
		t.Fatalf("missing board: ScanFloor=%d err=%v", got, err)
	}
}
