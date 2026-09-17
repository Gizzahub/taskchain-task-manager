package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
)

func (s *archiveSession) verifyPendingArchive(rec archiveRecord, req ArchiveRequest) error {
	if err := s.shared.verifyBoardIdentity(s.root); err != nil {
		return err
	}
	if err := s.shared.verifyLocalIDBinding(s.root); err != nil {
		return err
	}
	board, err := canonicalStorageBoard(s.root)
	if err != nil {
		return err
	}
	if board != rec.BoardPath || !sameArchive(rec, req) {
		return errors.New("archive board or request mismatch")
	}
	namespace := ""
	if s.shared != nil && s.shared.state != nil {
		namespace = s.shared.state.NamespaceID
		p := s.shared.state.PendingArchive
		if p == nil || p.Owner != board || p.RequestID != req.RequestID {
			return errors.New("pending archive lost common reservation")
		}
	}
	if rec.Namespace != namespace {
		return errors.New("archive namespace changed")
	}
	j, err := loadTransitionsForStorage(s.root)
	if err != nil {
		return err
	}
	if j.StorageProtocol != 3 {
		return errors.New("pending archive lost protocol binding")
	}
	if err := s.shared.verifyPolicyAuthority(s.root, j); err != nil {
		return err
	}
	if err := checkBundleGate(s.root, j); err != nil {
		return err
	}
	if err := checkRelocationGate(s.root, j); err != nil {
		return err
	}
	for _, other := range j.Records {
		if other.Kind == "pending" {
			return errors.New("pending transition conflicts with archive")
		}
	}
	repairs, err := loadRepairJournal(s.root)
	if err != nil {
		return err
	}
	if repairs.BoardPath != board {
		return errors.New("repair journal board changed")
	}
	for _, other := range repairs.Records {
		if other.Kind == "pending" {
			return errors.New("pending repair conflicts with archive")
		}
	}
	policy, err := policyForJournal(s.root, j)
	if err != nil {
		return err
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, rec.PolicyCanonical) || bytesDigest(canonical) != rec.PolicyDigest {
		return errors.New("archive policy changed")
	}
	current, err := loadArchiveJournal(s.root)
	if err != nil {
		return err
	}
	got, err := archiveJournalBytes(current)
	if err != nil {
		return err
	}
	want, err := archiveJournalBytes(s.archives)
	if err != nil {
		return err
	}
	if !bytes.Equal(got, want) {
		return errors.New("pending archive journal changed")
	}
	skip := ""
	if _, err := s.root.Lstat(rec.Target); err == nil {
		skip = rec.Source
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := listLockedWithPolicy(s.root, skip, policy)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if sameIdentity(entry.Card.ID, rec.ID) {
			if entry.Card.ID != rec.ID || (entry.Path != rec.Source && entry.Path != rec.Target) {
				return errors.New("archive identity appeared at another path")
			}
			found = true
		}
	}
	if !found {
		return errors.New("archive source and target disappeared")
	}
	completion, err := s.archiveAdmissionIndex(entries, req)
	if err != nil {
		return err
	}
	// Re-evaluate references against current board state, not a frozen success
	// boolean. The exact source bytes and rules stay bound by the pending record.
	prepared, err := prepareArchiveRecord(req, rec.Original, rec.Mode, policy, board, namespace, func(id string) (bool, error) { return completion.done(id), nil })
	if err != nil {
		return err
	}
	if prepared.FinalSHA256 != rec.FinalSHA256 || prepared.Target != rec.Target {
		return errors.New("archive preparation changed during recovery")
	}
	return nil
}
