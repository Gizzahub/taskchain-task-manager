package candidatediagnostic

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type failedCandidateAssessment struct{ cancel context.CancelFunc }

func (failedCandidateAssessment) Provenance() (string, string) { return "", strings.Repeat("a", 64) }
func (c failedCandidateAssessment) Assess(context.Context, boardpolicy.Policy) ([]boardfiles.SnapshotFinding, error) {
	if c.cancel != nil {
		c.cancel()
	}
	return []boardfiles.SnapshotFinding{{Path: "partial.md"}}, errors.New("private input and filesystem details")
}

func TestCandidateDiagnosticStagesDoNotLeakPartialResults(t *testing.T) {
	in := Input{Repo: "/not-opened", Board: "tasks", CandidatePolicy: "/not-opened", Source: "index"}
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		c := failedCandidateAssessment{}
		if cancelled {
			c.cancel = cancel
		}
		result := assessWith(ctx, in,
			func(context.Context, string) ([]byte, error) { return boardpolicy.Default().Canonical() },
			func(context.Context, Input) (capturedCandidate, error) { return c, nil })
		cancel()
		want := "assessment-unavailable"
		if cancelled {
			want = "cancelled"
		}
		if result.Error == nil || result.Error.Code != want || result.ExitCode() != 2 ||
			result.Candidate == nil || result.Snapshot == nil || len(result.Findings) != 0 || strings.Contains(result.Error.Detail, "private input") {
			t.Fatalf("lost stage provenance or leaked partial result: %+v", result)
		}
	}
}

func TestCandidateDiagnosticInvalidPolicyNeverCaptures(t *testing.T) {
	in := Input{Repo: "/not-opened", Board: "tasks", CandidatePolicy: "/not-opened", Source: "index"}
	result := assessWith(context.Background(), in,
		func(context.Context, string) ([]byte, error) { return []byte("invalid policy"), nil },
		func(context.Context, Input) (capturedCandidate, error) {
			t.Fatal("capture called for invalid candidate")
			return nil, nil
		})
	if result.Error == nil || result.Error.Code != "candidate-invalid" || result.Candidate != nil || result.Snapshot != nil {
		t.Fatalf("wrong invalid-candidate result: %+v", result)
	}
}

type cleanCandidateAssessment struct{}

func (cleanCandidateAssessment) Provenance() (string, string) { return "", strings.Repeat("b", 64) }
func (cleanCandidateAssessment) Assess(context.Context, boardpolicy.Policy) ([]boardfiles.SnapshotFinding, error) {
	return nil, nil
}

func TestCandidateDiagnosticSchemaAndSemanticValidation(t *testing.T) {
	input := Input{Repo: "/unused", Board: "tasks", CandidatePolicy: "/unused", Source: "index"}
	for version := 1; version <= 5; version++ {
		decl := boardpolicy.Declaration{}
		if version >= 2 {
			decl.Relocations = []boardpolicy.Transition{}
		}
		if version >= 3 {
			decl.Modules = []string{}
		}
		if version == 4 {
			decl.FileRolesDeclared = true
		}
		var policy boardpolicy.Policy
		var err error
		if version == 5 {
			policy, err = boardpolicy.NewV5(decl, nil)
		} else {
			policy, err = boardpolicy.New(decl)
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := policy.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		raw = append([]byte("# prospective input\n"), raw...)
		result := assessWith(context.Background(), input,
			func(context.Context, string) ([]byte, error) { return raw, nil },
			func(context.Context, Input) (capturedCandidate, error) { return cleanCandidateAssessment{}, nil })
		if result.ExitCode() != 0 || result.Candidate == nil || result.Candidate.SchemaVersion != version || result.Candidate.InputSHA256 == result.Candidate.PolicySHA256 {
			t.Fatalf("schema/digest source conflated: version=%d %+v", version, result)
		}
	}
	policy, err := boardpolicy.New(boardpolicy.Declaration{Modules: []string{}, FileRolesDeclared: true,
		FileRoles: []boardpolicy.FileRole{{Path: "AGENTS.md", Kind: boardpolicy.FileRoleGuidance}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := policy.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	result := assessWith(context.Background(), input,
		func(context.Context, string) ([]byte, error) { return raw, nil },
		func(context.Context, Input) (capturedCandidate, error) {
			t.Fatal("incompatible semantic policy reached capture")
			return nil, nil
		})
	if result.Error == nil || result.Error.Code != "candidate-invalid" || result.Candidate != nil {
		t.Fatalf("semantic refusal lost: %+v", result)
	}
}
