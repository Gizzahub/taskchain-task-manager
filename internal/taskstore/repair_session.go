package taskstore

import (
	"encoding/json"
	"errors"
	"io/fs"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type repairSession struct {
	*boardSession
	shared        *sharedSession
	transitions   transitionJournal
	journal       repairJournal
	journalExists bool
	policy        boardpolicy.Policy
}

func openRepairSession(dir string, req RepairRequest) (_ *repairSession, err error) {
	shared, release, err := acquireSharedStorageOptions(dir, false, false, false, true)
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
	s := &repairSession{boardSession: &boardSession{root: r, unlock: unlock, releaseCommon: release}, shared: shared}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	if err := shared.verifyBoardIdentity(r); err != nil {
		return nil, err
	}
	if err := shared.verifyLocalIDBinding(r); err != nil {
		return nil, err
	}
	s.transitions, err = loadTransitionsForStorage(r)
	if err != nil {
		return nil, err
	}
	if err := shared.verifyPolicyAuthority(r, s.transitions); err != nil {
		return nil, err
	}
	if err := checkBundleGate(r, s.transitions); err != nil {
		return nil, err
	}
	for _, rec := range s.transitions.Records {
		if rec.Kind == "pending" {
			return nil, errors.New("recover pending transition before status repair")
		}
	}
	if _, err := loadIDs(r); err != nil {
		return nil, err
	}
	s.policy, err = policyForJournal(r, s.transitions)
	if err != nil {
		return nil, err
	}
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return nil, err
	}
	s.journal, err = loadRepairJournal(r)
	if errors.Is(err, fs.ErrNotExist) {
		if s.transitions.StorageProtocol != 0 {
			return nil, errors.New("adopted storage journal is missing; restore it")
		}
		s.journal = repairJournal{SchemaVersion: 1, BoardPath: board, Records: []repairRecord{}}
		err = nil
	} else if err != nil {
		return nil, err
	} else {
		s.journalExists = true
	}
	if s.journal.BoardPath != board {
		return nil, errors.New("storage journal belongs to another board")
	}
	if s.transitions.StorageProtocol == 0 && len(s.journal.Records) != 0 {
		return nil, errors.New("orphan storage journal; restore its protocol marker")
	}
	if shared != nil && shared.state != nil {
		if s.transitions.StorageProtocol == 1 && shared.state.StorageProtocol != 1 {
			return nil, errors.New("storage-adopted board has no common protocol; restore common state")
		}
		if p := shared.state.PendingRepair; p != nil {
			if p.Owner != board || p.RequestID != req.RequestID {
				return nil, errors.New("shared repair belongs to another request or board")
			}
			if s.transitions.StorageProtocol != 1 || !s.journalExists {
				return nil, errors.New("shared pending repair lost local protocol binding")
			}
		}
	}
	return s, nil
}

func (s *repairSession) adopt(explicit bool, step func(string) error) error {
	commonNeeded := s.shared != nil && s.shared.state != nil && s.shared.state.StorageProtocol != 1
	if (!s.journalExists || s.transitions.StorageProtocol != 1 || commonNeeded) && !explicit {
		return errors.New("storage protocol adoption requires --adopt after upgrading all writers")
	}
	if !s.journalExists {
		if err := saveRepairJournal(s.root, s.journal, true); err != nil {
			return err
		}
		s.journalExists = true
		if err := storageStep(step, "after-empty-journal"); err != nil {
			return err
		}
	}
	if err := s.shared.adoptStorageProtocol(); err != nil {
		return err
	}
	if err := storageStep(step, "after-common-protocol"); err != nil {
		return err
	}
	if s.transitions.StorageProtocol != 1 {
		s.transitions.StorageProtocol = 1
		if err := publishTransitionJournal(s.root, s.transitions); err != nil {
			return err
		}
	}
	return storageStep(step, "after-local-protocol")
}

func repairJournalBytes(j repairJournal) ([]byte, error) {
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := decodeRepairJournal(raw); err != nil {
		return nil, err
	}
	completed := completedRepairJournal(j)
	reserve, err := json.Marshal(completed)
	if err != nil {
		return nil, err
	}
	if len(reserve)+1 > maxRepairsBytes {
		return nil, errors.New("repair completion exceeds journal capacity")
	}
	return raw, nil
}

func completedRepairJournal(j repairJournal) repairJournal {
	j.Records = append([]repairRecord(nil), j.Records...)
	for i := range j.Records {
		if j.Records[i].Kind == "pending" {
			j.Records[i].Kind = "completed"
			j.Records[i].Original = nil
			j.Records[i].Patched = nil
		}
	}
	return j
}

func (s *repairSession) reserveCommon(target repairJournal, req RepairRequest) error {
	if s.shared == nil || s.shared.state == nil {
		return nil
	}
	original, err := repairJournalBytes(s.journal)
	if err != nil {
		return err
	}
	raw, err := repairJournalBytes(target)
	if err != nil {
		return err
	}
	next := *s.shared.state
	if next.PendingRepair != nil {
		return errors.New("common repair reservation already exists")
	}
	next.PendingRepair = &sharedRepairPending{Owner: s.journal.BoardPath, RequestID: req.RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: raw}
	return s.shared.saveStorageState(next)
}

func (s *repairSession) resumeCommon(req RepairRequest) error {
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingRepair == nil {
		return nil
	}
	p := s.shared.state.PendingRepair
	target, err := decodeRepairJournal(p.TargetJournal)
	if err != nil {
		return err
	}
	matched := false
	for _, rec := range target.Records {
		if rec.Kind == "pending" {
			matched = rec.RequestID == req.RequestID && sameRepair(rec, req)
		}
	}
	if !matched {
		return errors.New("shared repair request differs from recorded request")
	}
	raw, err := repairJournalBytes(s.journal)
	if err != nil {
		return err
	}
	done, err := repairJournalBytes(completedRepairJournal(target))
	if err != nil {
		return err
	}
	if bytesDigest(raw) == bytesDigest(p.TargetJournal) || bytesDigest(raw) == bytesDigest(done) {
		return nil
	}
	if bytesDigest(raw) != p.OriginalJournalSHA256 {
		return errors.New("local repair journal differs from common reservation; restore exact journal")
	}
	if err := saveRepairJournal(s.root, target, false); err != nil {
		return err
	}
	s.journal = target
	return nil
}

func (s *repairSession) clearCommon(req RepairRequest) error {
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingRepair == nil {
		return nil
	}
	next := *s.shared.state
	if next.PendingRepair.RequestID != req.RequestID || next.PendingRepair.Owner != s.journal.BoardPath {
		return errors.New("shared repair owner mismatch")
	}
	next.PendingRepair = nil
	return s.shared.saveStorageState(next)
}
