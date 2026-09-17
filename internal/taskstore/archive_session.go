package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
)

type archiveSession struct {
	*relocationSession
	archives      archiveJournal
	archivesExist bool
}

// Only the archive writer may bypass its own pending gate. The common lock
// still precedes the board lock, and other transaction families remain blocked.
func openArchiveSession(dir string, req ArchiveRequest) (_ *archiveSession, err error) {
	base, err := openStorageSessionOptions(dir, RepairRequest{RequestID: req.RequestID}, false, true)
	if err != nil {
		return nil, err
	}
	s := &archiveSession{relocationSession: &relocationSession{repairSession: base}}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	s.moves, err = loadRelocationJournal(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		if s.transitions.StorageProtocol >= 2 {
			return nil, errors.New("adopted relocation journal is missing; restore it")
		}
		s.moves = relocationJournal{SchemaVersion: 1, BoardPath: s.journal.BoardPath, Records: []relocationRecord{}}
	} else if err != nil {
		return nil, err
	} else {
		s.movesExist = true
	}
	if s.moves.BoardPath != s.journal.BoardPath {
		return nil, errors.New("relocation journal belongs to another board")
	}
	if s.transitions.StorageProtocol < 2 && len(s.moves.Records) != 0 {
		return nil, errors.New("relocation journal lost its protocol marker")
	}
	for _, record := range s.moves.Records {
		if record.Kind == "pending" {
			return nil, errors.New("recover pending relocation before archive")
		}
	}
	namespace := ""
	if s.shared != nil && s.shared.state != nil {
		namespace = s.shared.state.NamespaceID
	}
	s.archives, err = loadArchiveJournalForProtocol(s.root, s.transitions.StorageProtocol)
	if errors.Is(err, fs.ErrNotExist) {
		if s.transitions.StorageProtocol >= 3 {
			return nil, errors.New("adopted archive journal is missing; restore it")
		}
		s.archives = archiveJournal{SchemaVersion: 1, BoardPath: s.journal.BoardPath, Namespace: namespace, Records: []archiveRecord{}}
	} else if err != nil {
		return nil, err
	} else {
		s.archivesExist = true
	}
	if s.archives.BoardPath != s.journal.BoardPath || s.archives.Namespace != namespace {
		return nil, errors.New("archive journal scope differs from current session")
	}
	if adoption, loadErr := loadArchiveCapacityAdoption(s.root); loadErr == nil && adoption.Phase == "pending" {
		return nil, errors.New("pending archive capacity adoption requires exact recovery")
	} else if loadErr != nil && !errors.Is(loadErr, fs.ErrNotExist) {
		return nil, loadErr
	}
	if s.transitions.StorageProtocol >= 5 && s.archives.SchemaVersion != 2 {
		return nil, errors.New("capacity-adopted archive journal is not schema 2; restore it")
	}
	if s.transitions.StorageProtocol < 3 && len(s.archives.Records) != 0 {
		return nil, errors.New("archive journal lost its protocol marker")
	}
	for _, rec := range s.archives.Records {
		if rec.State == "pending" && (rec.RequestID != req.RequestID || !sameArchive(rec, req)) {
			return nil, errors.New("pending archive requires its exact original request")
		}
	}
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingArchive != nil {
		p := s.shared.state.PendingArchive
		if p.Owner != s.archives.BoardPath || p.RequestID != req.RequestID || s.transitions.StorageProtocol != 3 || !s.archivesExist {
			return nil, errors.New("shared archive owner, request or local protocol mismatch")
		}
	}
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingArchiveDelta != nil {
		if _, err := s.resolveSharedDelta(req); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *archiveSession) adoptArchive(explicit bool, step func(string) error) error {
	commonNeeded := s.shared != nil && s.shared.state != nil && s.shared.state.StorageProtocol < 4
	if (!s.archivesExist || s.transitions.StorageProtocol < 4 || commonNeeded) && !explicit {
		return errors.New("archive protocol adoption requires --adopt after upgrading all writers")
	}
	for _, rec := range s.archives.Records {
		if rec.State == "pending" {
			return errors.New("recover pending archive before protocol adoption")
		}
	}
	if s.shared != nil && s.shared.state != nil && (s.shared.state.PendingArchive != nil || s.shared.state.PendingArchiveDelta != nil) {
		return errors.New("recover shared archive before protocol adoption")
	}
	if err := s.relocationSession.adoptRelocation(explicit, step); err != nil {
		return err
	}
	if !s.archivesExist {
		if err := saveArchiveJournal(s.root, s.archives, true); err != nil {
			return err
		}
		s.archivesExist = true
	}
	if err := storageStep(step, "after-archive-empty-journal"); err != nil {
		return err
	}
	if commonNeeded {
		next := *s.shared.state
		next.StorageProtocol = 4
		if err := s.shared.saveStorageState(next); err != nil {
			return err
		}
	}
	if err := storageStep(step, "after-archive-common-protocol"); err != nil {
		return err
	}
	if s.transitions.StorageProtocol < 4 {
		s.transitions.StorageProtocol = 4
		if err := publishTransitionJournal(s.root, s.transitions); err != nil {
			return err
		}
	}
	return storageStep(step, "after-archive-local-protocol")
}

func completedArchiveJournal(j archiveJournal) archiveJournal {
	j.Records = append([]archiveRecord{}, j.Records...)
	for i := range j.Records {
		if j.Records[i].State == "pending" {
			j.Records[i].State, j.Records[i].Original, j.Records[i].Patched = "completed", nil, nil
		}
	}
	return j
}

func (s *archiveSession) reserveArchive(target archiveJournal, req ArchiveRequest) error {
	if s.shared == nil || s.shared.state == nil {
		return nil
	}
	if s.shared.state.StorageProtocol >= 4 {
		next, err := s.prepareSharedDelta(target)
		if err != nil {
			return err
		}
		return s.shared.saveStorageState(next)
	}
	if s.shared.state.PendingArchive != nil {
		return errors.New("shared archive already reserved")
	}
	original, err := archiveJournalWire(s.archives)
	if err != nil {
		return err
	}
	raw, err := archiveJournalWire(target)
	if err != nil {
		return err
	}
	next := *s.shared.state
	next.PendingArchive = &sharedRepairPending{Owner: s.archives.BoardPath, RequestID: req.RequestID, OriginalJournalSHA256: bytesDigest(original), TargetJournal: raw}
	return s.shared.saveStorageState(next)
}

func (s *archiveSession) resumeArchive(req ArchiveRequest) error {
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingArchiveDelta != nil {
		return s.resumeArchiveDelta(req)
	}
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingArchive == nil {
		return nil
	}
	p := s.shared.state.PendingArchive
	target, err := decodeArchiveJournalWire(p.TargetJournal, s.archives.SchemaVersion)
	if err != nil {
		return err
	}
	matched := false
	for _, rec := range target.Records {
		if rec.State == "pending" {
			matched = rec.RequestID == req.RequestID && sameArchive(rec, req)
		}
	}
	if !matched {
		return errors.New("shared archive request differs from recorded request")
	}
	raw, err := archiveJournalWire(s.archives)
	if err != nil {
		return err
	}
	done, err := archiveJournalWire(completedArchiveJournal(target))
	if err != nil {
		return err
	}
	if bytes.Equal(raw, p.TargetJournal) || bytes.Equal(raw, done) {
		return nil
	}
	if bytesDigest(raw) != p.OriginalJournalSHA256 {
		return errors.New("local archive journal differs from shared reservation")
	}
	if err := saveArchiveJournalForProtocol(s.root, target, false, s.transitions.StorageProtocol); err != nil {
		return err
	}
	s.archives = target
	return nil
}

func (s *archiveSession) clearArchive(req ArchiveRequest) error {
	if s.shared != nil && s.shared.state != nil && s.shared.state.PendingArchiveDelta != nil {
		return s.clearArchiveDelta(req)
	}
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingArchive == nil {
		return nil
	}
	next := *s.shared.state
	if next.PendingArchive.RequestID != req.RequestID || next.PendingArchive.Owner != s.archives.BoardPath {
		return errors.New("shared archive owner mismatch")
	}
	target, err := decodeArchiveJournalWire(next.PendingArchive.TargetJournal, s.archives.SchemaVersion)
	if err != nil {
		return err
	}
	matched := false
	for _, rec := range target.Records {
		if rec.State == "pending" {
			matched = sameArchive(rec, req)
		}
	}
	if !matched {
		return errors.New("archive completion request mismatch")
	}
	want, err := archiveJournalWire(completedArchiveJournal(target))
	if err != nil {
		return err
	}
	current, err := loadArchiveJournalForProtocol(s.root, s.transitions.StorageProtocol)
	if err != nil {
		return err
	}
	got, err := archiveJournalWire(current)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("archive completion is not durable; preserve shared reservation")
	}
	next.PendingArchive = nil
	return s.shared.saveStorageState(next)
}
