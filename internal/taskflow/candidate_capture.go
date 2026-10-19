package taskflow

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type candidateCaptureSeal struct{ marker byte }

var completeCandidateSeal = &candidateCaptureSeal{marker: 1}

// CandidateCapture can only be minted by CaptureCandidateSnapshot after all
// capture checks succeed. It exposes neither its raw snapshot nor a constructor
// accepting caller-supplied entries. It establishes no mutation authority.
type CandidateCapture struct {
	snapshot            boardfiles.CardsOnlySnapshot
	source, tree, index string
	seal                *candidateCaptureSeal
}

func CaptureCandidateSnapshot(ctx context.Context, root, board, source, ref string) (CandidateCapture, error) {
	var raw CardsOnlyCapture
	var err error
	switch source {
	case "worktree":
		raw, err = CaptureCardsOnlyWorktree(ctx, root, board)
	case "index":
		raw, err = CaptureCardsOnlyIndex(ctx, root, board)
	case "ref":
		raw, err = CaptureCardsOnlyRef(ctx, root, board, ref)
	default:
		return CandidateCapture{}, errors.New("unsupported candidate capture source")
	}
	if err != nil {
		return CandidateCapture{}, err
	}
	c := CandidateCapture{snapshot: raw.Snapshot, source: source, tree: raw.TreeOID, index: raw.IndexDigest}
	if !c.validProvenance() {
		return CandidateCapture{}, errors.New("candidate capture provenance is inconsistent")
	}
	if err := ctx.Err(); err != nil {
		return CandidateCapture{}, err
	}
	c.seal = completeCandidateSeal
	return c, nil
}

func (c CandidateCapture) validProvenance() bool {
	if c.source == "ref" {
		return captureOID(c.tree) && c.index == ""
	}
	if c.source != "index" && c.source != "worktree" {
		return false
	}
	b, err := hex.DecodeString(c.index)
	return c.tree == "" && err == nil && len(b) == 32
}

func (c CandidateCapture) Provenance() (tree, index string) { return c.tree, c.index }

func (c CandidateCapture) Assess(ctx context.Context, policy boardpolicy.Policy) ([]boardfiles.SnapshotFinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.seal != completeCandidateSeal || !c.validProvenance() {
		return nil, errors.New("candidate assessment requires a sealed complete capture")
	}
	return boardfiles.AssessCompleteCardsOnlyInventory(ctx, c.snapshot, policy)
}
