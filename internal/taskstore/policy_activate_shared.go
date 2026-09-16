package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func activateSharedPolicy(s *sharedSession, policy boardpolicy.Policy, resume bool, step func(string) error) (result PolicyActivationResult, err error) {
	canonical, err := policy.Canonical()
	if err != nil {
		return result, err
	}
	phase := "initializing"
	var plans []policyActivationPlan
	if existing := s.state.Policy; existing != nil {
		if !bytes.Equal(existing.Canonical, canonical) {
			return result, errors.New("initial shared policy is immutable; different policy cannot be activated")
		}
		if existing.Phase == "active" {
			phase = "joining"
		} else {
			if !resume {
				return result, errors.New("shared policy activation interrupted; explicit resume required")
			}
			phase, plans = existing.Phase, existing.Pending
			if phase == "joining" && plans[0].Root != filepath.Join(s.location.Repository, filepath.FromSlash(s.location.Board)) {
				return result, errors.New("pending policy join must resume from its original board")
			}
		}
	} else if resume {
		return result, errors.New("no shared policy activation exists to resume")
	}
	boards, err := openSharedPolicyBoards(s, phase, plans)
	if err != nil {
		return result, err
	}
	mayHavePublished := len(plans) > 0
	defer func() {
		err = errors.Join(err, closeSharedPolicyBoards(boards))
		if err != nil && mayHavePublished {
			err = fmt.Errorf("shared policy activation outcome may be uncertain; preserve state and retry identical policy (pending transaction: use resume; no transaction: retry original invocation): %w", err)
		}
	}()
	if s.state.Policy != nil && s.state.Policy.Phase == "active" {
		state, loadErr := loadPolicyActivation(boards[0].root)
		if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
			return result, loadErr
		}
		if loadErr == nil && state.Plan.Root == boards[0].path && state.Scope == "shared" {
			j, err := loadTransitionsForBundle(boards[0].root)
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
		if resume {
			return result, errors.New("no policy join exists to resume for this board")
		}
	}
	if len(plans) == 0 {
		next, err := prepareSharedPolicy(s, boards, policy, phase)
		if err != nil {
			return result, err
		}
		if err := verifySharedPolicyInventory(s, boards, phase); err != nil {
			return result, err
		}
		mayHavePublished = true
		if err := publishSharedState(s.root, next, false); err != nil {
			return result, err
		}
		s.state = &next
	}
	if err := bundleStep(step, "after-common-policy-pending"); err != nil {
		return result, err
	}
	if err := verifySharedPolicyInventory(s, boards, phase); err != nil {
		return result, err
	}
	// Check every board before advancing any, including a resume from common-only
	// pending state where no local receipt has been created yet.
	for i, b := range boards {
		state := sharedPolicyLocalState(*s.state, s.state.Policy.Pending[i])
		if _, _, err := inspectPolicyActivationPlan(b.root, state); err != nil {
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
			return result, errors.New("shared policy activation has incomplete local receipt")
		}
	}
	completedResult := policyActivationResult(sharedPolicyLocalState(*s.state, s.state.Policy.Pending[0]), false, len(boards))
	next := *s.state
	authority := *s.state.Policy
	authority.Phase, authority.Pending = "active", []policyActivationPlan{}
	next.Policy = &authority
	if err := publishSharedState(s.root, next, false); err != nil {
		return result, err
	}
	s.state = &next
	result = completedResult
	if err := bundleStep(step, "after-common-policy-active"); err != nil {
		return result, err
	}
	return result, nil
}
