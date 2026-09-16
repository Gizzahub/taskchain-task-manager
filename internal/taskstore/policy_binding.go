package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const policyFile = ".task-manager-policy.json"

// policyForJournal reads only the policy bound to this journal. An orphan
// policy is an interrupted activation, never permission to fall back.
func policyForJournal(r *os.Root, j transitionJournal) (boardpolicy.Policy, error) {
	raw, err := boundedSnapshotFile(r, policyFile, 64<<10)
	if j.SchemaVersion == 1 {
		if !errors.Is(err, fs.ErrNotExist) {
			if err != nil {
				return boardpolicy.Policy{}, fmt.Errorf("inspect policy: %w", err)
			}
			return boardpolicy.Policy{}, errors.New("policy activation is incomplete; preserve policy and journal for explicit recovery")
		}
		if j.PolicyDigest != "" {
			return boardpolicy.Policy{}, errors.New("legacy journal cannot bind a policy")
		}
		return boardpolicy.Default(), nil
	}
	if j.SchemaVersion != 2 || !sharedHex64.MatchString(j.PolicyDigest) {
		return boardpolicy.Policy{}, errors.New("invalid policy journal binding")
	}
	if err != nil {
		return boardpolicy.Policy{}, fmt.Errorf("read bound policy (no default fallback): %w", err)
	}
	p, err := boardpolicy.Parse(raw)
	if err != nil {
		return boardpolicy.Policy{}, fmt.Errorf("parse bound policy: %w", err)
	}
	canonical, err := p.Canonical()
	if err != nil {
		return boardpolicy.Policy{}, err
	}
	if !bytes.Equal(canonical, raw) || bytesDigest(raw) != j.PolicyDigest {
		return boardpolicy.Policy{}, errors.New("bound policy bytes or digest changed; preserve journal and restore the exact policy")
	}
	return p, nil
}

func policyForBoard(r *os.Root) (boardpolicy.Policy, error) {
	j, err := loadTransitions(r)
	if err != nil {
		return boardpolicy.Policy{}, err
	}
	return policyForJournal(r, j)
}
