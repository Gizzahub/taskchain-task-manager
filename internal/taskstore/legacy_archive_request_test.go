package taskstore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func legacyArchiveRequestFixture(t *testing.T, raw []byte) (LegacyArchiveRequest, boardpolicy.Policy) {
	t.Helper()
	policy := boardpolicy.Default()
	return LegacyArchiveRequest{
		ArchiveRequest: ArchiveRequest{
			ID: "TASK-001", Owner: "operator", Token: strings.Repeat("b", 32), RequestID: strings.Repeat("a", 32),
			Source: "_archive/done/TASK-001.md", ExpectedSHA256: bytesDigest(raw), Operation: "legacy-adoption",
			Assertion: "operator observed the archived card", Rules: []byte(archiveCompletionRulesFixture),
		},
		ExpectedMode: 0o644,
	}, policy
}

func TestPrepareLegacyArchiveRecordPreservesRawCardWithoutCompletion(t *testing.T) {
	_, raw, _, _ := archiveCompletionFixture(t)
	req, policy := legacyArchiveRequestFixture(t, raw)
	rec, err := prepareLegacyArchiveRecord(req, raw, 0o644, policy, "/synthetic/tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Operation != "legacy-adoption" || rec.Completion != nil || !bytes.Equal(rec.Original, raw) || !bytes.Equal(rec.Patched, raw) || rec.Source != rec.Target {
		t.Fatalf("unexpected legacy record: %+v", rec)
	}
	if !sameLegacyArchive(rec, req) {
		t.Fatal("record does not replay with equivalent request")
	}
}

func TestPrepareLegacyArchiveApprovedTaskCreatesLegacyCompletion(t *testing.T) {
	_, raw, _, _ := archiveCompletionFixture(t)
	req, policy := legacyArchiveRequestFixture(t, raw)
	req.ApproveCompletion = true
	rec, err := prepareLegacyArchiveRecord(req, raw, req.ExpectedMode, policy, "/synthetic/tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Completion == nil || rec.Completion.Provenance != "legacy-completion" || rec.Completion.Assertion != req.Assertion {
		t.Fatalf("missing legacy completion binding: %+v", rec.Completion)
	}
	if err := validateArchiveRecord(rec); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareLegacyArchiveRejectsMalformedAdmission(t *testing.T) {
	_, raw, _, _ := archiveCompletionFixture(t)
	base, policy := legacyArchiveRequestFixture(t, raw)
	if _, err := prepareLegacyArchiveRecord(base, raw, base.ExpectedMode, policy, "/synthetic/tasks", ""); err != nil {
		t.Fatalf("valid baseline: %v", err)
	}
	cases := map[string]struct {
		mutate func(*LegacyArchiveRequest)
		want   string
	}{
		"operation": {func(r *LegacyArchiveRequest) { r.Operation = "archive" }, "operation"},
		"hash":      {func(r *LegacyArchiveRequest) { r.ExpectedSHA256 = strings.Repeat("0", 64) }, "source bytes"},
		"path":      {func(r *LegacyArchiveRequest) { r.Source = "done/TASK-001.md" }, "archived"},
		"id":        {func(r *LegacyArchiveRequest) { r.ID = "TASK-002" }, "raw identity"},
		"assertion": {func(r *LegacyArchiveRequest) { r.Assertion = "  " }, "explicit assertion"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := base
			tc.mutate(&req)
			if _, err := prepareLegacyArchiveRecord(req, raw, 0o644, policy, "/synthetic/tasks", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error=%v, want %q", err, tc.want)
			}
		})
	}
	for name, req := range map[string]LegacyArchiveRequest{
		"mode mismatch": func() LegacyArchiveRequest { r := base; r.ExpectedMode = 0o640; return r }(),
		"zero mode":     func() LegacyArchiveRequest { r := base; r.ExpectedMode = 0; return r }(),
		"high bit mode": func() LegacyArchiveRequest { r := base; r.ExpectedMode = 0o1000; return r }(),
	} {
		t.Run(name, func(t *testing.T) {
			actualMode := req.ExpectedMode
			if name == "mode mismatch" {
				actualMode = 0o644
			}
			if _, err := prepareLegacyArchiveRecord(req, raw, actualMode, policy, "/synthetic/tasks", ""); err == nil || !strings.Contains(err.Error(), "mode") {
				t.Fatalf("error=%v, want mode error", err)
			}
		})
	}
	if _, err := prepareLegacyArchiveRecord(base, raw, 0o644, policy, "relative/tasks", ""); err == nil || !strings.Contains(err.Error(), "board") {
		t.Fatalf("error=%v, want board error", err)
	}
	if _, err := prepareLegacyArchiveRecord(base, raw, 0o644, policy, "/synthetic/tasks", "bad"); err == nil || !strings.Contains(err.Error(), "board") {
		t.Fatalf("error=%v, want namespace error", err)
	}
}

func TestPrepareLegacyArchiveRejectsApprovedSupersededAndNonTask(t *testing.T) {
	_, raw, _, _ := archiveCompletionFixture(t)
	req, policy := legacyArchiveRequestFixture(t, raw)
	req.ApproveCompletion = true
	superseded := []byte("---\nid: TASK-001\nstatus: superseded\n---\n")
	supersededReq := req
	supersededReq.ExpectedSHA256 = bytesDigest(superseded)
	if _, err := prepareLegacyArchiveRecord(supersededReq, superseded, 0o644, policy, "/synthetic/tasks", ""); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("superseded approval error=%v", err)
	}
	nonTask := append([]byte(nil), raw...)
	nonTask = bytes.Replace(nonTask, []byte("id: TASK-001"), []byte("id: PLAN-001"), 1)
	req.ID = "PLAN-001"
	req.ExpectedSHA256 = bytesDigest(nonTask)
	if _, err := prepareLegacyArchiveRecord(req, nonTask, 0o644, policy, "/synthetic/tasks", ""); err == nil || !strings.Contains(err.Error(), "TASK") {
		t.Fatalf("non-TASK approval error=%v", err)
	}
}

func TestSameLegacyArchiveBindsApprovalAndMode(t *testing.T) {
	_, raw, _, _ := archiveCompletionFixture(t)
	req, policy := legacyArchiveRequestFixture(t, raw)
	rec, err := prepareLegacyArchiveRecord(req, raw, 0o644, policy, "/synthetic/tasks", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sameLegacyArchive(rec, req) {
		t.Fatal("base request did not match")
	}
	approved := req
	approved.ApproveCompletion = true
	if sameLegacyArchive(rec, approved) {
		t.Fatal("approval change replayed as same request")
	}
	changedMode := req
	changedMode.ExpectedMode = 0o640
	if sameLegacyArchive(rec, changedMode) {
		t.Fatal("mode change replayed as same request")
	}
	canonicalRules := []byte("schema-version: 1\narchive-admission:\n  fields:\n    children: child-ids\n    evidence: review-proof\n    promoted-to: promoted\n    resolution: disposition\n    review: review-result\n  accepted-reviews: [pass, conditional, waived]\n")
	canonicalRulesReq := req
	canonicalRulesReq.Rules = canonicalRules
	if !sameLegacyArchive(rec, canonicalRulesReq) {
		t.Fatal("canonical-equivalent rules did not replay")
	}
}
