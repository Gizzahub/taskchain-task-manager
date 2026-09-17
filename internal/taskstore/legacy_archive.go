package taskstore

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func AdoptLegacyArchive(dir string, req LegacyArchiveRequest, adopt bool) (ArchiveResult, error) {
	return legacyArchiveWithStep(dir, req, adopt, false, nil)
}

func RecoverLegacyArchive(dir string, req LegacyArchiveRequest) (ArchiveResult, error) {
	return legacyArchiveWithStep(dir, req, false, true, nil)
}

func legacyArchiveWithStep(dir string, req LegacyArchiveRequest, adopt, recoverOnly bool, step func(string) error) (result ArchiveResult, err error) {
	if req.Operation != "legacy-adoption" {
		return result, fmt.Errorf("legacy archive operation is required")
	}
	common := req.ArchiveRequest
	common.Operation = "force"
	if err := validateArchiveRequest(common); err != nil {
		return result, err
	}
	if req.ExpectedMode == 0 || req.ExpectedMode&^uint32(0777) != 0 {
		return result, errors.New("invalid legacy archive expected mode")
	}
	s, err := openArchiveSession(dir, req.ArchiveRequest)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	if err := s.verifyLegacyPending(req); err != nil {
		return result, err
	}
	if err := s.resumeArchive(req.ArchiveRequest); err != nil {
		return result, err
	}
	for _, rec := range s.archives.Records {
		if rec.RequestID != req.RequestID {
			continue
		}
		if !sameLegacyArchive(rec, req) {
			return result, errors.New("request ID already used by different legacy archive")
		}
		if _, err := s.prepareLegacy(req, rec); err != nil {
			return result, err
		}
		if rec.State == "completed" {
			if err := s.clearArchive(req.ArchiveRequest); err != nil {
				return result, err
			}
			return archiveResult(rec), nil
		}
		return s.finishLegacy(rec, req, step)
	}
	if recoverOnly {
		return result, errors.New("matching legacy archive not found; recovery never starts an operation")
	}
	rec, err := s.prepareLegacy(req, archiveRecord{})
	if err != nil {
		return result, err
	}
	next := s.archives
	next.Records = append(append([]archiveRecord{}, next.Records...), rec)
	if _, err := archiveJournalBytes(next); err != nil {
		return result, err
	}
	if err := s.adoptArchive(adopt, step); err != nil {
		return result, err
	}
	if err := s.reserveArchive(next, req.ArchiveRequest); err != nil {
		return result, err
	}
	if err := storageStep(step, "after-archive-common-pending"); err != nil {
		return result, err
	}
	if err := saveArchiveJournal(s.root, next, false); err != nil {
		return result, err
	}
	s.archives = next
	if err := storageStep(step, "after-archive-journal"); err != nil {
		return result, err
	}
	return s.finishLegacy(rec, req, step)
}

func (s *archiveSession) prepareLegacy(req LegacyArchiveRequest, existing archiveRecord) (archiveRecord, error) {
	if err := relocationParents(s.root, req.Source, false); err != nil {
		return archiveRecord{}, err
	}
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return archiveRecord{}, err
	}
	found := false
	for _, entry := range entries {
		if entry.Path == req.Source && entry.Card.ID == req.ID {
			found = true
			break
		}
	}
	if !found {
		return archiveRecord{}, errors.New("legacy archive source is not the requested discovered card")
	}
	if existing.State != "completed" {
		if _, err := s.archiveAdmissionIndex(entries, req.ArchiveRequest); err != nil {
			return archiveRecord{}, err
		}
	}
	info, err := s.root.Lstat(req.Source)
	if err != nil {
		return archiveRecord{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return archiveRecord{}, errors.New("legacy archive source is not regular")
	}
	raw, err := readTransitionCard(s.root, req.Source)
	if err != nil {
		return archiveRecord{}, err
	}
	policy := s.policy
	if existing.State == "completed" {
		policy, err = boardpolicy.Parse(existing.PolicyCanonical)
		if err != nil {
			return archiveRecord{}, fmt.Errorf("parse historical archive policy: %w", err)
		}
	}
	rec, err := prepareLegacyArchiveRecord(req, raw, uint32(info.Mode().Perm()), policy, s.archives.BoardPath, s.archives.Namespace)
	if err != nil {
		return archiveRecord{}, err
	}
	if existing.State != "" {
		if existing.Source != rec.Source || existing.Target != rec.Target || existing.Mode != rec.Mode || existing.OriginalSHA256 != rec.OriginalSHA256 || existing.FinalSHA256 != rec.FinalSHA256 || existing.PolicyDigest != rec.PolicyDigest || existing.RulesDigest != rec.RulesDigest || (existing.Completion != nil) != (rec.Completion != nil) {
			return archiveRecord{}, errors.New("legacy archive current card differs from recorded request")
		}
	}
	return rec, nil
}

