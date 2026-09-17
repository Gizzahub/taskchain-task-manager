package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
)

func (s *relocationSession) verifyPendingRelocation(rec relocationRecord, req RelocationRequest) error {
	if err := s.shared.verifyBoardIdentity(s.root); err != nil {
		return err
	}
	board, err := canonicalStorageBoard(s.root)
	if err != nil {
		return err
	}
	if board != rec.BoardPath || !sameRelocation(rec, req) {
		return errors.New("pending relocation board or request mismatch")
	}
	namespace := ""
	if s.shared != nil && s.shared.state != nil {
		namespace = s.shared.state.NamespaceID
		p := s.shared.state.PendingRelocation
		if p == nil || p.RequestID != req.RequestID || p.Owner != board {
			return errors.New("pending relocation lost common reservation")
		}
	}
	if rec.Namespace != namespace {
		return errors.New("relocation namespace changed")
	}
	j, err := loadTransitionsForStorage(s.root)
	if err != nil {
		return err
	}
	if j.StorageProtocol < 2 || j.StorageProtocol > 5 {
		return errors.New("pending relocation lost protocol binding")
	}
	if err := checkArchiveGate(s.root, j); err != nil {
		return err
	}
	if err := checkBundleGate(s.root, j); err != nil {
		return err
	}
	for _, other := range j.Records {
		if other.Kind == "pending" {
			return errors.New("pending transition conflicts with relocation")
		}
	}
	if err := s.shared.verifyPolicyAuthority(s.root, j); err != nil {
		return err
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
		return errors.New("relocation policy changed")
	}
	repairs, err := loadRepairJournal(s.root)
	if err != nil {
		return err
	}
	if repairs.BoardPath != board {
		return errors.New("repair journal binding changed during relocation")
	}
	for _, other := range repairs.Records {
		if other.Kind == "pending" {
			return errors.New("pending repair conflicts with relocation")
		}
	}
	moves, err := loadRelocationJournal(s.root)
	if err != nil {
		return err
	}
	raw, err := relocationJournalBytes(moves)
	if err != nil {
		return err
	}
	want, err := relocationJournalBytes(s.moves)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, want) {
		return errors.New("pending relocation journal changed")
	}
	skip := ""
	if _, err := s.root.Lstat(req.Target); err == nil {
		skip = req.Source
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := listLockedWithPolicy(s.root, skip, policy)
	if err != nil {
		return err
	}
	found := false
	for _, entry := range entries {
		if sameIdentity(entry.Card.ID, req.ID) {
			if entry.Path != req.Source && entry.Path != req.Target {
				return errors.New("relocation identity appeared at another path")
			}
			found = true
		}
	}
	if !found {
		return errors.New("relocation card disappeared")
	}
	return s.validateRelocationAdmission(entries, req)
}
