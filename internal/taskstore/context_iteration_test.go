package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

const maintenanceIntentID = "INTENT-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func maintenanceIntentRaw(t *testing.T) ([]byte, intentdoc.Document) {
	t.Helper()
	raw := []byte(`{"schemaVersion":2,"kind":"intent","id":"` + maintenanceIntentID + `","revision":1,"title":"Maintain","outcome":"Healthy","mode":"maintenance","constraints":[],"nonGoals":[],"successCriteria":[{"key":"checked","text":"The system was checked."}],"maintenance":{"triggers":[{"key":"manual-check","kind":"manual"}],"budget":{"maxIterations":4,"maxTasks":10,"maxElapsedSeconds":600,"noProgressLimit":2}}}`)
	doc, err := intentdoc.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return raw, doc
}

func maintenanceIterationRaw(t *testing.T, intent intentdoc.Document, id, decision string, previous *string, batch *intentdoc.Document) []byte {
	t.Helper()
	digest, err := intent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	ref := map[string]any{"id": intent.ID(), "revision": intent.Revision(), "digest": digest}
	value := map[string]any{
		"schemaVersion": 2, "kind": "iteration", "id": id, "revision": 1,
		"intent": ref, "ordinal": 1,
		"trigger":    map[string]any{"key": "manual-check", "evidenceRefs": []string{"operator-note"}},
		"usage":      map[string]any{"tasks": 0, "elapsedSeconds": 0, "noProgressCount": 0},
		"evaluation": map[string]any{"actor": "operator", "progress": false, "criteria": []any{map[string]any{"key": "checked", "result": "unknown", "evidenceRefs": []string{}}}, "decision": decision, "stopReason": map[string]string{"idle": "none", "continue": "none", "stopped": "user-stop"}[decision], "reason": "observed", "remainingGaps": []string{}, "evidenceRefs": []string{"operator-note"}},
	}
	if decision != "idle" {
		value["usage"].(map[string]any)["noProgressCount"] = 1
	}
	if previous != nil {
		value["previous"] = map[string]any{"id": *previous, "revision": 1, "digest": strings.Repeat("b", 64)}
	}
	if batch != nil {
		digest, _ := batch.Digest()
		value["batch"] = map[string]any{"id": batch.ID(), "revision": batch.Revision(), "digest": digest}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestContextIterationIdlePathReplayAndHistoricalRead(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	rawIntent, intent := maintenanceIntentRaw(t)
	if _, err := RegisterContext(board, rawIntent); err != nil {
		t.Fatal(err)
	}
	id := "ITERATION-11111111111111111111111111111111"
	raw := maintenanceIterationRaw(t, intent, id, "idle", nil, nil)
	registered, err := RegisterContext(board, raw)
	if err != nil || registered.Path != ".task-manager-context/iterations/"+id+"/1.json" || registered.ReferenceValidation != "verified" {
		t.Fatalf("iteration register=%+v err=%v", registered, err)
	}
	replayed, err := RegisterContext(board, raw)
	if err != nil || replayed.Status != "unchanged" || replayed.ReferenceValidation != "not_rechecked" {
		t.Fatalf("iteration replay=%+v err=%v", replayed, err)
	}
	conflict := bytes.Replace(raw, []byte(`"reason":"observed"`), []byte(`"reason":"changed"`), 1)
	beforeConflict := boardBytes(t, board)
	if _, err := RegisterContext(board, conflict); err == nil || !strings.Contains(err.Error(), "different immutable") {
		t.Fatalf("iteration content conflict=%v", err)
	}
	if !reflect.DeepEqual(beforeConflict, boardBytes(t, board)) {
		t.Fatal("iteration content conflict changed board")
	}
	if err := os.Remove(filepath.Join(board, ".task-manager-context", "intents", maintenanceIntentID, "1.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, raw); err != nil {
		t.Fatalf("historical iteration replay rechecked deleted intent: %v", err)
	}
	shown, err := ShowContext(board, "iteration", id, 1)
	if err != nil || shown.ReferenceValidation != "not_rechecked" || !bytes.Equal(shown.Canonical, registered.Canonical) {
		t.Fatalf("historical iteration=%+v err=%v", shown, err)
	}
}

func TestContextIterationReferenceFailuresPreserveBoard(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	rawIntent, intent := maintenanceIntentRaw(t)
	if _, err := RegisterContext(board, rawIntent); err != nil {
		t.Fatal(err)
	}
	assertRejected := func(raw []byte, cause string) {
		t.Helper()
		before := boardBytes(t, board)
		if _, err := RegisterContext(board, raw); err == nil || !strings.Contains(err.Error(), cause) {
			t.Fatalf("expected %q, got %v", cause, err)
		}
		if !reflect.DeepEqual(before, boardBytes(t, board)) {
			t.Fatalf("rejected registration changed board (%s)", cause)
		}
	}
	badTrigger := bytes.Replace(maintenanceIterationRaw(t, intent, "ITERATION-22222222222222222222222222222222", "idle", nil, nil), []byte("manual-check"), []byte("unknown"), 1)
	assertRejected(badTrigger, "trigger is not declared")
	missingPrevious := maintenanceIterationRaw(t, intent, "ITERATION-33333333333333333333333333333333", "continue", func() *string { v := "ITERATION-44444444444444444444444444444444"; return &v }(), nil)
	missingPrevious = bytes.Replace(missingPrevious, []byte(`"ordinal":1`), []byte(`"ordinal":2`), 1)
	assertRejected(missingPrevious, "not registered")
	terminalID := "ITERATION-77777777777777777777777777777777"
	terminalRaw := maintenanceIterationRaw(t, intent, terminalID, "stopped", nil, nil)
	terminalDoc, err := intentdoc.Parse(terminalRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, terminalRaw); err != nil {
		t.Fatal("valid terminal predecessor:", err)
	}
	childID := "ITERATION-88888888888888888888888888888888"
	childRaw := maintenanceIterationRaw(t, intent, childID, "continue", &terminalID, nil)
	terminalDigest, _ := terminalDoc.Digest()
	childRaw = bytes.Replace(childRaw, []byte(strings.Repeat("b", 64)), []byte(terminalDigest), 1)
	childRaw = bytes.Replace(childRaw, []byte(`"ordinal":1`), []byte(`"ordinal":2`), 1)
	childRaw = bytes.Replace(childRaw, []byte(`"noProgressCount":1`), []byte(`"noProgressCount":2`), 1)
	assertRejected(childRaw, "terminal")
	intentDigest, err := intent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	badDigest := bytes.Replace(maintenanceIterationRaw(t, intent, "ITERATION-55555555555555555555555555555555", "idle", nil, nil), []byte(intentDigest), []byte(strings.Repeat("0", 64)), 1)
	assertRejected(badDigest, "kind, identity or digest mismatch")
}

func TestContextMaintenanceBatchRules(t *testing.T) {
	t.Parallel()
	board := filepath.Join(t.TempDir(), "tasks")
	if err := Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(board, CreateRequest{ID: "TASK-1", Title: "maintenance task"}); err != nil {
		t.Fatal(err)
	}
	rawIntent, intent := maintenanceIntentRaw(t)
	if _, err := RegisterContext(board, rawIntent); err != nil {
		t.Fatal(err)
	}
	digest, _ := intent.Digest()
	batch := map[string]any{"schemaVersion": 1, "kind": "batch", "id": "BATCH-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "revision": 1, "intent": map[string]any{"id": intent.ID(), "revision": 1, "digest": digest}, "gap": "check", "taskIds": []string{"TASK-1"}, "constraints": []string{}, "authorizationRefs": []string{}}
	batchRaw, _ := json.Marshal(batch)
	batchDoc, err := intentdoc.Parse(batchRaw)
	if err != nil {
		t.Fatal(err)
	}
	registered, err := RegisterContext(board, batchRaw)
	if err != nil || registered.ReferenceValidation != "verified" {
		t.Fatalf("valid selected batch=%+v err=%v", registered, err)
	}
	iteration := maintenanceIterationRaw(t, intent, "ITERATION-66666666666666666666666666666666", "continue", nil, &batchDoc)
	if _, err := RegisterContext(board, iteration); err != nil {
		t.Fatal("valid selected batch iteration:", err)
	}
	otherIntentRaw := bytes.Replace(rawIntent, []byte(maintenanceIntentID), []byte("INTENT-"+strings.Repeat("d", 32)), 1)
	otherIntent, err := intentdoc.Parse(otherIntentRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, otherIntentRaw); err != nil {
		t.Fatal(err)
	}
	otherDigest, _ := otherIntent.Digest()
	otherBatch := map[string]any{"schemaVersion": 1, "kind": "batch", "id": "BATCH-cccccccccccccccccccccccccccccccc", "revision": 1, "intent": map[string]any{"id": otherIntent.ID(), "revision": 1, "digest": otherDigest}, "gap": "check", "taskIds": []string{"TASK-1"}, "constraints": []string{}, "authorizationRefs": []string{}}
	otherBatchRaw, _ := json.Marshal(otherBatch)
	otherBatchDoc, err := intentdoc.Parse(otherBatchRaw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RegisterContext(board, otherBatchRaw); err != nil {
		t.Fatal(err)
	}
	mislinked := maintenanceIterationRaw(t, intent, "ITERATION-99999999999999999999999999999999", "continue", nil, &otherBatchDoc)
	beforeRejected := boardBytes(t, board)
	if _, err := RegisterContext(board, mislinked); err == nil || !strings.Contains(err.Error(), "same intent revision") {
		t.Fatalf("mislinked batch error=%v", err)
	}
	if !reflect.DeepEqual(beforeRejected, boardBytes(t, board)) {
		t.Fatal("mislinked batch rejection changed board")
	}
	achieved := batch
	achieved["evaluation"] = map[string]any{"actor": "operator", "intent": map[string]any{"id": intent.ID(), "revision": 1, "digest": digest}, "decision": "achieved", "reason": "done", "remainingGaps": []string{}, "evidenceRefs": []string{"evidence"}}
	achieved["revision"] = 2
	achievedRaw, _ := json.Marshal(achieved)
	if _, err := RegisterContext(board, achievedRaw); err == nil || !strings.Contains(err.Error(), "achieved") {
		t.Fatalf("maintenance achieved batch accepted: %v", err)
	}
	if !reflect.DeepEqual(beforeRejected, boardBytes(t, board)) {
		t.Fatal("maintenance achieved rejection changed board")
	}
}
