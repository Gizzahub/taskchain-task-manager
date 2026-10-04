package taskstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

func TestDiskArchiveOperationLiteralsStayFixed(t *testing.T) {
	t.Parallel()
	if diskArchiveOperationArchive != "archive" || diskArchiveOperationSupersede != "supersede" || diskArchiveOperationForce != "force" || diskArchiveOperationLegacyAdoption != "legacy-adoption" {
		t.Fatalf("persisted spellings = %q %q %q %q", diskArchiveOperationArchive, diskArchiveOperationSupersede, diskArchiveOperationForce, diskArchiveOperationLegacyAdoption)
	}
}

func TestUnknownArchiveOperationPreservesErrors(t *testing.T) {
	t.Parallel()
	req, _, _ := archiveRequestFixture(t)
	req.Operation = "retire"
	if err := validateArchiveRequest(req); err == nil || err.Error() != `unsupported archive operation "retire"` {
		t.Fatalf("request error=%v", err)
	}
	req.Operation = "legacy-adoption"
	if err := validateArchiveRequest(req); err == nil || err.Error() != `unsupported archive operation "legacy-adoption"` {
		t.Fatalf("legacy operation on a normal request error=%v", err)
	}
	_, rec, _, _ := archiveJournalFixture(t)
	rec.Operation = "retire"
	if err := validateArchiveRecord(rec); err == nil || err.Error() != "unknown archive operation" {
		t.Fatalf("record error=%v", err)
	}
	if result, err := archiveResult(rec); err == nil || err.Error() != "unknown archive operation" || result.Operation != "" {
		t.Fatalf("stdout translation result=%+v err=%v", result, err)
	}
}

func TestArchiveOperationBoundaryDiskStdoutAndRecovery(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, operation, source, target, requestID string
		legacy, approve, eligible                  bool
	}{
		{name: "archive", operation: "archive", source: "done/TASK-1.md", target: "_archive/done/TASK-1.md", requestID: strings.Repeat("a", 32), eligible: true},
		{name: "supersede", operation: "supersede", source: "done/TASK-1.md", target: "_archive/done/TASK-1.md", requestID: strings.Repeat("a", 32)},
		{name: "force", operation: "force", source: "done/TASK-1.md", target: "_archive/done/TASK-1.md", requestID: strings.Repeat("a", 32)},
		{name: "legacy-adoption", operation: "legacy-adoption", source: "_archive/done/TASK-1.md", target: "_archive/done/TASK-1.md", requestID: strings.Repeat("d", 32), legacy: true},
		{name: "legacy-adoption-approved", operation: "legacy-adoption", source: "_archive/done/TASK-1.md", target: "_archive/done/TASK-1.md", requestID: strings.Repeat("d", 32), legacy: true, approve: true, eligible: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var dir string
			var result ArchiveResult
			var err error
			var replay func() (ArchiveResult, error)
			if tc.legacy {
				var req LegacyArchiveRequest
				dir, req, _ = legacyArchiveWriterFixture(t)
				if req.RequestID != tc.requestID || req.Source != tc.source {
					t.Fatalf("legacy fixture drift: %+v", req.ArchiveRequest)
				}
				req.ApproveCompletion = tc.approve
				if _, err = legacyArchiveWithStep(dir, req, true, false, repairStopAt("after-archive-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-journal") {
					t.Fatalf("pending boundary: %v", err)
				}
				assertArchiveWire(t, dir, "pending", tc.operation, true)
				result, err = RecoverLegacyArchive(dir, req)
				replay = func() (ArchiveResult, error) { return RecoverLegacyArchive(dir, req) }
			} else {
				var req ArchiveRequest
				dir, req, _ = archiveWriterFixture(t)
				if req.RequestID != tc.requestID || req.Source != tc.source {
					t.Fatalf("archive fixture drift: %+v", req)
				}
				req.Operation = tc.operation
				if tc.operation == "force" {
					req.Assertion = "operator override"
				}
				if _, err = archiveWithStep(dir, req, true, false, repairStopAt("after-archive-journal")); err == nil || !strings.Contains(err.Error(), "stop at after-archive-journal") {
					t.Fatalf("pending boundary: %v", err)
				}
				assertArchiveWire(t, dir, "pending", tc.operation, true)
				result, err = RecoverArchive(dir, req)
				replay = func() (ArchiveResult, error) { return RecoverArchive(dir, req) }
			}
			if err != nil {
				t.Fatalf("recovery: %v", err)
			}
			assertArchiveWire(t, dir, "completed", tc.operation, false)
			assertArchivePublicJSON(t, result, tc.requestID, tc.source, tc.target, tc.operation, tc.eligible)
			stored := mustReadFile(t, filepath.Join(dir, archivesFile))
			again, err := replay()
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			if got := mustReadFile(t, filepath.Join(dir, archivesFile)); !bytes.Equal(got, stored) {
				t.Fatal("recovery replay changed journal bytes")
			}
			assertArchivePublicJSON(t, again, tc.requestID, tc.source, tc.target, tc.operation, tc.eligible)
		})
	}
}

