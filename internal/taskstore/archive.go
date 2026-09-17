package taskstore

import (
	"errors"
	"io/fs"
)

type ArchiveResult struct {
	SchemaVersion      int    `json:"schemaVersion"`
	RequestID          string `json:"requestId"`
	ID                 string `json:"id"`
	Source             string `json:"source"`
	Target             string `json:"target"`
	Status             string `json:"status"`
	Operation          string `json:"operation"`
	CompletionEligible bool   `json:"completionEligible"`
}

func Archive(dir string, req ArchiveRequest, adopt bool) (ArchiveResult, error) {
	return archiveWithStep(dir, req, adopt, false, nil)
}

func RecoverArchive(dir string, req ArchiveRequest) (ArchiveResult, error) {
	return archiveWithStep(dir, req, false, true, nil)
}

func archiveResult(rec archiveRecord) ArchiveResult {
	return ArchiveResult{SchemaVersion: 1, RequestID: rec.RequestID, ID: rec.ID, Source: rec.Source, Target: rec.Target, Status: "completed", Operation: rec.Operation, CompletionEligible: rec.Completion != nil}
}

func archiveWithStep(dir string, req ArchiveRequest, adopt, recoverOnly bool, step func(string) error) (result ArchiveResult, err error) {
	if err := validateArchiveRequest(req); err != nil {
		return result, err
	}
	s, err := openArchiveSession(dir, req)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	if err := s.resumeArchive(req); err != nil {
		return result, err
	}
	for _, rec := range s.archives.Records {
		if rec.RequestID != req.RequestID {
			continue
		}
		if !sameArchive(rec, req) {
			return result, errors.New("request ID already used by another archive")
		}
		if rec.State == "completed" {
			if err := s.clearArchive(req); err != nil {
				return result, err
			}
			return archiveResult(rec), nil
		}
		return s.finishArchive(rec, req, step)
	}
	if recoverOnly {
		return result, errors.New("matching archive not found; recovery never starts an operation")
	}
	rec, err := s.prepareArchive(req)
	if err != nil {
		return result, err
	}
	next := s.archives
	next.Records = append(append([]archiveRecord{}, next.Records...), rec)
	if _, err := archiveJournalBytes(next); err != nil {
		return result, err
	}
	if _, err := archiveJournalBytes(completedArchiveJournal(next)); err != nil {
		return result, err
	}
	if err := s.preflightArchiveDelta(next); err != nil {
		return result, err
	}
	if err := s.adoptArchive(adopt, step); err != nil {
		return result, err
	}
	if err := s.reserveArchive(next, req); err != nil {
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
	return s.finishArchive(rec, req, step)
}

func (s *archiveSession) finishArchive(rec archiveRecord, req ArchiveRequest, step func(string) error) (ArchiveResult, error) {
	authorize := func() error { return s.verifyPendingArchive(rec, req) }
	if err := relocateFiles(s.root, rec.Source, rec.Target, rec.Original, rec.Patched, rec.Mode, authorize, step); err != nil {
		return ArchiveResult{}, err
	}
	if err := authorize(); err != nil {
		return ArchiveResult{}, err
	}
	ok, err := matchingRelocationFile(s.root, rec.Target, rec.Patched, rec.Mode)
	if err != nil {
		return ArchiveResult{}, err
	}
	if !ok {
		return ArchiveResult{}, errors.New("archive target disappeared before receipt")
	}
	if _, err := s.root.Lstat(rec.Source); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return ArchiveResult{}, err
		}
		return ArchiveResult{}, errors.New("archive source reappeared before receipt")
	}
	completed := completedArchiveJournal(s.archives)
	if err := saveArchiveJournal(s.root, completed, false); err != nil {
		return ArchiveResult{}, err
	}
	s.archives = completed
	if err := storageStep(step, "after-archive-receipt"); err != nil {
		return ArchiveResult{}, err
	}
	if err := s.clearArchive(req); err != nil {
		return ArchiveResult{}, err
	}
	return archiveResult(rec), nil
}
