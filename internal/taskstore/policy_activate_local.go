package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func activateLocalPolicy(dir string, s *sharedSession, policy boardpolicy.Policy, resume bool, step func(string) error) (result PolicyActivationResult, err error) {
	return activateLocalPolicyWithOptions(dir, s, policy, PolicyActivationOptions{Resume: resume}, step)
}

func activateLocalPolicyWithOptions(dir string, s *sharedSession, policy boardpolicy.Policy, options PolicyActivationOptions, step func(string) error) (result PolicyActivationResult, err error) {
	resume := options.Resume
	r, err := openBoard(dir)
	if err != nil {
		return result, err
	}
	unlock, err := lock(r)
	if err != nil {
		return result, errors.Join(err, r.Close())
	}
	defer func() {
		cleanup := errors.Join(unlock(), r.Close())
		err = errors.Join(err, cleanup)
	}()
	if err := s.verifyBoardIdentity(r); err != nil {
		return result, err
	}
	if err := s.verifyLocalIDBinding(r); err != nil {
		return result, err
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return result, err
	}
	digest := bytesDigest(canonical)
	activation, loadErr := loadPolicyActivation(r)
	if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
		return result, errors.Join(errors.New("policy activation binding unavailable; preserve activation state"), loadErr)
	}
	if loadErr == nil {
		if activation.Revision != nil && activation.Phase != "completed" {
			return result, errors.New("policy revision is pending; use explicit revision resume")
		}
		if activation.Scope != "local" || activation.Namespace != "" {
			return result, errors.New("shared policy activation cannot be downgraded to local")
		}
		if activation.Digest != digest || !bytes.Equal(activation.Canonical, canonical) {
			return result, errors.New("different policy cannot replace the existing activation")
		}
		if activation.Plan.Root != "" {
			owner, ownerErr := filepath.EvalSymlinks(r.Name())
			if ownerErr != nil {
				return result, ownerErr
			}
			owner, ownerErr = filepath.Abs(owner)
			if ownerErr != nil || owner != activation.Plan.Root {
				if ownerErr == nil {
					ownerErr = errors.New("policy activation belongs to another board")
				}
				return result, ownerErr
			}
			if err := verifyBoardHandle(r, owner); err != nil {
				return result, err
			}
		}
		if activation.Phase != "completed" {
			if (activation.Plan.ModuleAdoption != nil) != options.AdoptModules {
				return result, errors.New("pending module adoption requires the original adopt-modules flag")
			}
			if !resume {
				return result, errors.New("policy activation is pending; explicit resume required")
			}
			if err := applyPolicyActivationPlan(r, activation, step); err != nil {
				return result, fmt.Errorf("policy activation may be partially published; preserve state and retry identical policy (pending transaction: use resume; no transaction: retry original invocation): %w", err)
			}
			return policyActivationResult(activation, false, 1), nil
		}
		j, err := loadTransitionsForBundle(r)
		if err != nil {
			return result, err
		}
		if err := s.verifyPolicyAuthority(r, j); err != nil {
			return result, err
		}
		return policyActivationResult(activation, true, 1), nil
	}
	if resume {
		return result, errors.New("policy activation has no recorded transaction to resume")
	}
	authorityID, err := newPolicyAuthorityID()
	if err != nil {
		return result, err
	}
	j, err := loadTransitions(r)
	if err != nil {
		return result, err
	}
	state, _, err := preparePolicyChangeWithModules(r, policy, policyAuthorityBinding{AuthorityID: authorityID, Scope: "local"}, "", j, nil, options.AdoptModules)
	if err != nil {
		return result, err
	}
	if err := applyPolicyActivationPlan(r, state, step); err != nil {
		return result, fmt.Errorf("policy activation may be partially published; preserve state and retry identical policy (pending transaction: use resume; no transaction: retry original invocation): %w", err)
	}
	return policyActivationResult(state, false, 1), nil
}
