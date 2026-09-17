package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepairRejectsExplicitEmptyPolicyBinding(t *testing.T) {
	board, req, _, _ := statusRepairFixture(t)
	if _, err := RepairStatus(board, req, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRepairJournal(raw); err != nil {
		t.Fatal(err)
	}
	var j map[string]json.RawMessage
	if err := json.Unmarshal(raw, &j); err != nil {
		t.Fatal(err)
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(j["records"], &records); err != nil {
		t.Fatal(err)
	}
	records[0]["policyCanonical"] = json.RawMessage(`""`)
	j["records"], err = json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeRepairJournal(raw); err == nil || !strings.Contains(err.Error(), "nonempty base64") {
		t.Fatalf("empty binding accepted or wrong error: %v", err)
	}
}

func moduleRepairFixture(t *testing.T) (string, RepairRequest) {
	t.Helper()
	board := moduleAdoptionBoard(t)
	if _, err := ActivatePolicy(board, moduleAdoptionRaw(t), PolicyActivationOptions{AdoptModules: true}); err != nil {
		t.Fatal(err)
	}
	entry, err := Create(board, CreateRequest{Title: "repair module", Module: "backend", Category: "auth/api"})
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(board, entry.Path)
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("\n| **Status** | [x] Done |\n")...)
	if err := os.WriteFile(name, raw, 0o640); err != nil {
		t.Fatal(err)
	}
	return board, RepairRequest{ID: entry.Card.ID, Owner: "tester", RequestID: strings.Repeat("c", 32), Path: entry.Path, ExpectedSHA256: bytesDigest(raw)}
}

func TestModuleRepairRecoveryAndHistoricalReplay(t *testing.T) {
	for _, phase := range []string{"after-journal", "after-stage", "after-replacement", "after-receipt"} {
		t.Run(phase, func(t *testing.T) {
			board, req := moduleRepairFixture(t)
			if _, err := repairStatusWithStep(board, req, true, false, repairStopAt(phase)); err == nil || !strings.Contains(err.Error(), "stop at "+phase) {
				t.Fatalf("requested interruption not observed: %v", err)
			}
			result, err := RecoverStatusRepair(board, req)
			if err != nil || !result.Changed || result.Status != "completed" {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			raw, err := os.ReadFile(filepath.Join(board, req.Path))
			if err != nil || bytes.Contains(raw, []byte("[x] Done")) {
				t.Fatalf("status not repaired: %s %v", raw, err)
			}
			if err := os.Remove(filepath.Join(board, req.Path)); err != nil {
				t.Fatal(err)
			}
			before := boardBytes(t, board)
			again, err := RecoverStatusRepair(board, req)
			if err != nil || again != result || !reflectEqualBoard(before, boardBytes(t, board)) {
				t.Fatalf("historical replay changed board: %+v %v", again, err)
			}
		})
	}
}

func TestModuleRepairJournalRequiresExactHistoricalPolicy(t *testing.T) {
	board, req := moduleRepairFixture(t)
	if _, err := RepairStatus(board, req, true); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(board, repairsFile))
	if err != nil {
		t.Fatal(err)
	}
	j, err := decodeRepairJournal(raw)
	if err != nil || len(j.Records) != 1 || len(j.Records[0].PolicyCanonical) == 0 {
		t.Fatalf("missing policy binding: %+v %v", j, err)
	}
	for _, kind := range []string{"missing", "noncanonical", "digest", "status", "kind-zone"} {
		t.Run(kind, func(t *testing.T) {
			rec := j.Records[0]
			switch kind {
			case "missing":
				rec.PolicyCanonical = nil
			case "noncanonical":
				rec.PolicyCanonical = append(append([]byte(nil), rec.PolicyCanonical...), '\n')
			case "digest":
				rec.PolicyDigest = strings.Repeat("0", 64)
			case "status":
				rec.CanonicalStatus = "done"
			case "kind-zone":
				rec.Path = strings.Replace(rec.Path, "/todo/", "/plan/", 1)
			}
			if err := validateRepairRecord(rec); err == nil {
				t.Fatal("invalid historical binding accepted")
			}
		})
	}
}
