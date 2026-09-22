package taskstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type PolicyActivationOptions struct {
	Resume       bool
	AllWorktrees bool
	AdoptModules bool
}

type PolicyActivationResult struct {
	OutputVersion int                        `json:"outputVersion"`
	AuthorityID   string                     `json:"authorityId"`
	Scope         outputvocab.AuthorityScope `json:"scope"`
	Digest        string                     `json:"digest"`
	Status        outputvocab.ResultStatus   `json:"status"`
	Replayed      bool                       `json:"replayed"`
	Boards        int                        `json:"boards"`
}

// ActivatePolicy explicitly adopts an immutable policy. Shared-ID namespaces
// adopt one common authority; other boards remain local. Explicit module adoption
// may initialize the ID ledger. It never invokes an agent or changes Git topology.
func ActivatePolicy(dir string, raw []byte, options PolicyActivationOptions) (PolicyActivationResult, error) {
	return activatePolicyWithStep(dir, raw, options, nil)
}

func activatePolicyWithStep(dir string, raw []byte, options PolicyActivationOptions, step func(string) error) (result PolicyActivationResult, err error) {
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		return result, err
	}
	if options.AdoptModules && len(policy.Modules()) == 0 {
		return result, errors.New("adopt-modules requires a declared module scope")
	}
	s, release, err := acquireSharedForPolicy(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = finishPolicyActivation(result, err, release()) }()
	if s != nil && s.state != nil {
		if (s.state.Policy == nil || s.state.Policy.Phase == "initializing") && !options.AllWorktrees {
			return result, errors.New("shared policy activation requires explicit all-worktrees acknowledgement; no board was changed")
		}
		return activateSharedPolicy(s, policy, options, step)
	}
	return activateLocalPolicyWithOptions(dir, s, policy, options, step)
}

func finishPolicyActivation(result PolicyActivationResult, operationErr, cleanupErr error) error {
	err := errors.Join(operationErr, cleanupErr)
	if err != nil && result.Status == outputvocab.Completed {
		return fmt.Errorf("policy activation may already be completed; preserve state and retry the identical policy to confirm the result: %w", err)
	}
	return err
}

func newPolicyAuthorityID() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(token[:]), nil
}

func policyActivationResult(state policyActivationState, replayed bool, boards int) PolicyActivationResult {
	return PolicyActivationResult{OutputVersion: outputformat.Version, AuthorityID: state.AuthorityID, Scope: outputvocab.AuthorityScope(state.Scope), Digest: state.Digest, Status: outputvocab.Completed, Replayed: replayed, Boards: boards}
}
