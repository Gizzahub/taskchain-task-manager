package taskstore

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func reviseSharedPolicy(s *sharedSession, policy boardpolicy.Policy, options PolicyRevisionOptions, step func(string) error) (result PolicyActivationResult, err error) {
	if s.state.Policy == nil {
		return result, errors.New("shared policy revision requires an active authority")
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return result, err
	}
	existing := s.state.Policy
	var plans []policyActivationPlan
	if existing.Phase == "revising" {
		rev := existing.Revision
		if !options.Resume || rev == nil || rev.PreviousAuthorityID != options.ExpectedAuthorityID || rev.PreviousDigest != options.ExpectedDigest || !bytes.Equal(existing.Canonical, canonical) {
			return result, errors.New("shared policy revision requires identical explicit resume")
		}
		plans = existing.Pending
	} else if existing.Phase != "active" {
		return result, errors.New("initial policy activation or join must finish before revision")
	} else if existing.AuthorityID != options.ExpectedAuthorityID || existing.Digest != options.ExpectedDigest {
		return replaySharedRevision(s, canonical, options)
	} else if options.Resume {
		return result, errors.New("no shared policy revision exists to resume")
	}
	boards, err := openSharedPolicyBoards(s, "revising", plans)
	if err != nil {
		return result, err
	}
	mayHavePublished := len(plans) > 0
	defer func() {
		err = errors.Join(err, closeSharedPolicyBoards(boards))
		if err != nil && mayHavePublished {
			err = uncertainPolicyRevision(err)
		}
	}()
	if len(plans) == 0 {
		id, err := newPolicyAuthorityID()
		if err != nil {
			return result, err
		}
		next := *s.state
		authority := sharedPolicyAuthority{AuthorityID: id, Phase: "revising", Canonical: canonical, Digest: bytesDigest(canonical), Pending: []policyActivationPlan{}, Revision: &policyRevision{PreviousAuthorityID: existing.AuthorityID, PreviousCanonical: bytes.Clone(existing.Canonical), PreviousDigest: existing.Digest}}
		for _, b := range boards {
			j, err := loadTransitions(b.root)
			if err != nil {
				return result, err
			}
			if err := s.verifyPolicyAuthority(b.root, j); err != nil {
				return result, err
			}
			state, err := preparePolicyRevision(b.root, policy, policyAuthorityBinding{AuthorityID: id, Scope: "shared", Namespace: next.NamespaceID}, b.head, options)
			if err != nil {
				return result, err
			}
			if _, _, err := inspectPolicyActivationPlan(b.root, state); err != nil {
				return result, err
			}
			authority.Pending = append(authority.Pending, state.Plan)
		}
		if err := verifySharedPolicyInventory(s, boards, "revising"); err != nil {
			return result, err
		}
		next.Policy, next.PolicyRevisionProtocol = &authority, 1
		mayHavePublished = true
		if err := publishSharedState(s.root, next, false); err != nil {
			return result, err
		}
		s.state = &next
	}
	if err := bundleStep(step, "after-common-policy-pending"); err != nil {
		return result, err
	}
	return finishSharedPolicyChange(s, boards, "revising", step)
}

func replaySharedRevision(s *sharedSession, canonical []byte, options PolicyRevisionOptions) (result PolicyActivationResult, err error) {
	boards, err := openSharedPolicyBoards(s, "joining", nil)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, closeSharedPolicyBoards(boards)) }()
	state, err := loadPolicyActivation(boards[0].root)
	if err != nil {
		return result, err
	}
	if state.Phase != "completed" || !matchesPolicyRevision(state, canonical, options) {
		return result, errors.New("shared policy revision compare-and-swap binding mismatch")
	}
	j, err := loadTransitions(boards[0].root)
	if err != nil {
		return result, err
	}
	if err := s.verifyPolicyAuthority(boards[0].root, j); err != nil {
		return result, err
	}
	if err := verifySharedPolicyInventory(s, boards, "joining"); err != nil {
		return result, err
	}
	return policyActivationResult(state, true, 1), nil
}

// All participants are inspected before any local publication and again before
// clearing the common barrier. Both initial activation and revision use it.
func finishSharedPolicyChange(s *sharedSession, boards []sharedPolicyBoard, phase string, step func(string) error) (result PolicyActivationResult, err error) {
	if err := verifySharedPolicyInventory(s, boards, phase); err != nil {
		return result, err
	}
	for i, b := range boards {
		if _, _, err := inspectPolicyActivationPlan(b.root, sharedPolicyLocalState(*s.state, s.state.Policy.Pending[i])); err != nil {
			return result, err
		}
	}
	for i, b := range boards {
		if err := verifyPolicyCommonState(s); err != nil {
			return result, err
		}
		state := sharedPolicyLocalState(*s.state, s.state.Policy.Pending[i])
		if err := applyPolicyActivationPlan(b.root, state, func(at string) error { return bundleStep(step, fmt.Sprintf("board-%d/%s", i, at)) }); err != nil {
			return result, err
		}
		if err := bundleStep(step, fmt.Sprintf("after-policy-board-%d", i)); err != nil {
			return result, err
		}
	}
	if err := verifySharedPolicyInventory(s, boards, phase); err != nil {
		return result, err
	}
	for i, b := range boards {
		files, _, err := inspectPolicyActivationPlan(b.root, sharedPolicyLocalState(*s.state, s.state.Policy.Pending[i]))
		if err != nil {
			return result, err
		}
		if !files.completed {
			return result, errors.New("shared policy change has incomplete local receipt")
		}
	}
	completed := policyActivationResult(sharedPolicyLocalState(*s.state, s.state.Policy.Pending[0]), false, len(boards))
	next := *s.state
	authority := *s.state.Policy
	authority.Phase, authority.Pending = "active", []policyActivationPlan{}
	next.Policy = &authority
	if err := publishSharedState(s.root, next, false); err != nil {
		return result, err
	}
	s.state = &next
	result = completed
	if err := bundleStep(step, "after-common-policy-active"); err != nil {
		return result, err
	}
	return result, nil
}
