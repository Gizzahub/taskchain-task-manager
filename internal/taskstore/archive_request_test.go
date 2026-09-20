package taskstore

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/archivepolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func archiveRequestFixture(t *testing.T) (ArchiveRequest, []byte, boardpolicy.Policy) {
	t.Helper()
	b, raw, _, rules := archiveCompletionFixture(t)
	return ArchiveRequest{ID: b.ID, Owner: "worker", RequestID: b.RequestID, Source: b.Source, ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: rules}, raw, boardpolicy.Default()
}

func TestPrepareArchiveRecordNormalAdmissionAndCompletion(t *testing.T) {
	t.Parallel()
	req, raw, policy := archiveRequestFixture(t)
	rec, err := prepareArchiveRecord(req, raw, 0644, policy, "/synthetic/tasks", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Operation != "archive" || rec.Target != "_archive/done/TASK-001.md" || rec.Completion == nil {
		t.Fatalf("record=%+v", rec)
	}
	if !sameArchive(rec, req) {
		t.Fatal("prepared record does not replay the request")
	}
	if !bytes.Equal(rec.Original, raw) || !bytes.Equal(rec.Patched, raw) {
		t.Fatal("normal archive unexpectedly changed source bytes")
	}
}

func TestPrepareArchiveRecordDeniedAndExplicitOperations(t *testing.T) {
	t.Parallel()
	req, raw, policy := archiveRequestFixture(t)
	denied := append([]byte(nil), raw...)
	denied = bytes.Replace(denied, []byte("review-result: pass"), []byte("review-result: reject"), 1)
	req.ExpectedSHA256 = bytesDigest(denied)
	if _, err := prepareArchiveRecord(req, denied, 0644, policy, "/synthetic/tasks", "", nil); err == nil || !strings.Contains(err.Error(), "admission denied") {
		t.Fatalf("denied review accepted: %v", err)
	}
	for _, operation := range []string{"supersede", "force"} {
		t.Run(operation, func(t *testing.T) {
			x := req
			x.Operation = operation
			x.ExpectedSHA256 = bytesDigest(raw)
			if operation == "force" {
				x.Assertion = "operator override"
			}
			rec, err := prepareArchiveRecord(x, raw, 0644, policy, "/synthetic/tasks", "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Completion != nil || rec.Operation != operation {
				t.Fatalf("unexpected completion/provenance: %+v", rec)
			}
			if operation == "force" && !bytes.Equal(rec.Original, rec.Patched) {
				t.Fatal("force changed source bytes")
			}
		})
	}
}

func TestArchiveRequestStrictFieldsAndRulesReplay(t *testing.T) {
	t.Parallel()
	req, raw, policy := archiveRequestFixture(t)
	for name, mutate := range map[string]func(*ArchiveRequest){
		"id":               func(r *ArchiveRequest) { r.ID = "TASK-01" },
		"request":          func(r *ArchiveRequest) { r.RequestID = "bad" },
		"hash":             func(r *ArchiveRequest) { r.ExpectedSHA256 = strings.Repeat("0", 64) },
		"operation":        func(r *ArchiveRequest) { r.Operation = "legacy-adoption" },
		"rules":            func(r *ArchiveRequest) { r.Rules = []byte(`{}`) },
		"source traversal": func(r *ArchiveRequest) { r.Source = "../done/TASK-001.md" },
		"source absolute":  func(r *ArchiveRequest) { r.Source = "/tmp/done/TASK-001.md" },
		"source backslash": func(r *ArchiveRequest) { r.Source = "done\\TASK-001.md" },
		"source NUL":       func(r *ArchiveRequest) { r.Source = "done/\x00TASK-001.md" },
		"source too long":  func(r *ArchiveRequest) { r.Source = strings.Repeat("a", 1024) },
	} {
		t.Run(name, func(t *testing.T) {
			x := req
			mutate(&x)
			if _, err := prepareArchiveRecord(x, raw, 0644, policy, "/synthetic/tasks", "", nil); err == nil {
				t.Fatal("invalid archive request accepted")
			}
		})
	}
	changedRules := append([]byte(nil), req.Rules...)
	changedRules = bytes.Replace(changedRules, []byte("pass"), []byte("accepted"), 1)
	x := req
	x.Rules = changedRules
	if _, err := archivepolicy.ParseConfig(changedRules); err != nil {
		t.Fatal("changed rules fixture invalid:", err)
	}
	rec, err := prepareArchiveRecord(req, raw, 0644, policy, "/synthetic/tasks", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sameArchive(rec, x) {
		t.Fatal("different canonical rules replayed as same archive")
	}
	formatted := req
	formatted.Rules = []byte(archiveCompletionRulesFixture)
	if !sameArchive(rec, formatted) {
		t.Fatal("semantically identical formatted rules failed replay comparison")
	}
}

func TestPrepareArchiveRecordPlanReferencesUseResolver(t *testing.T) {
	t.Parallel()
	req, _, policy := archiveRequestFixture(t)
	req.ID = "PLAN-1"
	req.Source = "plan/PLAN-1.md"
	req.ExpectedSHA256 = bytesDigest([]byte("---\nid: PLAN-1\nchild-ids: [TASK-2]\n---\n"))
	raw := []byte("---\nid: PLAN-1\nchild-ids: [TASK-2]\n---\n")
	if _, err := prepareArchiveRecord(req, raw, 0644, policy, "/synthetic/tasks", "", func(string) (bool, error) { return false, nil }); err == nil || !strings.Contains(err.Error(), "admission denied") {
		t.Fatalf("unfinished child accepted: %v", err)
	}
	rec, err := prepareArchiveRecord(req, raw, 0644, policy, "/synthetic/tasks", "", func(string) (bool, error) { return true, nil })
	if err != nil || rec.Completion != nil {
		t.Fatalf("resolved plan preparation failed: %+v %v", rec, err)
	}
	wantErr := errors.New("resolver failure")
	_, err = prepareArchiveRecord(req, raw, 0644, policy, "/synthetic/tasks", "", func(string) (bool, error) { return false, wantErr })
	if err == nil || !strings.Contains(err.Error(), "resolver failure") {
		t.Fatalf("resolver error lost: %v", err)
	}
}

func TestArchiveRequestRejectsUnsafeSourceBeforeBoardAccess(t *testing.T) {
	t.Parallel()
	req, _, _ := archiveRequestFixture(t)
	for _, source := range []string{"../done/TASK-001.md", "..", ".", "/done/TASK-001.md", "done/../TASK-001.md", "done\\TASK-001.md", "done/\x00.md", strings.Repeat("x", 1024)} {
		x := req
		x.Source = source
		if err := validateArchiveRequest(x); err == nil {
			t.Fatalf("unsafe source %q accepted", source)
		}
	}
	req.Owner = string([]byte{0xff})
	if err := validateArchiveRequest(req); err == nil {
		t.Fatal("invalid UTF-8 owner accepted")
	}
}
