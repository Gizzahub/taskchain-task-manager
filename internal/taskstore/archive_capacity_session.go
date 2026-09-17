package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

type archiveCapacitySession struct {
	*boardSession
	shared      *sharedSession
	transitions transitionJournal
	board       string
	namespace   string
	journal     archiveJournal
	raw         []byte
	mode        uint32
	adoption    *archiveCapacityAdoption
	original    []byte
	target      []byte
}

// Capacity recovery deliberately does not open a normal archive session. It
// binds the local authority and exact raw state before protocol-selected decode.
func openArchiveCapacitySession(dir, upgradeID string) (_ *archiveCapacitySession, err error) {
	shared, release, err := acquireSharedCapacityOptions(dir, false, false, false, false, false, false, true)
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
	s := &archiveCapacitySession{boardSession: &boardSession{root: r, unlock: unlock, releaseCommon: release}, shared: shared}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.close())
		}
	}()
	if err := shared.verifyBoardIdentity(r); err != nil {
		return nil, err
	}
	s.transitions, err = loadTransitionsForStorage(r)
	if err != nil {
		return nil, err
	}
	for _, rec := range s.transitions.Records {
		if rec.Kind == "pending" {
			return nil, errors.New("recover pending transition before capacity adoption")
		}
	}
	s.board, err = canonicalStorageBoard(r)
	if err != nil {
		return nil, err
	}
	ids, err := loadIDs(r)
	if err != nil {
		return nil, err
	}
	if shared != nil && shared.state != nil {
		s.namespace = shared.state.NamespaceID
		if ids.SchemaVersion != 3 || ids.Namespace != s.namespace {
			return nil, errors.New("capacity adoption local shared namespace binding mismatch")
		}
	} else if ids.SchemaVersion == 3 {
		return nil, errors.New("capacity adoption shared local binding has no common state; restore common state")
	}
	if err := capacityStorageIdle(r, s.transitions, s.board); err != nil {
		return nil, err
	}
	a, loadErr := loadArchiveCapacityAdoption(r)
	if errors.Is(loadErr, fs.ErrNotExist) {
		return openNewArchiveCapacitySession(s)
	}
	if loadErr != nil {
		return nil, loadErr
	}
	if a.UpgradeID != upgradeID {
		return nil, errors.New("archive capacity adoption belongs to another upgrade")
	}
	s.adoption = &a
	s.original, s.target, s.mode, err = loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil {
		return nil, errors.New("archive capacity payload missing or invalid; restore it")
	}
	if err := validateCapacityPayloadBinding(a, s.original, s.target, s.mode); err != nil {
		return nil, err
	}
	return openRecoveringArchiveCapacitySession(s)
}

func openNewArchiveCapacitySession(s *archiveCapacitySession) (*archiveCapacitySession, error) {
	if s.transitions.StorageProtocol != 4 {
		return nil, errors.New("archive capacity adoption requires protocol 4 or its existing recovery journal")
	}
	if s.shared != nil && s.shared.state != nil {
		state := s.shared.state
		if state.StorageProtocol != 4 || state.PendingArchive != nil || state.PendingArchiveDelta != nil || state.PendingArchiveCapacity != nil {
			return nil, errors.New("shared archive capacity adoption requires an idle protocol 4 common state")
		}
	}
	info, err := s.root.Lstat(archivesFile)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() == 0 {
		return nil, errors.New("archive journal type or mode invalid")
	}
	s.mode = uint32(info.Mode().Perm())
	s.raw, err = boundedSnapshotFile(s.root, archivesFile, maxRepairsBytes)
	if err != nil {
		return nil, err
	}
	s.journal, err = decodeArchiveJournal(s.raw)
	if err != nil {
		return nil, err
	}
	if err := validateCapacityScope(s); err != nil {
		return nil, err
	}
	return s, nil
}

func openRecoveringArchiveCapacitySession(s *archiveCapacitySession) (*archiveCapacitySession, error) {
	info, err := s.root.Lstat(archivesFile)
	if err != nil {
		return nil, errors.New("archive capacity journal missing; restore it")
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != s.mode {
		return nil, errors.New("archive capacity journal type or mode differs; restore it")
	}
	s.raw, err = boundedSnapshotFile(s.root, archivesFile, maxArchiveCapacityBytes)
	if err != nil {
		return nil, errors.New("archive capacity journal missing; restore it")
	}
	switch {
	case bytes.Equal(s.raw, s.original):
		s.journal, err = decodeArchiveJournal(s.raw)
	case bytes.Equal(s.raw, s.target):
		s.journal, err = decodeArchiveCapacityJournal(s.raw)
	default:
		return nil, errors.New("archive capacity journal is a third state; restore original or target")
	}
	if err != nil {
		return nil, err
	}
	if err := validateCapacityScope(s); err != nil {
		return nil, err
	}
	if s.transitions.StorageProtocol != 4 && s.transitions.StorageProtocol != 5 {
		return nil, errors.New("archive capacity adoption protocol barrier differs")
	}
	if s.shared != nil && s.shared.state != nil {
		state := s.shared.state
		if state.StorageProtocol != 4 && state.StorageProtocol != 5 {
			return nil, errors.New("archive capacity common protocol barrier differs")
		}
		if p := state.PendingArchiveCapacity; p != nil && (p.Owner != s.board || p.UpgradeID != s.adoption.UpgradeID) {
			return nil, errors.New("shared archive capacity marker belongs to another adoption")
		}
	}
	return s, nil
}

func validateCapacityScope(s *archiveCapacitySession) error {
	if s.journal.BoardPath != s.board || s.journal.Namespace != s.namespace {
		return errors.New("archive capacity adoption scope differs from board")
	}
	if s.adoption != nil && (s.adoption.BoardPath != s.board || s.adoption.Namespace != s.namespace) {
		return errors.New("archive capacity adoption journal scope differs from board")
	}
	for _, rec := range s.journal.Records {
		if rec.State != "completed" {
			return errors.New("recover pending archive before capacity adoption")
		}
	}
	return nil
}

func capacityStorageIdle(r *os.Root, transitions transitionJournal, board string) error {
	repairs, err := loadRepairJournal(r)
	if err != nil {
		return err
	}
	if repairs.BoardPath != board {
		return errors.New("repair journal belongs to another board")
	}
	for _, rec := range repairs.Records {
		if rec.Kind == "pending" {
			return errors.New("recover pending repair before capacity adoption")
		}
	}
	moves, err := loadRelocationJournal(r)
	if err != nil {
		return err
	}
	if moves.BoardPath != board {
		return errors.New("relocation journal belongs to another board")
	}
	for _, rec := range moves.Records {
		if rec.Kind == "pending" {
			return errors.New("recover pending relocation before capacity adoption")
		}
	}
	return nil
}

func validateCapacityPayloadBinding(a archiveCapacityAdoption, original, target []byte, mode uint32) error {
	if len(original) != a.OriginalLength || len(target) != a.TargetLength || bytesDigest(original) != a.OriginalSHA256 || bytesDigest(target) != a.TargetSHA256 || mode != a.JournalMode {
		return errors.New("archive capacity payload differs from adoption journal")
	}
	return nil
}
