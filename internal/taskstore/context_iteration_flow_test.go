package taskstore

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func TestContextIterationHealthyChildAndFork(t *testing.T) {
	board := configuredFixture(t)
	raw, intent := maintenanceIntentRaw(t)
	if _, err := RegisterContext(board, raw); err != nil {
		t.Fatal(err)
	}
	parentRaw := maintenanceIterationRaw(t, intent, "ITERATION-"+strings.Repeat("a", 32), "continue", nil, nil)
	parent, err := RegisterContext(board, parentRaw)
	if err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"b", "c"} {
		var child intentdoc.Iteration
		if err := json.Unmarshal(maintenanceIterationRaw(t, intent, "ITERATION-"+strings.Repeat(suffix, 32), "idle", nil, nil), &child); err != nil {
			t.Fatal(err)
		}
		child.Previous = &intentdoc.ContextRef{ID: parent.ID, Revision: 1, Digest: parent.Digest}
		child.Ordinal, child.Usage.NoProgressCount = 2, 1
		child.Evaluation.Criteria[0].Result = "met"
		child.Evaluation.Criteria[0].EvidenceRefs = []string{"healthy-check"}
		raw, err := json.Marshal(child)
		if err != nil {
			t.Fatal(err)
		}
		result, err := RegisterContext(board, raw)
		if err != nil || result.ReferenceValidation != "verified" {
			t.Fatalf("child/fork=%+v %v", result, err)
		}
		var stored intentdoc.Iteration
		if err := json.Unmarshal(result.Canonical, &stored); err != nil {
			t.Fatal(err)
		}
		if stored.Usage.NoProgressCount != 1 || stored.Batch != nil {
			t.Fatal("idle changed previous no-progress count or invented work")
		}
	}
	entries, err := List(board)
	if err != nil || len(entries) != 0 {
		t.Fatalf("idle/fork created tasks: %v %v", entries, err)
	}
}

func TestContextMaintenanceBundlePublication(t *testing.T) {
	board := configuredFixture(t)
	rawIntent, intent := maintenanceIntentRaw(t)
	if _, err := RegisterContext(board, rawIntent); err != nil {
		t.Fatal(err)
	}
	req, _ := bundleFixture(t)
	digest, _ := intent.Digest()
	req.Batch.Intent = intentdoc.IntentRef{ID: intent.ID(), Revision: 1, Digest: digest}
	raw, err := parsedBundle(t, req).Canonical()
	if err != nil {
		t.Fatal(err)
	}
	published, err := PublishBundle(board, raw, BundleOptions{Adopt: true})
	if err != nil || published.Status != "completed" || len(published.Tasks) != 1 {
		t.Fatalf("maintenance bundle=%+v %v", published, err)
	}
	batch, err := intentdoc.Parse(published.Batch)
	if err != nil {
		t.Fatal(err)
	}
	iterationRaw := maintenanceIterationRaw(t, intent, "ITERATION-"+strings.Repeat("e", 32), "continue", nil, &batch)
	if result, err := RegisterContext(board, iterationRaw); err != nil || result.ReferenceValidation != "verified" {
		t.Fatalf("batch observation=%+v %v", result, err)
	}
	if replay, err := PublishBundle(board, raw, BundleOptions{}); err != nil || !replay.Replayed {
		t.Fatalf("bundle replay=%+v %v", replay, err)
	}
}
