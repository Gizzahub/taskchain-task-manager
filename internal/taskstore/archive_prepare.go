package taskstore

import (
	"errors"
	"io/fs"
)

func (s *archiveSession) prepareArchive(req ArchiveRequest) (archiveRecord, error) {
	var zero archiveRecord
	_, target, err := archiveDestination(req.Source, s.policy)
	if err != nil {
		return zero, err
	}
	if err := relocationParents(s.root, req.Source, false); err != nil {
		return zero, err
	}
	if err := relocationParents(s.root, target, false); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return zero, err
	}
	if _, err := s.root.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return zero, err
		}
		return zero, errors.New("archive target already exists")
	}
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return zero, err
	}
	found := false
	for _, entry := range entries {
		if entry.Path == req.Source && entry.Card.ID == req.ID {
			found = true
		}
	}
	if !found {
		return zero, errors.New("archive source is not the requested discovered card")
	}
	completion, err := s.archiveAdmissionIndex(entries, req)
	if err != nil {
		return zero, err
	}
	info, err := s.root.Lstat(req.Source)
	if err != nil {
		return zero, err
	}
	if !info.Mode().IsRegular() {
		return zero, errors.New("archive source is not regular")
	}
	raw, err := readTransitionCard(s.root, req.Source)
	if err != nil {
		return zero, err
	}
	return prepareArchiveRecord(req, raw, uint32(info.Mode().Perm()), s.policy, s.archives.BoardPath, s.archives.Namespace, func(id string) (bool, error) { return completion.done(id), nil })
}

// The current operation's pending binding cannot count as completion. Keep
// other completed archive bindings, validating them against this same snapshot.
func (s *archiveSession) archiveAdmissionIndex(entries []Entry, req ArchiveRequest) (completionIndex, error) {
	base := s.archives
	base.Records = []archiveRecord{}
	for _, rec := range s.archives.Records {
		if rec.State == "completed" {
			base.Records = append(base.Records, rec)
		} else if !sameArchive(rec, req) {
			return nil, errors.New("archive pending request mismatch")
		}
	}
	completion, err := completionForJournal(entries, s.policy, &base, s.archives.BoardPath, s.archives.Namespace, func(entry Entry) ([]byte, error) { return readTransitionCard(s.root, entry.Path) })
	if err != nil {
		return nil, err
	}
	ids, err := loadIDs(s.root)
	if err != nil {
		return nil, err
	}
	reserved := false
	for _, id := range ids.Reserved {
		if sameIdentity(id, req.ID) {
			reserved = true
		}
	}
	if !reserved {
		return nil, errors.New("archive card must have a permanent ID reservation")
	}
	claims, err := loadClaims(s.root, entries)
	if err != nil {
		return nil, err
	}
	if err := validateRepairClaim(claims, RepairRequest{ID: req.ID, Owner: req.Owner, Token: req.Token}); err != nil {
		return nil, err
	}
	return completion, nil
}
