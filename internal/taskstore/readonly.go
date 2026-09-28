package taskstore

import "errors"

// ListReadOnly returns the normal card projection without acquiring the board
// writer locks or creating lock directories. It validates pending transitions,
// shared namespace state and identity bindings, policy, cards, and claims like
// List. It refuses an existing writer lock instead of waiting. Callers that
// require a stable observation must bracket the call with their own
// identity/content check; this function does not participate in writer
// coordination.
func ListReadOnly(dir string) (entries []Entry, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	shared, closeShared, err := openSharedReadOnly(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, closeShared()) }()
	if err := rejectReadOnlyLock(r, "task board"); err != nil {
		return nil, err
	}
	if err := shared.verifyBoard(r); err != nil {
		return nil, err
	}
	if err := rejectPendingTransitions(r); err != nil {
		return nil, err
	}
	entries, err = listLocked(r)
	if err != nil {
		return nil, err
	}
	if _, err := loadClaims(r, entries); err != nil {
		return nil, err
	}
	return entries, nil
}
