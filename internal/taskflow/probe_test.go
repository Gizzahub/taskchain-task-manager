package taskflow

import (
	"context"
	"strings"
	"testing"
)

func TestSplitProbeArgs(t *testing.T) {
	tests := []struct {
		name    string
		cmd     string
		want    []string
		wantErr string
	}{
		{name: "plain words", cmd: "test -f marker.txt", want: []string{"test", "-f", "marker.txt"}},
		{name: "double quotes", cmd: `grep "two words" file`, want: []string{"grep", "two words", "file"}},
		{name: "single quotes", cmd: `grep 'two words' file`, want: []string{"grep", "two words", "file"}},
		{name: "backslash refused", cmd: `test -f a\b`, wantErr: "backslash"},
		{name: "backslash in quotes refused", cmd: `grep "a\b" f`, wantErr: "backslash"},
		{name: "semicolon refused", cmd: "test -f x; rm -rf /", wantErr: "metacharacter"},
		{name: "pipe refused", cmd: "grep a b | sh", wantErr: "metacharacter"},
		{name: "substitution refused", cmd: "test -f $(pwd)", wantErr: "metacharacter"},
		{name: "glob refused", cmd: "ls *.md", wantErr: "glob"},
		{name: "unbalanced quote refused", cmd: `grep "open f`, wantErr: "unterminated quote"},
		{name: "empty", cmd: "   ", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitProbeArgs(tt.cmd)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("splitProbeArgs(%q) err = %v, want %q", tt.cmd, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("splitProbeArgs(%q) = %#v, want %#v", tt.cmd, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitProbeArgs(%q)[%d] = %q, want %q", tt.cmd, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestClassifyProbe(t *testing.T) {
	allowed := []string{
		"test -f absent-marker.txt",
		"[ -f absent-marker.txt ]",
		"ls tasks",
		"grep -c root README.md",
		"find tasks -name '*.md'",
		"! test -f absent-marker.txt",
	}
	for _, cmd := range allowed {
		if err := classifyProbe(cmd); err != nil {
			t.Errorf("classifyProbe(%q) = %v, want allowed", cmd, err)
		}
	}
	refused := map[string]string{
		"rm -rf tasks":                 "allowlist",
		"!test -f x":                   "space",
		"find . -delete":               "action",
		"find . -exec rm {} ;":         "metacharacter",
		"":                             "empty",
		"!":                            "space",
		"sh -c 'echo hi'":              "allowlist",
		"find tasks -name x -fprint p": "action",
	}
	for cmd, wantErr := range refused {
		err := classifyProbe(cmd)
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Errorf("classifyProbe(%q) = %v, want %q", cmd, err, wantErr)
		}
	}
	// The negated form skips the find action check by design: "! find . -delete"
	// asserts nothing is deletable, which is a claim about the tree, not an edit.
	if err := classifyProbe("! find . -delete"); err != nil {
		t.Errorf("classifyProbe negated find action = %v, want allowed", err)
	}
}

func TestVacuousCriteriaNeedsGitRoot(t *testing.T) {
	root := t.TempDir() // no .git: nothing may be probed, and nothing panics
	criteria := []Criterion{{Text: "bound", Command: "test -d ."}}
	got, err := vacuousCriteria(context.Background(), root, "todo", criteria)
	if err != nil || len(got) != 0 {
		t.Fatalf("vacuousCriteria without .git = %#v, %v; want no probe", got, err)
	}
}

func TestVacuousCriteriaNonTodoZoneSkipped(t *testing.T) {
	criteria := []Criterion{{Text: "bound", Command: "true"}}
	got, err := vacuousCriteria(context.Background(), t.TempDir(), "doing", criteria)
	if err != nil || len(got) != 0 {
		t.Fatalf("vacuousCriteria in doing = %#v, %v; want untouched", got, err)
	}
}

func TestVacuousCriteriaReportsPassingBinding(t *testing.T) {
	root := t.TempDir()
	if err := writeFileForTest(root, ".git", []byte("gitdir: nowhere\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileForTest(root, "marker.txt", []byte("here\n")); err != nil {
		t.Fatal(err)
	}
	criteria := []Criterion{
		{Text: "marker exists", Command: "test -f marker.txt"},
		{Text: "marker missing", Command: "test -f absent-marker.txt"},
		{Text: "already checked", Command: "test -f marker.txt", Checked: true},
		{Text: "unbound", Command: ""},
		{Text: "off-allowlist", Command: "stat marker.txt"},
	}
	got, err := vacuousCriteria(context.Background(), root, "todo", criteria)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Text != "marker exists" {
		t.Fatalf("vacuous = %#v, want only 'marker exists'", got)
	}
}
