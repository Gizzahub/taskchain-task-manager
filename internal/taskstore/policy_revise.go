package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type PolicyRevisionOptions struct {
	ExpectedAuthorityID string
	ExpectedDigest      string
	Resume              bool
	AllWorktrees        bool
	AdoptModules        bool
}

// RevisePolicy is an explicit compare-and-swap of an already active policy.
// It never adopts a board or implicitly resumes another revision.
func RevisePolicy(dir string, raw []byte, options PolicyRevisionOptions) (PolicyActivationResult, error) {
	return revisePolicyWithStep(dir, raw, options, nil)
}

func revisePolicyWithStep(dir string, raw []byte, options PolicyRevisionOptions, step func(string) error) (result PolicyActivationResult, err error) {
	if !sharedHex32.MatchString(options.ExpectedAuthorityID) || !sharedHex64.MatchString(options.ExpectedDigest) {
		return result, errors.New("policy revision requires exact previous authority ID and digest")
	}
	policy, err := boardpolicy.Parse(raw)
	if err != nil {
		return result, err
	}
	digest, err := policy.Digest()
	if err != nil {
		return result, err
	}
	if digest == options.ExpectedDigest {
		return result, errors.New("policy revision must change the policy digest")
	}
	s, release, err := acquireSharedForPolicy(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = finishPolicyActivation(result, err, release()) }()
	if s != nil && s.state != nil {
		if !options.AllWorktrees {
			return result, errors.New("shared policy revision requires explicit all-worktrees acknowledgement")
		}
		return reviseSharedPolicy(s, policy, options, step)
	}
	return reviseLocalPolicy(dir, s, policy, options, step)
}

func matchesPolicyRevision(state policyActivationState, canonical []byte, options PolicyRevisionOptions) bool {
	return (state.Plan.ModuleAdoption != nil) == options.AdoptModules && state.Revision != nil && state.Revision.PreviousAuthorityID == options.ExpectedAuthorityID && state.Revision.PreviousDigest == options.ExpectedDigest && bytes.Equal(state.Canonical, canonical)
}

func reviseLocalPolicy(dir string, s *sharedSession, policy boardpolicy.Policy, options PolicyRevisionOptions, step func(string) error) (result PolicyActivationResult, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return result, err
	}
	unlock, err := lock(r)
	if err != nil {
		return result, errors.Join(err, r.Close())
	}
	defer func() { err = errors.Join(err, unlock(), r.Close()) }()
	if err := s.verifyBoardIdentity(r); err != nil {
		return result, err
	}
	if err := s.verifyLocalIDBinding(r); err != nil {
		return result, err
	}
	state, err := loadPolicyActivation(r)
	if err != nil {
		return result, err
	}
	if state.Scope != "local" || state.Namespace != "" {
		return result, errors.New("shared revision cannot become local")
	}
	if err := verifyRevisionOwner(r, state); err != nil {
		return result, err
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return result, err
	}
	if matchesPolicyRevision(state, canonical, options) {
		if state.Phase == "completed" {
			if err := s.verifyBoard(r); err != nil {
				return result, err
			}
			return policyActivationResult(state, true, 1), nil
		}
		if !options.Resume {
			return result, errors.New("policy revision pending; explicit revision resume required")
		}
	} else {
		if options.Resume || state.Phase != "completed" {
			return result, errors.New("no matching policy revision to resume")
		}
		id, err := newPolicyAuthorityID()
		if err != nil {
			return result, err
		}
		state, err = preparePolicyRevision(r, policy, policyAuthorityBinding{AuthorityID: id, Scope: "local"}, "", options)
		if err != nil {
			return result, err
		}
	}
	if err := applyPolicyActivationPlan(r, state, step); err != nil {
		return result, uncertainPolicyRevision(err)
	}
	return policyActivationResult(state, false, 1), nil
}

func uncertainPolicyRevision(err error) error {
	return fmt.Errorf("policy revision may be partially published; preserve target policy and expected authority/digest (pending transaction: use explicit revision resume; no transaction: retry original invocation): %w", err)
}

func verifyRevisionOwner(r *os.Root, state policyActivationState) error {
	owner, err := filepath.EvalSymlinks(r.Name())
	if err != nil {
		return err
	}
	owner, err = filepath.Abs(owner)
	if err != nil {
		return err
	}
	if owner != state.Plan.Root {
		return errors.New("revision belongs to another board; explicit join required")
	}
	return verifyBoardHandle(r, state.Plan.Root)
}
