package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
)

// TestPolymorphicEncodingSitesVersionEveryBranch grades the two encoding sites
// that do not carry one document shape but several. Counting sites says 23;
// counting shapes says 28, and the extra five all hide behind these two calls.
// A per-branch check is the only thing that catches a branch whose shape is not
// an object: the chokepoint refuses those rather than wrapping them, so the
// failure is a nonzero exit at runtime and a passing test that happened to
// exercise the other branch.
func TestPolymorphicEncodingSitesVersionEveryBranch(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	cardPath, rulesPath := writeCompletionFixture(t, validCompletionCard("P1", "- [x] done\n"), []byte(completionRules))

	// store.go's single Encode call carries four shapes: an anonymous struct,
	// two sequences behind the entryList envelope, and one Entry.
	// validation.go's carries three: an anonymous struct and two report types.
	branches := []struct {
		name string
		args []string
	}{
		{"store init", []string{"init", "--dir", board, "--json"}},
		{"store list", []string{"list", "--dir", board, "--json"}},
		{"store create", []string{"create", "--dir", board, "--title", "Synthetic task", "--json"}},
		{"store ready", []string{"ready", "--dir", board, "--json"}},
		{"validate bare", []string{"validate", cardPath, "--json"}},
		{"validate rules", []string{"validate", cardPath, "--config", rulesPath, "--json"}},
		{"validate completion", []string{"validate-completion", cardPath, "--config", rulesPath, "--json"}},
	}
	for _, branch := range branches {
		t.Run(branch.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := run(branch.args, &out, &diagnostics); code != 0 {
				t.Fatalf("code=%d diagnostics=%s", code, &diagnostics)
			}
			var top map[string]json.RawMessage
			if err := json.Unmarshal(out.Bytes(), &top); err != nil {
				t.Fatalf("emitted document is not a JSON object: %q err=%v", out.String(), err)
			}
			raw, ok := top["outputVersion"]
			if !ok {
				t.Fatalf("emitted document has no top-level \"outputVersion\" key: %s", out.Bytes())
			}
			var version int
			if err := json.Unmarshal(raw, &version); err != nil {
				t.Fatalf("outputVersion is not a number: %s", raw)
			}
			if version != outputformat.Version {
				t.Errorf("outputVersion = %d, want outputformat.Version = %d", version, outputformat.Version)
			}
			// Regaining this at the top level would re-merge the stdout axis
			// with the on-disk journal axis, which is independently at 1-4.
			if _, ok := top["schemaVersion"]; ok {
				t.Errorf("emitted document carries a top-level \"schemaVersion\" key: %s", out.Bytes())
			}
		})
	}
}
