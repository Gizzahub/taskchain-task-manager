package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func bundleFixture(t *testing.T) (intentdoc.BundleRequest, intentdoc.Document) {
	t.Helper()
	intent, err := intentdoc.Parse([]byte(testContextIntent))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := intent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	req := intentdoc.BundleRequest{
		SchemaVersion: 1, Kind: "task-bundle", RequestID: strings.Repeat("a", 32),
		Batch: intentdoc.BundleMetadata{ID: "BATCH-abcdef0123456789abcdef0123456789", Revision: 1, Intent: intentdoc.IntentRef{ID: intent.ID(), Revision: intent.Revision(), Digest: digest}, Gap: "Implement evidence", Constraints: []string{}, AuthorizationRefs: []string{}},
		Tasks: []intentdoc.TaskDraft{{Key: "first", ID: "", Title: "First", DependsOn: []intentdoc.TaskReference{}}},
	}
	return req, intent
}

func parsedBundle(t *testing.T, req intentdoc.BundleRequest) intentdoc.BundleDocument {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := intentdoc.ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestPrepareBundleExplicitPrecedenceForwardKeysAndIsolation(t *testing.T) {
	t.Parallel()
	req, intent := bundleFixture(t)
	req.Tasks[0].DependsOn = []intentdoc.TaskReference{{Key: "later"}, {TaskID: "TASK-001"}}
	req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: "later", ID: "TASK-010", Title: "Later", DependsOn: []intentdoc.TaskReference{}})
	entries := []Entry{{Path: "todo/TASK-1.md", Card: card.View{ID: "TASK-1", Title: "Existing", Status: "pending"}}}
	ledger := idLedger{SchemaVersion: 2, Reserved: []string{"TASK-1", "TASK-9"}}
	before, _ := json.Marshal([]any{req, entries, ledger})
	doc := parsedBundle(t, req)
	plan, err := prepareTaskBundle(doc, entries, ledger, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Cards) != 2 || plan.Cards[0].Entry.Card.ID != "TASK-11" || plan.Cards[1].Entry.Card.ID != "TASK-010" {
		t.Fatalf("allocation %+v", plan.Cards)
	}
	if !reflect.DeepEqual(plan.Cards[0].Entry.Card.DependsOn, []string{"TASK-010", "TASK-001"}) {
		t.Fatal(plan.Cards[0].Entry.Card.DependsOn)
	}
	batchRaw, _ := plan.Batch.Canonical()
	var batch intentdoc.Batch
	if err := json.Unmarshal(batchRaw, &batch); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(batch.TaskIDs, []string{"TASK-11", "TASK-010"}) {
		t.Fatalf("batch members %v", batch.TaskIDs)
	}
	again, err := prepareTaskBundle(doc, entries, ledger, intent)
	if err != nil || !reflect.DeepEqual(plan, again) {
		t.Fatalf("not deterministic: %v", err)
	}
	plan.Ledger.Reserved[0] = "TASK-999"
	plan.Cards[0].Raw[0] = '!'
	plan.Cards[0].Entry.Card.DependsOn[0] = "TASK-999"
	after, _ := json.Marshal([]any{req, entries, ledger})
	if !bytes.Equal(before, after) {
		t.Fatal("preparation/result mutation changed input snapshot")
	}
	third, err := prepareTaskBundle(doc, entries, ledger, intent)
	if err != nil || !reflect.DeepEqual(again, third) {
		t.Fatalf("result aliases immutable request: %v", err)
	}
}

func TestPrepareBundleMatchesSingleCreate(t *testing.T) {
	t.Parallel()
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "configured"}[configured], func(t *testing.T) {
			req, intent := bundleFixture(t)
			create := CreateRequest{Kind: "task", Title: "First"}
			if configured {
				config := "schema-version: 1\ncard-dialect:\n  name: ce"
				req.Tasks[0].Template = &intentdoc.DraftTemplate{ValidationConfig: config, Type: "feature", Priority: "P1", Summary: "Implement once", Criteria: []string{"Evidence exists"}}
				rules, err := card.ParseValidationConfig([]byte(config))
				if err != nil {
					t.Fatal(err)
				}
				create.Template = &CreateTemplate{Rules: rules, Type: "feature", Priority: "P1", Summary: "Implement once", Criteria: []string{"Evidence exists"}}
			}
			plan, err := prepareTaskBundle(parsedBundle(t, req), nil, idLedger{SchemaVersion: 2, Reserved: []string{}}, intent)
			if err != nil {
				t.Fatal(err)
			}
			board := configuredFixture(t)
			entry, err := Create(board, create)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(board, entry.Path))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(raw, plan.Cards[0].Raw) || !reflect.DeepEqual(entry, plan.Cards[0].Entry) {
				t.Fatal("single creation differs from bundle preparation")
			}
		})
	}
}

func TestPrepareBundleFailureReturnsNoPartialProposal(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"missing-existing", "new-id-as-existing", "reserved-alias", "overflow", "long-filename", "late-invalid-template", "wrong-intent", "invalid-snapshot"} {
		t.Run(scenario, func(t *testing.T) {
			req, intent := bundleFixture(t)
			ledger := idLedger{SchemaVersion: 2, Reserved: []string{}}
			var entries []Entry
			switch scenario {
			case "missing-existing":
				req.Tasks[0].DependsOn = []intentdoc.TaskReference{{TaskID: "TASK-44"}}
			case "new-id-as-existing":
				req.Tasks[0].DependsOn = []intentdoc.TaskReference{{TaskID: "TASK-44"}}
				req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: "second", ID: "TASK-44", Title: "Second", DependsOn: []intentdoc.TaskReference{}})
			case "reserved-alias":
				ledger.Reserved = []string{"TASK-1"}
				req.Tasks[0].ID = "TASK-001"
			case "overflow":
				ledger.Reserved = []string{"TASK-18446744073709551615"}
			case "long-filename":
				req.Tasks[0].ID = "TASK-" + strings.Repeat("0", 250) + "1"
			case "late-invalid-template":
				req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: "second", Title: "Second", DependsOn: []intentdoc.TaskReference{}, Template: &intentdoc.DraftTemplate{ValidationConfig: "bad: config", Type: "feature", Priority: "P1", Summary: "Second", Criteria: []string{"Done"}}})
			case "wrong-intent":
				req.Batch.Intent.Digest = strings.Repeat("0", 64)
			case "invalid-snapshot":
				entries = []Entry{{Path: "todo/TASK-1.md", Card: card.View{ID: "TASK-1", DependsOn: []string{"TASK-99"}}}}
			}
			before, _ := json.Marshal([]any{entries, ledger})
			plan, err := prepareTaskBundle(parsedBundle(t, req), entries, ledger, intent)
			if err == nil || !reflect.DeepEqual(plan, preparedBundle{}) {
				t.Fatalf("partial result %+v, %v", plan, err)
			}
			after, _ := json.Marshal([]any{entries, ledger})
			if !bytes.Equal(before, after) {
				t.Fatal("failed preparation mutated snapshot")
			}
		})
	}
}
