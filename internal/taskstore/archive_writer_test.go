package taskstore

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

func archiveWriterFixture(t *testing.T) (string, ArchiveRequest, []byte) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "archive me"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-2", Title: "dependent", DependsOn: []string{"TASK-1"}}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "todo", "TASK-1.md")
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: independent review\n"), 1)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(dir, "done", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(filepath.Join(dir, "done", "TASK-1.md"))
	if err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("a", 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	return dir, req, raw
}

func TestArchiveWriterMovesNormalCardAndReplays(t *testing.T) {
	t.Parallel()
	dir, req, raw := archiveWriterFixture(t)
	beforeReady, err := Ready(dir)
	if err != nil || !readyHasID(beforeReady, "TASK-2") {
		t.Fatalf("dependent not ready before archive: %v %+v", err, beforeReady)
	}
	result, err := Archive(dir, req, true)
	if err != nil || result.Status != "completed" || result.Target != "_archive/done/TASK-1.md" || !result.CompletionEligible {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, req.Source)); !os.IsNotExist(err) {
		t.Fatalf("source remains: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, result.Target))
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("archived bytes changed: %v", err)
	}
	afterReady, err := Ready(dir)
	if err != nil || !readyHasID(afterReady, "TASK-2") {
		t.Fatalf("dependent not ready after archive: %v %+v", err, afterReady)
	}
	before := boardBytes(t, dir)
	replay, err := Archive(dir, req, true)
	if err != nil || replay.RequestID != result.RequestID || replay.Target != result.Target {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("exact replay changed board")
	}
}

func readyHasID(entries []Entry, id string) bool {
	for _, entry := range entries {
		if sameIdentity(entry.Card.ID, id) {
			return true
		}
	}
	return false
}

func TestArchiveWriterRequiresExplicitAdoptionAndPreservesHashFailures(t *testing.T) {
	t.Parallel()
	dir, req, _ := archiveWriterFixture(t)
	before := boardBytes(t, dir)
	if _, err := Archive(dir, req, false); err == nil {
		t.Fatal("implicit archive adoption accepted")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("refused adoption changed board")
	}
	wrong := req
	wrong.ExpectedSHA256 = strings.Repeat("0", 64)
	if _, err := Archive(dir, wrong, true); err == nil {
		t.Fatal("wrong source hash accepted")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("hash refusal changed board")
	}
}

func TestArchiveWriterHeldClaimMismatchPreservesBoard(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "claimed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, ClaimRequest{ID: "TASK-1", Owner: "holder", Token: strings.Repeat("b", 32)}); err != nil {
		t.Fatal(err)
	}
	raw := mustReadFile(t, filepath.Join(dir, "todo", "TASK-1.md"))
	req := ArchiveRequest{ID: "TASK-1", Owner: "other", RequestID: strings.Repeat("d", 32), Source: "todo/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "force", Assertion: "operator override", Rules: []byte(archiveCompletionRulesFixture)}
	before := boardBytes(t, dir)
	if _, err := Archive(dir, req, true); err == nil {
		t.Fatal("archive bypassed held claim mismatch")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("held claim refusal changed board")
	}
}

func TestArchiveWriterRecoveryBoundaries(t *testing.T) {
	t.Parallel()
	points := []string{"after-relocation-empty-journal", "after-relocation-common-protocol", "after-relocation-local-protocol", "after-archive-empty-journal", "after-archive-common-protocol", "after-archive-local-protocol", "after-archive-common-pending", "after-archive-journal", "after-stage", "after-target", "after-source", "after-archive-receipt"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			dir, req, raw := archiveWriterFixture(t)
			if _, err := archiveWithStep(dir, req, true, false, repairStopAt(point)); err == nil || !strings.Contains(err.Error(), "stop at "+point) {
				t.Fatalf("boundary not reached: %v", err)
			}
			var result ArchiveResult
			var err error
			if strings.Contains(point, "archive-journal") || strings.Contains(point, "stage") || strings.Contains(point, "target") || strings.Contains(point, "source") || strings.Contains(point, "archive-receipt") {
				result, err = RecoverArchive(dir, req)
			} else {
				result, err = Archive(dir, req, true)
			}
			if err != nil || result.Status != "completed" {
				t.Fatalf("resume=%+v err=%v", result, err)
			}
			got, err := os.ReadFile(filepath.Join(dir, result.Target))
			if err != nil || !bytes.Equal(got, raw) {
				t.Fatalf("recovery lost source bytes: %v", err)
			}
		})
	}
}

func TestRecoverArchiveAbsentRefusesAndPreservesBoard(t *testing.T) {
	t.Parallel()
	dir, req, _ := archiveWriterFixture(t)
	before := boardBytes(t, dir)
	if _, err := RecoverArchive(dir, req); err == nil {
		t.Fatal("recovery-only call started an archive")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("recovery-only refusal changed board")
	}
}

