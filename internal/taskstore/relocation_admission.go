package taskstore

import (
	"errors"
	"io/fs"
)

func (s *relocationSession) prepareRelocation(req RelocationRequest) (relocationRecord, error) {
	var zero relocationRecord
	if _, _, err := relocationZones(req.Source, req.Target, s.policy); err != nil {
		return zero, err
	}
	if err := relocationParents(s.root, req.Source, false); err != nil {
		return zero, err
	}
	if err := relocationParents(s.root, req.Target, false); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return zero, err
	}
	if _, err := s.root.Lstat(req.Target); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return zero, err
		}
		return zero, errors.New("relocation target already exists")
	}
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return zero, err
	}
	found := false
	for _, entry := range entries {
		if entry.Path == req.Source && sameIdentity(entry.Card.ID, req.ID) {
			found = true
		}
	}
	if !found {
		return zero, errors.New("relocation source is not the requested discovered card")
	}
	if err := s.validateRelocationAdmission(entries, req); err != nil {
		return zero, err
	}
	info, err := s.root.Lstat(req.Source)
	if err != nil {
		return zero, err
	}
	if !info.Mode().IsRegular() {
		return zero, errors.New("relocation source is not regular")
	}
	raw, err := readTransitionCard(s.root, req.Source)
	if err != nil {
		return zero, err
	}
	if bytesDigest(raw) != req.ExpectedSHA256 {
		return zero, errors.New("relocation source digest mismatch")
	}
	patched, changed, err := prepareRelocationPatch(raw, req.ID, req.Source, req.Target, s.policy)
	if err != nil {
		return zero, err
	}
	canonical, err := s.policy.Canonical()
	if err != nil {
		return zero, err
	}
	rec := relocationRecord{Kind: "pending", RequestID: req.RequestID, ID: req.ID, Owner: req.Owner, Token: req.Token, Source: req.Source, Target: req.Target, ExpectedSHA256: req.ExpectedSHA256, Mode: uint32(info.Mode().Perm()), Original: raw, Patched: patched, PolicyDigest: bytesDigest(canonical), PolicyCanonical: canonical, BoardPath: s.moves.BoardPath, Changed: changed}
	if s.shared != nil && s.shared.state != nil {
		rec.Namespace = s.shared.state.NamespaceID
	}
	return rec, validateRelocationRecord(rec)
}

func (s *relocationSession) validateRelocationAdmission(entries []Entry, req RelocationRequest) error {
	completion, err := completionForJournal(entries, s.policy, nil, "", "", nil)
	if err != nil {
		return err
	}
	ledger, err := loadIDs(s.root)
	if err != nil {
		return err
	}
	reserved := false
	for _, id := range ledger.Reserved {
		if sameIdentity(id, req.ID) {
			reserved = true
		}
	}
	if !reserved {
		return errors.New("relocation card must have a permanent ID reservation")
	}
	claims, err := loadClaims(s.root, entries)
	if err != nil {
		return err
	}
	if err := validateRepairClaim(claims, req.repairIdentity()); err != nil {
		return err
	}
	_, to, err := relocationZones(req.Source, req.Target, s.policy)
	if err != nil {
		return err
	}
	if isWorkTask(req.ID) && (to == "doing" || to == s.policy.DoneZone()) {
		if req.Token == "" {
			return errors.New("execution relocation requires an existing held claim")
		}
		for _, entry := range entries {
			if !sameIdentity(entry.Card.ID, req.ID) {
				continue
			}
			for _, dep := range entry.Card.DependsOn {
				if !completion.done(dep) {
					return errors.New("relocation dependency is not done")
				}
			}
		}
	}
	return nil
}