func (s *archiveSession) verifyLegacyPending(req LegacyArchiveRequest) error {
	for _, rec := range s.archives.Records {
		if rec.State == "pending" {
			if !sameLegacyArchive(rec, req) {
				return errors.New("pending legacy archive requires its exact original request")
			}
			if _, err := s.prepareLegacy(req, rec); err != nil {
				return err
			}
		}
	}
	if s.shared == nil || s.shared.state == nil || s.shared.state.PendingArchive == nil {
		return nil
	}
	target, err := decodeArchiveJournal(s.shared.state.PendingArchive.TargetJournal)
	if err != nil {
		return err
	}
	matched := false
	for _, rec := range target.Records {
		if rec.State == "pending" {
			if !sameLegacyArchive(rec, req) {
				return errors.New("shared pending legacy archive request differs")
			}
			if _, err := s.prepareLegacy(req, rec); err != nil {
				return err
			}
			matched = true
		}
	}
	if !matched {
		return errors.New("shared pending archive has no legacy request")
	}
	return nil
}

func (s *archiveSession) finishLegacy(rec archiveRecord, req LegacyArchiveRequest, step func(string) error) (ArchiveResult, error) {
	if err := s.verifyPendingLegacy(rec, req); err != nil {
		return ArchiveResult{}, err
	}
	completed := completedArchiveJournal(s.archives)
	if err := saveArchiveJournal(s.root, completed, false); err != nil {
		return ArchiveResult{}, err
	}
	s.archives = completed
	if err := storageStep(step, "after-archive-receipt"); err != nil {
		return ArchiveResult{}, err
	}
	if err := s.clearArchive(req.ArchiveRequest); err != nil {
		return ArchiveResult{}, err
	}
	completedRecord := rec
	completedRecord.State, completedRecord.Original, completedRecord.Patched = "completed", nil, nil
	return archiveResult(completedRecord), nil
}

func (s *archiveSession) verifyPendingLegacy(rec archiveRecord, req LegacyArchiveRequest) error {
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
	if board != rec.BoardPath || !sameLegacyArchive(rec, req) || rec.State != "pending" {
		return errors.New("pending legacy archive board or request mismatch")
	}
	namespace := ""
	if s.shared != nil && s.shared.state != nil {
		namespace = s.shared.state.NamespaceID
		p := s.shared.state.PendingArchive
		if p == nil || p.Owner != board || p.RequestID != req.RequestID {
			return errors.New("pending legacy archive lost common reservation")
		}
	}
	if rec.Namespace != namespace {
		return errors.New("pending legacy archive namespace changed")
	}
	j, err := loadTransitionsForStorage(s.root)
	if err != nil {
		return err
	}
	if j.StorageProtocol != 3 {
		return errors.New("pending legacy archive lost protocol binding")
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
			return errors.New("pending transition conflicts with legacy archive")
		}
	}
	repairs, err := loadRepairJournal(s.root)
	if err != nil {
		return err
	}
	if repairs.BoardPath != board {
		return errors.New("legacy archive repair journal board changed")
	}
	for _, other := range repairs.Records {
		if other.Kind == "pending" {
			return errors.New("pending repair conflicts with legacy archive")
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
		return errors.New("pending legacy archive policy changed")
	}
	currentJournal, err := loadArchiveJournal(s.root)
	if err != nil {
		return err
	}
	currentRaw, err := archiveJournalBytes(currentJournal)
	if err != nil {
		return err
	}
	wantRaw, err := archiveJournalBytes(s.archives)
	if err != nil || !bytes.Equal(currentRaw, wantRaw) {
		return errors.New("pending legacy archive journal changed")
	}
	current, err := s.prepareLegacy(req, rec)
	if err != nil {
		return err
	}
	if !bytes.Equal(current.Original, rec.Original) || current.Mode != rec.Mode {
		return errors.New("legacy archive current card changed")
	}
	return nil
}
