package taskstore

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
)

const archivesFile = ".task-manager-archives.json"

// Callers hold the board/common locks. Loading does not authorize protocol
// adoption or expose completion bindings; those require session scope checks.
func loadArchiveJournal(r *os.Root) (archiveJournal, error) {
	return loadArchiveJournalForProtocol(r, 4)
}

func loadArchiveJournalForProtocol(r *os.Root, protocol int) (archiveJournal, error) {
	limit := maxRepairsBytes
	if protocol >= 5 {
		limit = maxArchiveCapacityBytes
	}
	raw, err := boundedSnapshotFile(r, archivesFile, limit)
	if err != nil {
		return archiveJournal{}, err
	}
	if protocol >= 5 {
		return decodeArchiveCapacityJournal(raw)
	}
	return decodeArchiveJournal(raw)
}

// Identity checks run during policy recovery and therefore cannot select the
// archive decoder through the policy-aware transition loader. A pending
// capacity adoption may contain either its exact schema-1 original or exact
// schema-2 target, so resolve that state from the immutable payload authority.
func loadArchiveJournalForIdentityBinding(r *os.Root) (archiveJournal, error) {
	a, err := loadArchiveCapacityAdoption(r)
	if errors.Is(err, fs.ErrNotExist) {
		return loadArchiveJournalForProtocol(r, 4)
	}
	if err != nil {
		return archiveJournal{}, err
	}
	if a.Phase == "completed" {
		if _, _, _, err := loadArchiveCapacityPayload(r, a.PayloadSHA256); err != nil {
			return archiveJournal{}, err
		}
		journal, err := loadArchiveJournalForProtocol(r, 5)
		if err != nil {
			return archiveJournal{}, err
		}
		if journal.SchemaVersion != 2 {
			return archiveJournal{}, errors.New("completed archive capacity journal is not schema 2")
		}
		return journal, nil
	}
	original, target, mode, err := loadArchiveCapacityPayload(r, a.PayloadSHA256)
	if err != nil {
		return archiveJournal{}, err
	}
	if err := validateCapacityPayloadBinding(a, original, target, mode); err != nil {
		return archiveJournal{}, err
	}
	info, err := r.Lstat(archivesFile)
	if err != nil {
		return archiveJournal{}, err
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != mode {
		return archiveJournal{}, errors.New("pending archive capacity journal type or mode differs")
	}
	raw, err := boundedSnapshotFile(r, archivesFile, maxArchiveCapacityBytes)
	if err != nil {
		return archiveJournal{}, err
	}
	if bytes.Equal(raw, original) {
		return decodeArchiveJournal(raw)
	}
	if bytes.Equal(raw, target) {
		return decodeArchiveCapacityJournal(raw)
	}
	return archiveJournal{}, errors.New("pending archive capacity journal is a third state")
}

// Initial publication never replaces an existing path. Updates require an
// existing regular journal; recovery must not recreate a missing adopted file.
func saveArchiveJournal(r *os.Root, j archiveJournal, initial bool) error {
	return saveArchiveJournalForProtocol(r, j, initial, 4)
}

func saveArchiveJournalForProtocol(r *os.Root, j archiveJournal, initial bool, protocol int) error {
	var raw []byte
	var err error
	if protocol >= 5 {
		raw, err = archiveCapacityJournalBytes(j)
	} else {
		raw, err = archiveJournalBytes(j)
	}
	if err != nil {
		return err
	}
	if !initial {
		info, err := r.Lstat(archivesFile)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("archive journal is not regular")
		}
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, archivesFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		return errors.Join(r.Remove(name), syncRoot(r))
	}
	if err := r.Rename(name, archivesFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}
