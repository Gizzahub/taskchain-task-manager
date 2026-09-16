package taskstore

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

func TestBundleAdmissionPreparesWithoutPublishing(t *testing.T) {
	board := configuredFixture(t)
	req, _ := bundleFixture(t)
	if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, board)
	s, err := openBundleSession(board)
	if err != nil {
		t.Fatal(err)
	}
	record, prepareErr := s.prepareRecord(parsedBundle(t, req))
	closeErr := s.close()
	if prepareErr != nil || closeErr != nil {
		t.Fatalf("prepare=%v close=%v", prepareErr, closeErr)
	}
	if err := validateBundleRecord(record); err != nil {
		t.Fatal(err)
	}
	if record.Cards[0].ID != "TASK-1" {
		t.Fatal(record.Cards)
	}
	if !reflect.DeepEqual(before, boardBytes(t, board)) {
		t.Fatal("admission published board state")
	}
}

func TestBundleAdmissionFailureLeavesNoUpgradeOrReservation(t *testing.T) {
	for _, scenario := range []string{"missing-intent", "wrong-intent", "missing-task", "occupied-batch", "late-template"} {
		t.Run(scenario, func(t *testing.T) {
			board := configuredFixture(t)
			req, intent := bundleFixture(t)
			if scenario != "missing-intent" {
				if _, err := RegisterContext(board, []byte(testContextIntent)); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "wrong-intent":
				req.Batch.Intent.Digest = strings.Repeat("0", 64)
			case "missing-task":
				req.Tasks[0].DependsOn = []intentdoc.TaskReference{{TaskID: "TASK-99"}}
			case "occupied-batch":
				plan, err := prepareTaskBundle(parsedBundle(t, req), nil, idLedger{SchemaVersion: 2, Reserved: []string{}}, intent)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := Create(board, CreateRequest{Title: "existing"}); err != nil {
					t.Fatal(err)
				}
				raw, err := plan.Batch.Canonical()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := RegisterContext(board, raw); err != nil {
					t.Fatal(err)
				}
			case "late-template":
				req.Tasks = append(req.Tasks, intentdoc.TaskDraft{Key: "second", Title: "Second", DependsOn: []intentdoc.TaskReference{}, Template: &intentdoc.DraftTemplate{ValidationConfig: "bad: config", Type: "feature", Priority: "P1", Summary: "Second", Criteria: []string{"Done"}}})
			}
			before := boardBytes(t, board)
			s, err := openBundleSession(board)
			if err != nil {
				t.Fatal(err)
			}
			record, prepareErr := s.prepareRecord(parsedBundle(t, req))
			closeErr := s.close()
			if prepareErr == nil || closeErr != nil || !reflect.DeepEqual(record, bundleRecord{}) {
				t.Fatalf("record=%+v prepare=%v close=%v", record, prepareErr, closeErr)
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("failed admission changed board")
			}
		})
	}
}
