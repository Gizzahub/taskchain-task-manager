package taskstore

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func TestPrepareBundleAggregateExpansionBound(t *testing.T) {
	t.Parallel()
	req, intent := bundleFixture(t)
	req.Tasks = nil
	refs := []intentdoc.TaskReference{}
	for i := 1; i <= 64; i++ {
		key := fmt.Sprintf("base-%d", i)
		id := "TASK-" + strings.Repeat("0", 245-len(fmt.Sprint(i))) + fmt.Sprint(i)
		req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: key, ID: id, Title: "Base", DependsOn: []intentdoc.TaskReference{}})
		refs = append(refs, intentdoc.TaskReference{Key: key})
	}
	for i := 1; i <= 64; i++ {
		req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: fmt.Sprintf("consumer-%d", i), Title: "Consumer", DependsOn: append([]intentdoc.TaskReference(nil), refs...)})
	}
	// Short key references expand into long, individually valid file IDs.
	doc := parsedBundle(t, req)
	plan, err := prepareTaskBundle(doc, nil, idLedger{SchemaVersion: 2, Reserved: []string{}}, intent)
	if err == nil || !strings.Contains(err.Error(), "prepared bundle exceeds") || !reflect.DeepEqual(plan, preparedBundle{}) {
		t.Fatalf("aggregate expansion yielded %+v, %v", plan, err)
	}
}

func TestCreateFilenameLimitBeforeReservation(t *testing.T) {
	t.Parallel()
	id := "TASK-" + strings.Repeat("0", 246) + "1"
	if len(id+".md") != 255 {
		t.Fatal("invalid boundary fixture")
	}
	if _, err := prepareCreatedCard(id, CreateRequest{ID: id, Title: "Boundary"}); err != nil {
		t.Fatal(err)
	}
	board := configuredFixture(t)
	before := boardBytes(t, board)
	_, err := Create(board, CreateRequest{ID: "TASK-0" + id[len("TASK-"):], Title: "Overlong"})
	if err == nil || !strings.Contains(err.Error(), "filename exceeds") {
		t.Fatalf("filename error = %v", err)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("overlong filename reserved an ID or staged a file")
	}
}

func TestPrepareBundleRejectsInvalidLedgerAndZeroRequest(t *testing.T) {
	t.Parallel()
	req, intent := bundleFixture(t)
	doc := parsedBundle(t, req)
	for _, ledger := range []idLedger{
		{}, {SchemaVersion: 4, Reserved: []string{}},
		{SchemaVersion: 2, Reserved: []string{"TASK-01"}},
		{SchemaVersion: 2, Reserved: []string{"TASK-1", "TASK-1"}},
		{SchemaVersion: 2, Reserved: []string{}, Namespace: strings.Repeat("1", 32)},
		{SchemaVersion: 3, Reserved: []string{}},
	} {
		if _, err := prepareTaskBundle(doc, nil, ledger, intent); err == nil {
			t.Fatalf("invalid ledger accepted: %+v", ledger)
		}
	}
	if _, err := prepareTaskBundle(intentdoc.BundleDocument{}, nil, idLedger{SchemaVersion: 2, Reserved: []string{}}, intent); err == nil {
		t.Fatal("zero request accepted")
	}
}

func TestPrepareBundleExplicitHolePreservesSharedBinding(t *testing.T) {
	t.Parallel()
	req, intent := bundleFixture(t)
	req.Tasks[0].ID = "TASK-002"
	ledger := idLedger{SchemaVersion: 3, Namespace: strings.Repeat("1", 32), Reserved: []string{"TASK-100"}}
	plan, err := prepareTaskBundle(parsedBundle(t, req), nil, ledger, intent)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cards[0].Entry.Card.ID != "TASK-002" || plan.Ledger.SchemaVersion != 3 || plan.Ledger.Namespace != ledger.Namespace || !reflect.DeepEqual(plan.Ledger.Reserved, []string{"TASK-100", "TASK-2"}) {
		t.Fatalf("explicit hole or binding changed: %+v", plan)
	}
}