func assertArchiveWire(t *testing.T, dir, state, operation string, payload bool) {
	t.Helper()
	raw := mustReadFile(t, filepath.Join(dir, archivesFile))
	if !bytes.Contains(raw, []byte(`"schemaVersion":1`)) || !bytes.Contains(raw, []byte(`"state":"`+state+`"`)) || !bytes.Contains(raw, []byte(`"operation":"`+operation+`"`)) {
		t.Fatalf("journal wire missing fixed %s/%s", state, operation)
	}
	decoded, err := decodeArchiveJournal(raw)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	if decoded.SchemaVersion != 1 || len(decoded.Records) != 1 {
		t.Fatalf("schema=%d records=%d", decoded.SchemaVersion, len(decoded.Records))
	}
	rec := decoded.Records[0]
	if rec.State != state || rec.Operation != operation {
		t.Fatalf("decoded state=%s operation=%s", rec.State, rec.Operation)
	}
	var wire struct {
		Records []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil || len(wire.Records) != 1 {
		t.Fatalf("record object: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(wire.Records[0], &fields); err != nil {
		t.Fatal(err)
	}
	_, hasOriginal := fields["original"]
	_, hasPatched := fields["patched"]
	if payload {
		if !hasOriginal || !hasPatched || len(rec.Original) == 0 || len(rec.Patched) == 0 {
			t.Fatal("pending archive schema lost original or patched")
		}
		return
	}
	if hasOriginal || hasPatched || len(rec.Original) != 0 || len(rec.Patched) != 0 {
		t.Fatal("completed archive schema retained payload")
	}
}

// TestFrozenPreRefactorArchiveWires reads static schema-1 journals. It decodes
// those bytes and translates known operations. It does not build a journal
// with the current writer.
func TestFrozenPreRefactorArchiveWires(t *testing.T) {
	t.Parallel()
	cases := []struct {
		operation string
		state     string
		stdout    outputvocab.ArchiveOperation
	}{
		{operation: "archive", state: "pending", stdout: outputvocab.ArchiveOp},
		{operation: "archive", state: "completed", stdout: outputvocab.ArchiveOp},
		{operation: "supersede", state: "pending", stdout: outputvocab.Supersede},
		{operation: "supersede", state: "completed", stdout: outputvocab.Supersede},
		{operation: "force", state: "pending", stdout: outputvocab.Force},
		{operation: "force", state: "completed", stdout: outputvocab.Force},
		{operation: "legacy-adoption", state: "pending", stdout: outputvocab.LegacyAdoption},
		{operation: "legacy-adoption", state: "completed", stdout: outputvocab.LegacyAdoption},
	}
	for _, tc := range cases {
		t.Run(tc.operation+"/"+tc.state, func(t *testing.T) {
			t.Parallel()
			encoded := mustReadFile(t, filepath.Join("testdata", "frozen-archive", tc.operation+"-"+tc.state+".b64"))
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
			if err != nil {
				t.Fatalf("frozen bytes: %v", err)
			}
			if !bytes.Contains(raw, []byte(`"schemaVersion":1`)) || !bytes.Contains(raw, []byte(`"state":"`+tc.state+`"`)) || !bytes.Contains(raw, []byte(`"operation":"`+tc.operation+`"`)) {
				t.Fatalf("frozen spelling mismatch for %s %s", tc.operation, tc.state)
			}
			pending := tc.state == "pending"
			hasOriginal := bytes.Contains(raw, []byte(`"original"`))
			hasPatched := bytes.Contains(raw, []byte(`"patched"`))
			if hasOriginal != pending || hasPatched != pending {
				t.Fatalf("payload keys original=%v patched=%v pending=%v", hasOriginal, hasPatched, pending)
			}
			journal, err := decodeArchiveJournal(raw)
			if err != nil {
				t.Fatalf("read frozen journal: %v", err)
			}
			if journal.SchemaVersion != 1 || journal.BoardPath != "/synthetic/tasks" || len(journal.Records) != 1 {
				t.Fatalf("journal schema=%d board=%s records=%d", journal.SchemaVersion, journal.BoardPath, len(journal.Records))
			}
			rec := journal.Records[0]
			if rec.State != tc.state || rec.Operation != tc.operation || rec.ID != "TASK-001" || rec.BoardPath != "/synthetic/tasks" {
				t.Fatalf("record state=%s operation=%s id=%s board=%s", rec.State, rec.Operation, rec.ID, rec.BoardPath)
			}
			if pending {
				if len(rec.Original) == 0 || len(rec.Patched) == 0 {
					t.Fatal("pending frozen record lost payload")
				}
			} else if len(rec.Original) != 0 || len(rec.Patched) != 0 {
				t.Fatal("completed frozen record retained payload")
			}
			result, err := archiveResult(rec)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			if result.Operation != tc.stdout || string(result.Operation) != tc.operation || result.Status != outputvocab.Completed || result.ID != "TASK-001" {
				t.Fatalf("stdout result=%+v", result)
			}
		})
	}
}

func assertArchivePublicJSON(t *testing.T, result ArchiveResult, requestID, source, target, operation string, eligible bool) {
	t.Helper()
	var buf bytes.Buffer
	if err := outputformat.Encode(&buf, result); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("{\"outputVersion\":1,\"requestId\":%q,\"id\":\"TASK-1\",\"source\":%q,\"target\":%q,\"status\":\"completed\",\"operation\":%q,\"completionEligible\":%t}\n", requestID, source, target, operation, eligible)
	if buf.String() != want {
		t.Fatalf("public JSON\n got %s\nwant %s", buf.String(), want)
	}
}
