package taskstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// The dedicated session does not bypass validation: it reads both journals,
// binds the policy and namespace, and leaves request ownership to the caller.
type bundleSession struct {
	*boardSession
	shared        *sharedSession
	transitions   transitionJournal
	journal       bundleJournal
	journalExists bool
	policy        boardpolicy.Policy
}

func openBundleSession(dir string) (_ *bundleSession, err error) {
	shared, release, err := acquireSharedForBundle(dir, false, true)
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
	s := &bundleSession{boardSession: &boardSession{root: r, unlock: unlock, releaseCommon: release}, shared: shared}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, err
	}
	if err := verifyBoardHandle(r, canonical); err != nil {
		return nil, err
	}
	if err := shared.verifyBoardIdentity(r); err != nil {
		return nil, err
	}
	if err := shared.verifyLocalIDBinding(r); err != nil {
		return nil, err
	}
	s.transitions, err = loadTransitionsForBundle(r)
	if err != nil {
		return nil, err
	}
	for _, record := range s.transitions.Records {
		if record.Kind == "pending" {
			return nil, errors.New("pending transition must be recovered before bundle work")
		}
	}
	s.policy, err = policyForJournal(r, s.transitions)
	if err != nil {
		return nil, err
	}
	// Even recovery must never recreate a missing ledger from the current cards.
	if _, err := loadIDs(r); err != nil {
		return nil, fmt.Errorf("bundle requires existing ID ledger: %w", err)
	}
	s.journal, err = loadBundles(r)
	if errors.Is(err, fs.ErrNotExist) {
		if s.transitions.BundleProtocol != 0 {
			return nil, errors.New("adopted bundle journal is missing; restore it")
		}
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			return nil, err
		}
		s.journal = bundleJournal{SchemaVersion: 1, BoardID: hex.EncodeToString(id[:]), Records: []bundleRecord{}}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	s.journalExists = true
	if s.transitions.BundleProtocol == 0 && len(s.journal.Records) != 0 {
		return nil, errors.New("nonempty orphan bundle journal; restore its protocol binding")
	}
	return s, nil
}

// adopt is called only after whole-request validation and journal capacity
// checks. Empty orphan journals can resume adoption; nonempty ones cannot.
func (s *bundleSession) adopt(explicit bool, step func(string) error) error {
	commonNeedsUpgrade := s.shared != nil && s.shared.state != nil && s.shared.state.BundleProtocol != 1
	if (!s.journalExists || s.transitions.BundleProtocol != 1 || commonNeedsUpgrade) && !explicit {
		return errors.New("bundle protocol upgrade requires explicit adoption; upgrade all writers first")
	}
	if !s.journalExists {
		if err := saveBundles(s.root, s.journal, true); err != nil {
			return err
		}
		s.journalExists = true
		if err := bundleStep(step, "after-empty-journal"); err != nil {
			return err
		}
	}
	if s.transitions.BundleProtocol != 1 {
		s.transitions.BundleProtocol = 1
		if err := publishTransitionJournal(s.root, s.transitions); err != nil {
			return err
		}
		if err := bundleStep(step, "after-local-protocol"); err != nil {
			return err
		}
	}
	if commonNeedsUpgrade {
		next := *s.shared.state
		next.SchemaVersion, next.BundleProtocol = 2, 1
		if err := s.shared.verify(); err != nil {
			return err
		}
		if err := publishSharedState(s.shared.root, next, false); err != nil {
			return err
		}
		s.shared.state = &next
		if err := bundleStep(step, "after-common-protocol"); err != nil {
			return err
		}
	}
	return nil
}

func bundleStep(step func(string) error, phase string) error {
	if step == nil {
		return nil
	}
	return step(phase)
}
