package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `show` reads one file with no board policy, so it agrees with `list` only
// where the zone is a reserved name. This pins the half it can decide: below a
// tasks/ boundary a workflow zone sets the status, and archive or a kind
// directory leaves frontmatter alone. Without that boundary no directory is
// metadata at all.
func TestShowResolvesStatusFromTheZoneNotTheCategory(t *testing.T) {
	root := t.TempDir()
	write := func(rel, status string) string {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nid: TASK-1\ntitle: t\nstatus: " + status + "\n---\n\n# t\n"
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	for _, tc := range []struct {
		rel  string
		want string
	}{
		{"tasks/done/TASK-1.md", "done"},
		{"tasks/archive/done/TASK-1.md", "custom"},
		{"tasks/plan/done/TASK-1.md", "custom"},
		{"tasks/issue/blocked/TASK-1.md", "custom"},
		{"outside/done/TASK-1.md", "custom"},
	} {
		code, out, diag := runCLI(t, "show", write(tc.rel, "custom"), "--json")
		if code != 0 || diag != "" {
			t.Fatalf("%s: code=%d diag=%q", tc.rel, code, diag)
		}
		if want := `"status":"` + tc.want + `"`; !strings.Contains(out, want) {
			t.Fatalf("%s: want %s, got %s", tc.rel, want, strings.TrimSpace(out))
		}
	}
}
