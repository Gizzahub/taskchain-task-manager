package taskstore

import (
	"encoding/json"
	"errors"
)

func validateSharedArchiveDelta(s sharedState) error {
	d := s.PendingArchiveDelta
	if d == nil {
		return nil
	}
	if s.StorageProtocol != 4 || s.Phase != "active" || s.PendingArchive != nil || s.PendingRepair != nil || s.PendingRelocation != nil || s.PendingBundle != nil || (s.Policy != nil && s.Policy.Phase != "active") {
		return errors.New("conflicting shared archive delta reservation")
	}
	rec, err := archiveDeltaRecord(*d)
	if err != nil {
		return err
	}
	if d.Namespace != s.NamespaceID {
		return errors.New("shared archive delta namespace mismatch")
	}
	for _, id := range s.Reserved {
		if sameIdentity(id, rec.ID) {
			return nil
		}
	}
	return errors.New("shared archive delta ID is not reserved")
}

// Prepare the complete envelope before adoption publishes any protocol marker.
func (s *archiveSession) prepareSharedDelta(target archiveJournal) (sharedState, error) {
	var zero sharedState
	if len(target.Records) != len(s.archives.Records)+1 {
		return zero, errors.New("archive delta target must append one record")
	}
	d, err := prepareArchivePendingDelta(s.archives, target.Records[len(target.Records)-1])
	if err != nil {
		return zero, err
	}
	raw, err := archiveJournalBytes(target)
	if err != nil {
		return zero, err
	}
	if bytesDigest(raw) != d.TargetJournalSHA256 {
		return zero, errors.New("archive delta target changed history")
	}
	next := *s.shared.state
	if next.PendingArchive != nil || next.PendingArchiveDelta != nil {
		return zero, errors.New("shared archive already reserved")
	}
	next.StorageProtocol, next.PendingArchiveDelta = 4, &d
	if err := validateSharedState(next); err != nil {
		return zero, err
	}
	wire, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return zero, err
	}
	if len(wire)+1 > maxSharedStateBytes {
		return zero, errors.New("shared archive delta envelope exceeds 16 MiB")
	}
	return next, nil
}

func (s *archiveSession) preflightArchiveDelta(target archiveJournal) error {
	if s.shared == nil || s.shared.state == nil {
		return nil
	}
	_, err := s.prepareSharedDelta(target)
	return err
}

func (s *archiveSession) resolveSharedDelta(req ArchiveRequest) (archiveDeltaResolution, error) {
	d := s.shared.state.PendingArchiveDelta
	if d == nil || d.RequestID != req.RequestID || d.BoardPath != s.archives.BoardPath || s.transitions.StorageProtocol != 4 || !s.archivesExist {
		return archiveDeltaResolution{}, errors.New("shared archive delta owner, request or local protocol mismatch")
	}
	current, err := loadArchiveJournal(s.root)
	if err != nil {
		return archiveDeltaResolution{}, err
	}
	raw, err := archiveJournalBytes(current)
	if err != nil {
		return archiveDeltaResolution{}, err
	}
	r, err := resolveArchivePendingDelta(*d, raw, s.archives.BoardPath, s.shared.state.NamespaceID, s.shared.state.Reserved)
	if err != nil {
		return r, err
	}
	if !sameArchive(r.Pending, req) {
		return archiveDeltaResolution{}, errors.New("shared archive delta request differs from recorded request")
	}
	return r, nil
}

func (s *archiveSession) resumeArchiveDelta(req ArchiveRequest) error {
	r, err := s.resolveSharedDelta(req)
	if err != nil {
		return err
	}
	if r.State != "original" {
		return nil
	}
	target, err := decodeArchiveJournal(r.Target)
	if err != nil {
		return err
	}
	if err := saveArchiveJournal(s.root, target, false); err != nil {
		return err
	}
	s.archives = target
	return nil
}

func (s *archiveSession) clearArchiveDelta(req ArchiveRequest) error {
	r, err := s.resolveSharedDelta(req)
	if err != nil {
		return err
	}
	if r.State != "completed" {
		return errors.New("archive completion is not durable; preserve shared delta reservation")
	}
	next := *s.shared.state
	next.PendingArchiveDelta = nil
	return s.shared.saveStorageState(next)
}

func (s *archiveSession) verifyArchiveReservation(req ArchiveRequest, board string) error {
	if s.shared == nil || s.shared.state == nil {
		return nil
	}
	if s.shared.state.PendingArchiveDelta != nil {
		_, err := s.resolveSharedDelta(req)
		return err
	}
	p := s.shared.state.PendingArchive
	if p == nil || p.Owner != board || p.RequestID != req.RequestID {
		return errors.New("pending archive lost common reservation")
	}
	return nil
}
