package taskstore

import (
	"errors"
	"os"
)

// A board session participates in repository coordination even when it does
// not allocate IDs. Existing ID writers already hold this common lock and
// must not acquire a second session while their operation is in progress.
type boardSession struct {
	root          *os.Root
	unlock        func() error
	releaseCommon func() error
}

func openBoardSession(dir string) (*boardSession, error) {
	shared, release, err := acquireShared(dir, false)
	if err != nil {
		return nil, err
	}
	r, err := openBoard(dir)
	if err != nil {
		return nil, errors.Join(err, release())
	}
	unlock, err := lock(r)
	if err != nil {
		return nil, errors.Join(err, r.Close(), release())
	}
	s := &boardSession{root: r, unlock: unlock, releaseCommon: release}
	if err := shared.verifyBoard(r); err != nil {
		return nil, errors.Join(err, s.close())
	}
	return s, nil
}

func (s *boardSession) close() error {
	// Preserve the inverse acquisition order and report every cleanup error.
	return errors.Join(s.unlock(), s.root.Close(), s.releaseCommon())
}
