package taskstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type PolicyActivationOptions struct {
	Resume       bool
	AllWorktrees bool
}

type PolicyActivationResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	AuthorityID   string `json:"authorityId"`
	Scope         string `json:"scope"`
	Digest        string `json:"digest"`
	Status        string `json:"status"`
	Replayed      bool   `json:"replayed"`
	Boards        int    `json:"boards"`
}

// ActivatePolicy explicitly adopts an immutable policy. Shared-ID namespaces
// adopt one common authority; other boards remain local. It never enables IDs,
// invokes an agent, or changes Git topology.
func ActivatePolicy(dir string, raw []byte, options PolicyActivationOptions) (PolicyActivationResult, error) {
	return activatePolicyWithStep(dir, raw, options, nil)
}

func activatePolicyWithStep(dir string, raw []byte, options PolicyActivationOptions, step func(string) error) (result PolicyActivationResult, err error) {
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		return result, err
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
		return activateSharedPolicy(s, policy, options.Resume, step)
	}
	return activateLocalPolicy(dir, s, policy, options.Resume, step)
}

func finishPolicyActivation(result PolicyActivationResult, operationErr, cleanupErr error) error {
	err := errors.Join(operationErr, cleanupErr)
	if err != nil && result.Status == "completed" {
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
	return PolicyActivationResult{SchemaVersion: 1, AuthorityID: state.AuthorityID, Scope: state.Scope, Digest: state.Digest, Status: "completed", Replayed: replayed, Boards: boards}
}
