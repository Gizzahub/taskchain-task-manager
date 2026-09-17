package taskstore

import (
	"errors"
	"os"
)

const archivesFile = ".task-manager-archives.json"

// Callers hold the board/common locks. Loading does not authorize protocol
// adoption or expose completion bindings; those require session scope checks.
func loadArchiveJournal(r *os.Root) (archiveJournal, error) {
	raw, err := boundedSnapshotFile(r, archivesFile, maxRepairsBytes)
	if err != nil {
		return archiveJournal{}, err
	}
	return decodeArchiveJournal(raw)
}

// Initial publication never replaces an existing path. Updates require an
// existing regular journal; recovery must not recreate a missing adopted file.
func saveArchiveJournal(r *os.Root, j archiveJournal, initial bool) error {
	raw, err := archiveJournalBytes(j)
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