func TestArchiveWriterAlteredTargetRecoveryRefuses(t *testing.T) {
	t.Parallel()
	dir, req, _ := archiveWriterFixture(t)
	if _, err := archiveWithStep(dir, req, true, false, repairStopAt("after-target")); err == nil {
		t.Fatal("target boundary not reached")
	}
	target := filepath.Join(dir, "_archive", "done", "TASK-1.md")
	if err := os.WriteFile(target, append(mustReadFile(t, target), []byte("\n# tampered body\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := RecoverArchive(dir, req); err == nil || !strings.Contains(err.Error(), "card bytes changed") || !strings.Contains(err.Error(), "_archive/done/TASK-1.md") {
		t.Fatalf("altered target recovery error=%v", err)
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("altered target recovery changed board")
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestArchiveWriterCollisionAndNonNormalOperations(t *testing.T) {
	t.Parallel()
	dir, req, raw := archiveWriterFixture(t)
	target := filepath.Join(dir, "_archive", "done")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "TASK-1.md"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	before := boardBytes(t, dir)
	if _, err := Archive(dir, req, true); err == nil {
		t.Fatal("target collision accepted")
	}
	if !reflectEqualBoard(before, boardBytes(t, dir)) {
		t.Fatal("collision changed board")
	}
	for _, operation := range []string{"supersede", "force"} {
		t.Run(operation, func(t *testing.T) {
			dir, req, _ := archiveWriterFixture(t)
			req.Operation = operation
			if operation == "force" {
				req.Assertion = "operator override"
			}
			result, err := Archive(dir, req, true)
			if err != nil || result.CompletionEligible || result.Operation != outputvocab.ArchiveOperation(operation) {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if ready, readyErr := Ready(dir); readyErr != nil || readyHasID(ready, "TASK-2") {
				t.Fatalf("non-normal archive exposed dependent: err=%v ready=%+v", readyErr, ready)
			}
			if operation == "supersede" {
				raw, readErr := os.ReadFile(filepath.Join(dir, result.Target))
				if readErr != nil || !bytes.Contains(raw, []byte("status: superseded")) {
					t.Fatalf("superseded status not persisted: %v", readErr)
				}
			}
		})
	}
}

// TestArchiveOperationVocabularyExhaustive exercises the real switch that
// consumes ArchiveOperation end to end: validateArchiveRecord's
// "switch r.Operation" in archive_record.go. Together with
// TestArchiveWriterCollisionAndNonNormalOperations (which already covers
// "supersede" and "force"), this drives every outputvocab.AllArchiveOperations()
// member through Archive or AdoptLegacyArchive and checks the resulting
// ArchiveResult.Operation round-trips to the matching constant, then checks
// that a value outside the vocabulary hits the switch's default case and is
// rejected rather than silently accepted.
func TestArchiveOperationVocabularyExhaustive(t *testing.T) {
	t.Parallel()
	t.Run("archive", func(t *testing.T) {
		t.Parallel()
		dir, req, _ := archiveWriterFixture(t)
		result, err := Archive(dir, req, true)
		if err != nil || result.Operation != outputvocab.ArchiveOp {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("legacy-adoption", func(t *testing.T) {
		t.Parallel()
		dir, req, _ := legacyArchiveWriterFixture(t)
		result, err := AdoptLegacyArchive(dir, req, true)
		if err != nil || result.Operation != outputvocab.LegacyAdoption {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	})
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		_, r, _, _ := archiveJournalFixture(t)
		r.Operation = "bogus-operation"
		if err := validateArchiveRecord(r); err == nil {
			t.Fatal("validateArchiveRecord accepted an operation outside the declared ArchiveOperation vocabulary; the switch may have grown a default case")
		}
	})
}

func TestArchiveWriterPreservesCategoryPath(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "tasks")
	if err := Init(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, CreateRequest{ID: "TASK-1", Title: "categorized"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "todo", "TASK-1.md")
	raw := mustReadFile(t, source)
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: independent review\n"), 1)
	if err := os.WriteFile(source, raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "done", "auth", "api"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(source, filepath.Join(dir, "done", "auth", "api", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat("c", 32), Source: "done/auth/api/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	result, err := Archive(dir, req, true)
	if err != nil || result.Target != "_archive/done/auth/api/TASK-1.md" {
		t.Fatalf("category result=%+v err=%v", result, err)
	}
	if got := mustReadFile(t, filepath.Join(dir, result.Target)); !bytes.Equal(got, raw) {
		t.Fatal("category archive changed bytes")
	}
}
