package taskstore

import (
	"errors"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// ReadActivePolicy returns the policy bound to the board's active journal and
// activation authority. It is read-only: unlike writer sessions it creates no
// lock or shared namespace state, and it refuses an observation while a writer
// lock is present. Callers must not parse the tracked policy file themselves.
func ReadActivePolicy(dir string) (policy boardpolicy.Policy, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return boardpolicy.Policy{}, err
	}
	defer func() { err = errors.Join(err, r.Close()) }()

	shared, release, err := openSharedReadOnly(dir)
	if err != nil {
		return boardpolicy.Policy{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	if err := rejectReadOnlyLock(r, "task board"); err != nil {
		return boardpolicy.Policy{}, err
	}
	if err := shared.verifyBoard(r); err != nil {
		return boardpolicy.Policy{}, err
	}
	return policyForBoard(r)
}
