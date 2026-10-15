package taskstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReaderOnlyFileRolesCannotActivateOrRevise(t *testing.T) {
	raw := []byte("schema-version: 4\nboard-policy:\n  modules: []\n  file-roles:\n    enabled: true\n    projections: []\n    guidance: []\n    templates: []\n")
	for _, resume := range []bool{false, true} {
		for _, shared := range []bool{false, true} {
			dir := t.TempDir()
			sentinel := filepath.Join(dir, "keep.bin")
			if err := os.WriteFile(sentinel, []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, operation := range []func() error{
				func() error {
					_, err := ActivatePolicy(dir, raw, PolicyActivationOptions{Resume: resume, AllWorktrees: shared})
					return err
				},
				func() error {
					_, err := RevisePolicy(dir, raw, PolicyRevisionOptions{ExpectedAuthorityID: strings.Repeat("a", 32), ExpectedDigest: strings.Repeat("b", 64), Resume: resume, AllWorktrees: shared})
					return err
				},
			} {
				if err := operation(); err == nil || !strings.Contains(err.Error(), "reader-only") {
					t.Fatalf("want reader-only refusal, got %v", err)
				}
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if len(entries) != 1 || entries[0].Name() != "keep.bin" {
					t.Fatalf("refusal mutated board: %v", entries)
				}
				content, err := os.ReadFile(sentinel)
				if err != nil || string(content) != "unchanged" {
					t.Fatalf("refusal changed sentinel: %q %v", content, err)
				}
			}
		}
	}
}
