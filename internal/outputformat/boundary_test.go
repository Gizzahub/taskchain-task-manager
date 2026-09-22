package outputformat_test

import (
	"encoding/json"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

// TestOutputDocumentsSpellTheVersionKeyOutputVersion asserts on the encoded
// bytes rather than on the Go field, because the byte stream is what a
// consumer actually contracts with: a field could be renamed back while the
// json tag kept the old spelling, or the reverse, and only the bytes catch
// both. Regaining "schemaVersion" here would re-merge the output axis with
// the on-disk journal axis, which is independently at 1-4.
func TestOutputDocumentsSpellTheVersionKeyOutputVersion(t *testing.T) {
	documents := map[string]any{
		"ArchiveResult":          taskstore.ArchiveResult{},
		"RelocationResult":       taskstore.RelocationResult{},
		"RepairResult":           taskstore.RepairResult{},
		"ArchiveCapacityResult":  taskstore.ArchiveCapacityResult{},
		"ContextResult":          taskstore.ContextResult{},
		"BundleResult":           taskstore.BundleResult{},
		"PolicyActivationResult": taskstore.PolicyActivationResult{},
		"WorktreeReport":         githistory.WorktreeReport{},
		"CompletionReport":       card.CompletionReport{},
		"ValidationReport":       card.ValidationReport{},
	}
	for name, document := range documents {
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &top); err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if _, ok := top["outputVersion"]; !ok {
			t.Errorf("%s output document has no top-level \"outputVersion\" key: %s", name, encoded)
		}
		// This test marshals zero values, so it grades the json tag, not the
		// number. A construction site that hardcodes 1, or borrows a journal
		// schema constant, keeps the key and still publishes the wrong axis'
		// value; only the per-command tests that build real documents catch
		// that, and the two anonymous structs in cmd/ are covered there alone.
		if _, ok := top["schemaVersion"]; ok {
			t.Errorf("%s output document regained the top-level \"schemaVersion\" key, which names the on-disk journal axis: %s", name, encoded)
		}
	}
}
