package taskstore

import (
	"errors"
	"io/fs"
)

type relocationSession struct {
	*repairSession
	moves      relocationJournal
	movesExist bool
}

func openRelocationSession(dir string, req RelocationRequest) (_ *relocationSession, err error) {
	base, err := openStorageSession(dir, req.repairIdentity(), true)
	if err != nil {
		return nil, err
	}
	s := &relocationSession{repairSession: base}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	s.moves, err = loadRelocationJournal(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		if s.transitions.StorageProtocol == 2 {
			return nil, errors.New("adopted relocation journal is missing; restore it")
		}
		s.moves = relocationJournal{SchemaVersion: 1, BoardPath: s.journal.BoardPath, Records: []relocationRecord{}}
		err = nil
	} else if err != nil {
		return nil, err
	} else {
		s.movesExist = true
	}
	if s.moves.BoardPath != s.journal.BoardPath {
		return nil, errors.New("relocation journal belongs to another board")
	}
	if s.transitions.StorageProtocol < 2 && len(s.moves.Records) != 0 {
		return nil, errors.New("relocation journal lost its protocol marker; restore it")
	}
	if s.shared != nil && s.shared.state != nil {
		if p := s.shared.state.PendingRelocation; p != nil {
			if p.Owner != s.moves.BoardPath || p.RequestID != req.RequestID || s.transitions.StorageProtocol != 2 || !s.movesExist {
				return nil, errors.New("shared relocation owner, request or local binding mismatch")
			}
		}
	}
	return s, nil
}

func (s *relocationSession) adoptRelocation(explicit bool, step func(string) error) error {
	commonNeeded := s.shared != nil && s.shared.state != nil && s.shared.state.StorageProtocol != 2
	if (!s.movesExist || s.transitions.StorageProtocol != 2 || commonNeeded) && !explicit {
		return errors.New("relocation protocol adoption requires --adopt after upgrading all writers")
	}
	if err := s.repairSession.adopt(explicit, nil); err != nil {
		return err
	}
	if !s.movesExist {
		if err := saveRelocationJournal(s.root, s.moves, true); err != nil {
			return err
		}
		s.movesExist = true
	}
	if err := storageStep(step, "after-relocation-empty-journal"); err != nil {
		return err
	}
	if commonNeeded {
		next := *s.shared.state
		next.StorageProtocol = 2
		if err := s.shared.saveStorageState(next); err != nil {
			return err
		}
	}
	if err := storageStep(step, "after-relocation-common-protocol"); err != nil {
		return err
	}
	if s.transitions.StorageProtocol != 2 {
		s.transitions.StorageProtocol = 2
		if err := publishTransitionJournal(s.root, s.transitions); err != nil {
			return err
		}
	}
	return storageStep(step, "after-relocation-local-protocol")
}

func (s *relocationSession) reserveRelocation(target relocationJournal, req RelocationRequest) error {
	if s.shared == nil || s.shared.state == nil {
		return nil
	}
	original, err := relocationJournalBytes(s.moves)
	if err != nil {
		return err
	}
	raw, err := relocationJournalBytes(target)
	if err != nil {
		return err
	}
	next := *s.shared.state
	if next.PendingRelocation != nil {
		return errors.New("shared relocation already reserved")
	}
	next.PendingRelocation = &sharedRepairPending{Owner: s.moves.BoardPath, RequestID: req.RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: raw}
	return s.shared.saveStorageState(next)
}

func (s *relocationSession) resumeRelocation(req RelocationRequest) error {
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingRelocation == nil {
		return nil
	}
	p := s.shared.state.PendingRelocation
	target, err := decodeRelocationJournal(p.TargetJournal)
	if err != nil {
		return err
	}
	matched := false
	for _, rec := range target.Records {
		if rec.Kind == "pending" {
			matched = rec.RequestID == req.RequestID && sameRelocation(rec, req)
		}
	}
	if !matched {
		return errors.New("shared relocation request differs from recorded request")
	}
	raw, err := relocationJournalBytes(s.moves)
	if err != nil {
		return err
	}
	done, err := relocationJournalBytes(completedRelocationJournal(target))
	if err != nil {
		return err
	}
	if bytesDigest(raw) == bytesDigest(p.TargetJournal) || bytesDigest(raw) == bytesDigest(done) {
		return nil
	}
	if bytesDigest(raw) != p.OriginalJournalSHA256 {
		return errors.New("local relocation journal differs from shared reservation")
	}
	if err := saveRelocationJournal(s.root, target, false); err != nil {
		return err
	}
	s.moves = target
	return nil
}

func (s *relocationSession) clearRelocation(req RelocationRequest) error {
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingRelocation == nil {
		return nil
	}
	next := *s.shared.state
	if next.PendingRelocation.RequestID != req.RequestID || next.PendingRelocation.Owner != s.moves.BoardPath {
		return errors.New("shared relocation owner mismatch")
	}
	next.PendingRelocation = nil
	return s.shared.saveStorageState(next)
}
