package candidatediagnostic

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

// Assess never reads active authority or creates a Store. Call ValidateInput
// before public dispatch so grammar errors remain outside the JSON envelope.
func Assess(ctx context.Context, in Input) Result {
	return assessWith(ctx, in, boardfiles.ReadCandidatePolicy, func(ctx context.Context, in Input) (capturedCandidate, error) {
		return taskflow.CaptureCandidateSnapshot(ctx, in.Repo, in.Board, in.Source, in.Ref)
	})
}

// Dependencies stay package-private: only the production factory above can
// supply captures to public callers. Tests can exercise otherwise unreachable
// stage failures without weakening the sealed capture API.
type capturedCandidate interface {
	Provenance() (string, string)
	Assess(context.Context, boardpolicy.Policy) ([]boardfiles.SnapshotFinding, error)
}

func assessWith(ctx context.Context, in Input, read func(context.Context, string) ([]byte, error), captureSnapshot func(context.Context, Input) (capturedCandidate, error)) Result {
	r := Result{Operation: "assess-cards-only", Mode: "candidate-diagnostic", Status: "unavailable", Findings: []Finding{}}
	fail := func(code, detail string, err error) Result {
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code, detail = "cancelled", "candidate diagnostic was cancelled"
		}
		r.Status, r.Findings = "unavailable", []Finding{}
		r.Error = &DiagnosticError{Code: code, Detail: detail}
		return r
	}
	if err := ctx.Err(); err != nil {
		return fail("cancelled", "candidate diagnostic was cancelled", err)
	}
	if err := ValidateInput(in); err != nil {
		return fail("snapshot-unavailable", "diagnostic input is invalid", err)
	}
	raw, err := read(ctx, in.CandidatePolicy)
	if err != nil {
		detail := "candidate file could not be read as a stable bounded regular file"
		if errors.Is(err, fs.ErrNotExist) {
			detail = "candidate file does not exist"
		} else if errors.Is(err, fs.ErrPermission) {
			detail = "candidate file read was denied"
		}
		return fail("candidate-read", detail, err)
	}
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		return fail("candidate-invalid", "candidate policy failed strict codec validation", err)
	}
	if err := boardfiles.ValidateCardsOnlyPolicy(policy); err != nil {
		return fail("candidate-invalid", "candidate policy is incompatible with cards-only diagnostics", err)
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return fail("candidate-invalid", "candidate policy canonicalization failed", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("cancelled", "candidate diagnostic was cancelled", err)
	}
	r.Candidate = &Candidate{Source: "external-file", InputSHA256: fmt.Sprintf("%x", sha256.Sum256(raw)),
		PolicySHA256: fmt.Sprintf("%x", sha256.Sum256(canonical)), SchemaVersion: policy.SchemaVersion()}
	capture, err := captureSnapshot(ctx, in)
	if err != nil {
		return fail("snapshot-unavailable", "selected Git snapshot could not be captured safely", err)
	}
	tree, index := capture.Provenance()
	r.Snapshot = &Snapshot{Source: in.Source, Board: in.Board}
	if in.Source == "ref" {
		r.Snapshot.TreeOID = &tree
	} else {
		r.Snapshot.IndexDigest = &index
	}
	findings, err := capture.Assess(ctx, policy)
	if err != nil {
		return fail("assessment-unavailable", "captured inventory could not be assessed", err)
	}
	if err := ctx.Err(); err != nil {
		return fail("cancelled", "candidate diagnostic was cancelled", err)
	}
	for _, f := range findings {
		if err := ctx.Err(); err != nil {
			return fail("cancelled", "candidate diagnostic was cancelled", err)
		}
		severity := "error"
		if f.Severity == boardfiles.SnapshotIssueResidue {
			severity = "residue"
		}
		r.Findings = append(r.Findings, Finding{Path: f.Path, Severity: severity, Detail: f.Detail, Contract: f.Contract, Action: f.Action})
	}
	r.Status = "clean"
	if len(r.Findings) > 0 {
		r.Status = "findings"
	}
	if err := ctx.Err(); err != nil {
		return fail("cancelled", "candidate diagnostic was cancelled", err)
	}
	return r
}
