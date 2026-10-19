package candidatediagnostic

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestCandidateDiagnosticRelativeInputsUseCallingDirectory(t *testing.T) {
	root, in := diagnosticRepo(t)
	t.Chdir(filepath.Dir(root))
	in.Repo = filepath.Base(root)
	in.CandidatePolicy = filepath.Join(filepath.Base(root), "candidate.json")
	r := Assess(context.Background(), in)
	if r.ExitCode() != 0 || r.Candidate == nil || r.Snapshot == nil {
		t.Fatalf("relative inputs did not resolve from calling directory: %+v", r)
	}
}

func TestCandidateDiagnosticBoardPathByteAndEncodingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		valid      bool
	}{
		{"exact byte limit", strings.Repeat("a", 64<<10), true},
		{"over byte limit", strings.Repeat("a", (64<<10)+1), false},
		{"unicode byte limit", strings.Repeat("가", (64<<10)/3) + "a", true},
		{"unicode over byte limit", strings.Repeat("가", (64<<10)/3) + "aa", false},
		{"invalid UTF8", "tasks/\xff", false},
		{"NUL", "tasks/\x00", false},
		{"newline", "tasks/\n", false},
		{"unicode control", "tasks/\u0085", false},
		{"Git metadata", "tasks/.GiT/board", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := Input{Repo: "repo", CandidatePolicy: "candidate", Board: tc.path, Source: "index"}
			if err := ValidateInput(in); (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func TestCandidateDiagnosticFindingsHaveSortedExactContract(t *testing.T) {
	root, in := diagnosticRepo(t)
	for _, rel := range []string{"tasks/한글.md", "tasks/Z.md", "tasks/INDEX.md", "tasks/a.json"} {
		diagnosticWrite(t, root, rel, []byte("synthetic invalid entry"))
	}
	diagnosticGit(t, root, "add", "tasks")
	in.Source = "worktree"
	diagnosticWrite(t, root, "tasks/todo/README.md", []byte("residue"))
	r := Assess(context.Background(), in)
	if r.ExitCode() != 1 || len(r.Findings) < 5 {
		t.Fatalf("expected multiple tracked and residue findings: %+v", r)
	}
	for i, f := range r.Findings {
		if f.Contract != "docs/file-roles.md#cards-only-snapshot-foundation" || f.Action == "" {
			t.Fatalf("finding lacks exact contract/action: %+v", f)
		}
		if i > 0 {
			previous := r.Findings[i-1]
			if previous.Path > f.Path || (previous.Path == f.Path && previous.Detail > f.Detail) {
				t.Fatalf("findings not sorted by UTF8 bytes (path,detail): %+v", r.Findings)
			}
		}
	}
}
