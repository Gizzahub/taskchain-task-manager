package taskstore

import (
	"errors"
	"io/fs"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

type RelocationRequest struct {
	ID, Owner, Token, RequestID, Source, Target, ExpectedSHA256 string
}

type RelocationResult struct {
	OutputVersion int                      `json:"outputVersion"`
	RequestID     string                   `json:"requestId"`
	ID            string                   `json:"id"`
	Source        string                   `json:"source"`
	Target        string                   `json:"target"`
	Status        outputvocab.ResultStatus `json:"status"`
	Changed       bool                     `json:"changed"`
}

func Relocate(dir string, req RelocationRequest, adopt bool) (RelocationResult, error) {
	return relocateWithStep(dir, req, adopt, false, nil)
}

func RecoverRelocation(dir string, req RelocationRequest) (RelocationResult, error) {
	return relocateWithStep(dir, req, false, true, nil)
}

func (req RelocationRequest) repairIdentity() RepairRequest {
	return RepairRequest{ID: req.ID, Owner: req.Owner, Token: req.Token, RequestID: req.RequestID, Path: req.Source, ExpectedSHA256: req.ExpectedSHA256}
}

func sameRelocation(rec relocationRecord, req RelocationRequest) bool {
	return rec.ID == req.ID && rec.Owner == req.Owner && rec.Token == req.Token && rec.Source == req.Source && rec.Target == req.Target && rec.ExpectedSHA256 == req.ExpectedSHA256
}

func relocationResult(rec relocationRecord) RelocationResult {
	return RelocationResult{OutputVersion: outputformat.Version, RequestID: rec.RequestID, ID: rec.ID, Source: rec.Source, Target: rec.Target, Status: outputvocab.Completed, Changed: rec.Changed}
}

func relocateWithStep(dir string, req RelocationRequest, adopt, recoverOnly bool, step func(string) error) (result RelocationResult, err error) {
	if identityKey(req.ID) == "" || !validClaimOwner(req.Owner) || !claimToken.MatchString(req.RequestID) || !sharedHex64.MatchString(req.ExpectedSHA256) || (req.Token != "" && !claimToken.MatchString(req.Token)) {
		return result, errors.New("invalid relocation request")
	}
	s, err := openRelocationSession(dir, req)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	if err := s.resumeRelocation(req); err != nil {
		return result, err
	}
	for _, rec := range s.moves.Records {
		if rec.Kind == "pending" && rec.RequestID != req.RequestID {
			return result, errors.New("pending relocation requires its original request")
		}
	}
	for _, rec := range s.moves.Records {
		if rec.RequestID != req.RequestID {
			continue
		}
		if !sameRelocation(rec, req) {
			return result, errors.New("request ID already used by another relocation")
		}
		if rec.Kind == "completed" {
			if err := s.clearRelocation(req); err != nil {
				return result, err
			}
			return relocationResult(rec), nil
		}
		return s.finishRelocation(rec, req, step)
	}
	if recoverOnly {
		return result, errors.New("matching relocation not found; recovery never starts an operation")
	}
	rec, err := s.prepareRelocation(req)
	if err != nil {
		return result, err
	}
	next := s.moves
	next.Records = append(append([]relocationRecord(nil), next.Records...), rec)
	if _, err := relocationJournalBytes(next); err != nil {
		return result, err
	}
	if err := s.adoptRelocation(adopt, step); err != nil {
		return result, err
	}
	if err := s.reserveRelocation(next, req); err != nil {
		return result, err
	}
	if err := storageStep(step, "after-relocation-common-pending"); err != nil {
		return result, err
	}
	if err := saveRelocationJournal(s.root, next, false); err != nil {
		return result, err
	}
	s.moves = next
	if err := storageStep(step, "after-relocation-journal"); err != nil {
		return result, err
	}
	return s.finishRelocation(rec, req, step)
}

func (s *relocationSession) finishRelocation(rec relocationRecord, req RelocationRequest, step func(string) error) (RelocationResult, error) {
	authorize := func() error { return s.verifyPendingRelocation(rec, req) }
	if err := relocateFiles(s.root, rec.Source, rec.Target, rec.Original, rec.Patched, rec.Mode, authorize, step); err != nil {
		return RelocationResult{}, err
	}
	if err := authorize(); err != nil {
		return RelocationResult{}, err
	}
	ok, err := matchingRelocationFile(s.root, rec.Target, rec.Patched, rec.Mode)
	if err != nil {
		return RelocationResult{}, err
	}
	if !ok {
		return RelocationResult{}, errors.New("relocation target disappeared before receipt")
	}
	if _, err := s.root.Lstat(rec.Source); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return RelocationResult{}, err
		}
		return RelocationResult{}, errors.New("relocation source reappeared before receipt")
	}
	completed := completedRelocationJournal(s.moves)
	if err := saveRelocationJournal(s.root, completed, false); err != nil {
		return RelocationResult{}, err
	}
	s.moves = completed
	if err := storageStep(step, "after-relocation-receipt"); err != nil {
		return RelocationResult{}, err
	}
	if err := s.clearRelocation(req); err != nil {
		return RelocationResult{}, err
	}
	return relocationResult(rec), nil
}
